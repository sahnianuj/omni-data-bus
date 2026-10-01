// Package mappings manages a project's table → collection mappings (OC-035): creation with a
// generated design and auto-map applied, versioned saves with validation, auto-map re-runs
// (OC-017), design replacement (OC-015), validation (OC-022) and the document preview.
package mappings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"omnidatabus/internal/audit"
	"omnidatabus/internal/automap"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/naming"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/plan"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/relations"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// SchemaReader returns a source repository's latest snapshot (discovery.Service).
type SchemaReader interface {
	Latest(ctx context.Context, ws store.WorkspaceID, connectionID string) (store.DiscoverySnapshot, *schema.Snapshot, error)
}

// SourceOpener opens the stored source (connections.Service.OpenSource).
type SourceOpener func(ctx context.Context, ws store.WorkspaceID, connectionID string) (source.Session, store.Connection, error)

// KeyspaceOpener opens the stored target's keyspaces (connections.Service.OpenKeyspaces).
type KeyspaceOpener func(ctx context.Context, ws store.WorkspaceID, connectionID string) (target.Keyspaces, store.Connection, error)

// LocationStatter describes a location on a stored target (connections.Service.StatLocation);
// NOT_FOUND when it doesn't exist.
type LocationStatter func(ctx context.Context, ws store.WorkspaceID, connectionID string, loc store.Location) (target.LocationNode, error)

// Service manages mappings.
type Service struct {
	store         store.Store
	schemas       SchemaReader
	openSource    SourceOpener
	openKeyspaces KeyspaceOpener
	// StatLocation checks that a mapping's location exists on any target kind (OC-036); nil
	// falls back to the Couchbase keyspace browser.
	StatLocation LocationStatter
	// CaseSensitive asks whether the target tells keys apart by case (OC-039); nil = yes.
	CaseSensitive func(ctx context.Context, ws store.WorkspaceID, connectionID string) (bool, error)
	now           func() time.Time
	// updates throttles "mapping.update" audit entries (OC-033).
	updates *audit.Recorder
}

// NewService builds the service.
func NewService(st store.Store, schemas SchemaReader, openSource SourceOpener, openKeyspaces KeyspaceOpener) *Service {
	s := &Service{store: st, schemas: schemas, openSource: openSource, openKeyspaces: openKeyspaces, now: time.Now}
	s.updates = &audit.Recorder{Store: st.Audit(), Now: func() time.Time { return s.now() }}
	return s
}

// Errors.
var (
	ErrRepositoriesIncomplete = problem.New(http.StatusUnprocessableEntity, problem.CodeRepositoriesIncomplete, "Set up both repositories first",
		"Mappings need the project's source and target repositories to be saved.")
	ErrVersionConflict = problem.New(http.StatusPreconditionFailed, problem.CodeMappingVersionConflict, "Changed in another session",
		"This mapping changed in another session. Reload it, or overwrite with your version.")
)

// View is a mapping as the API returns it.
type View struct {
	ID           string                         `json:"id"`
	ProjectID    string                         `json:"projectId"`
	SourceSchema string                         `json:"sourceSchema"`
	SourceTable  string                         `json:"sourceTable"`
	Target       mapping.Target                 `json:"target"`
	Location     mapping.Location               `json:"location"`
	Spec         mapping.Spec                   `json:"spec"`
	Status       string                         `json:"status"`
	AutomapStats *mapping.Stats                 `json:"automapStats,omitempty"`
	Stats        mapping.Stats                  `json:"stats"`
	Issues       []mapping.Issue                `json:"issues"`
	Explain      map[string]automap.Explanation `json:"explain"`
	Version      int64                          `json:"version"`
	CreatedAt    time.Time                      `json:"createdAt"`
	UpdatedAt    time.Time                      `json:"updatedAt"`
	// EmbedTables are the columns of each embedded table (by embed id), and EmbedCandidates
	// the related tables that can be embedded (OC-021).
	EmbedTables     map[string]EmbedTable    `json:"embedTables"`
	EmbedCandidates []mapping.EmbedCandidate `json:"embedCandidates"`
}

// EmbedTable is an embedded table and its columns.
type EmbedTable struct {
	Table   mapping.TableRef `json:"table"`
	Columns []schema.Column  `json:"columns"`
	// Via are a many-to-many relation's junction payload columns (sources with from: via, OC-044).
	Via []schema.Column `json:"via,omitempty"`
}

// Summary is a mapping in the project's list (no spec).
type Summary struct {
	ID           string `json:"id"`
	SourceSchema string `json:"sourceSchema"`
	SourceTable  string `json:"sourceTable"`
	// Mode is canvas or composed; Driving is the table whose rows become documents (a composed
	// mapping's driving table, else the mapping's own table, OC-047).
	Mode         string           `json:"mode"`
	Driving      mapping.TableRef `json:"driving"`
	Target       mapping.Target   `json:"target"`
	Location     mapping.Location `json:"location"`
	Status       string           `json:"status"`
	AutomapStats *mapping.Stats   `json:"automapStats,omitempty"`
	Stats        mapping.Stats    `json:"stats"`
	Version      int64            `json:"version"`
	UpdatedAt    time.Time        `json:"updatedAt"`
	// Shape counts the document's arrays and objects bound to tables, and its group-by
	// columns (OC-062: the mapping list shows "nested: 2 arrays, 1 object").
	Shape Shape `json:"shape"`
}

// Shape is a mapping's document shape at a glance.
type Shape struct {
	Arrays  int      `json:"arrays"`
	Objects int      `json:"objects"`
	GroupBy []string `json:"groupBy,omitempty"`
}

func shapeOf(spec *mapping.Spec) Shape {
	sh := Shape{GroupBy: spec.GroupBy()}
	for _, r := range spec.Relations {
		if r.LookupOnly || r.TargetFieldID == "" {
			continue
		}
		if f := spec.DocSchema.Find(r.TargetFieldID); f != nil && f.Type == docschema.Array {
			sh.Arrays++
		} else if f != nil {
			sh.Objects++
		}
	}
	return sh
}

// projectCtx is a project with its repositories and latest source snapshot.
type projectCtx struct {
	source, target store.Connection
	snap           *schema.Snapshot
	virtual        []store.Relationship // the project's virtual relationships (OC-042)
}

func (s *Service) project(ctx context.Context, ws store.WorkspaceID, projectID string) (projectCtx, error) {
	if _, err := s.store.Projects().Get(ctx, ws, projectID); err != nil {
		return projectCtx{}, err
	}
	src, err := s.store.Connections().Get(ctx, ws, store.ConnectionID(projectID, store.RoleSource))
	if errors.Is(err, store.ErrNotFound) {
		return projectCtx{}, ErrRepositoriesIncomplete
	}
	if err != nil {
		return projectCtx{}, err
	}
	tgt, err := s.store.Connections().Get(ctx, ws, store.ConnectionID(projectID, store.RoleTarget))
	if errors.Is(err, store.ErrNotFound) {
		return projectCtx{}, ErrRepositoriesIncomplete
	}
	if err != nil {
		return projectCtx{}, err
	}
	_, snap, err := s.schemas.Latest(ctx, ws, src.ID)
	if err != nil {
		return projectCtx{}, err
	}
	virtual, err := s.store.Relationships().ListForProject(ctx, ws, projectID)
	if err != nil {
		return projectCtx{}, err
	}
	return projectCtx{source: src, target: tgt, snap: snap, virtual: virtual}, nil
}

func tableNotFound(schemaName, table string) problem.Problem {
	return problem.New(http.StatusNotFound, problem.CodeNotFound, "Table not found",
		"The latest schema snapshot has no table "+schemaName+"."+table+". Refresh the schema if it was just created.")
}

// related resolves embedded tables from the latest snapshot (OC-021).
func (p projectCtx) related() mapping.RelatedTables {
	return func(schemaName, table string) *schema.Table {
		if p.snap == nil {
			return nil
		}
		return p.snap.Table(schemaName, table)
	}
}

// vctx is the validation context of a mapping.
func (p projectCtx) vctx(t *schema.Table, loc mapping.Location) mapping.Context {
	return mapping.Context{Table: t, Location: loc, Related: p.related()}
}

// kind is the project's target kind.
func (p projectCtx) kind() string { return p.target.Type }

// location resolves a request's location for this project's target kind: the 2.0 location
// when given, else the 1.0 target. An empty collection or folder defaults to the table name.
func (p projectCtx) location(loc *mapping.Location, v1 *mapping.Target, table string) (mapping.Location, error) {
	var l mapping.Location
	switch {
	case loc != nil:
		l = *loc
		if l.Kind == "" {
			l.Kind = p.kind()
		}
	case v1 != nil:
		l = mapping.LocationFromV1(*v1)
		l.Kind = store.KindCouchbase
	default:
		l.Kind = p.kind()
	}
	switch {
	case l.Kind == store.KindCouchbase && l.Collection == "":
		l.Collection = naming.CollectionName(table)
	case l.Kind == store.KindDirectory && l.Folder == "":
		l.Folder = naming.FolderName(table)
	}
	if l.Kind == store.KindCouchbase && p.kind() == store.KindCouchbase {
		return l, nil // as in 1.0, a keyspace may be incomplete until the Tables step picks it
	}
	if err := mapping.ValidateLocation(l, p.kind()); err != nil {
		return l, err
	}
	return l, nil
}

// table returns the snapshot table, or an empty table (so validation reports drift) when the
// table disappeared from the source.
func (p projectCtx) table(schemaName, table string) *schema.Table {
	if t := p.snap.Table(schemaName, table); t != nil {
		return t
	}
	return &schema.Table{Name: table}
}

// issues is every rule: structural validation plus the transform compiler's findings.
func issues(spec *mapping.Spec, vctx mapping.Context) []mapping.Issue {
	out := mapping.Validate(spec, vctx)
	out = append(out, compileIssues(spec, vctx.Table, vctx.Related)...)
	mapping.SortIssues(out)
	mapping.Annotate(spec, out) // the node each issue is on (OC-049)
	return out
}

func columnNames(t *schema.Table) []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

func decodeSpec(raw json.RawMessage) (mapping.Spec, error) {
	var s mapping.Spec
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("stored mapping spec is invalid: %w", err)
	}
	s.Normalize()
	return s, nil
}

func (s *Service) view(m store.Mapping, p projectCtx, t *schema.Table, spec mapping.Spec, found []mapping.Issue) View {
	v := View{
		ID: m.ID, ProjectID: m.ProjectID, SourceSchema: m.SourceSchema, SourceTable: m.SourceTable,
		Target: mapping.V1Target(m.Where()), Location: m.Where(), Spec: spec, Status: mapping.Status(found), Issues: found,
		Explain: automap.Explain(&spec, automap.Columns(t), nil),
		Version: m.Version, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		EmbedTables: map[string]EmbedTable{}, EmbedCandidates: []mapping.EmbedCandidate{},
	}
	rel := p.related()
	for _, e := range spec.Relations {
		if ct := rel(e.Table.Schema, e.Table.Name); ct != nil {
			et := EmbedTable{Table: e.Table, Columns: ct.Columns}
			if e.Via != nil {
				if jt := rel(e.Via.Table.Schema, e.Via.Table.Name); jt != nil {
					for _, c := range e.Via.Columns {
						if jc := jt.Column(c); jc != nil {
							et.Via = append(et.Via, *jc)
						}
					}
				}
			}
			v.EmbedTables[e.ID] = et
		}
	}
	v.Stats = mapping.ComputeStatsWith(&spec, columnNames(t), embeddedColumns(&spec, p))
	if p.snap != nil {
		v.EmbedCandidates = mapping.Candidates(p.snap, t, &spec)
		v.EmbedCandidates = append(v.EmbedCandidates, relations.VirtualCandidates(p.snap, driving(m, &spec), p.virtual, &spec)...)
	}
	if len(m.AutomapStats) > 0 {
		var st mapping.Stats
		if json.Unmarshal(m.AutomapStats, &st) == nil {
			v.AutomapStats = &st
		}
	}
	return v
}

// CreateInput creates one mapping.
type CreateInput struct {
	SourceSchema string `json:"sourceSchema" validate:"required"`
	SourceTable  string `json:"sourceTable" validate:"required"`
	// Target is the 1.0 Couchbase form; Location (2.0) wins when both are sent (OC-036).
	Target   mapping.Target            `json:"target"`
	Location *mapping.Location         `json:"location,omitempty"`
	Generate docschema.GenerateOptions `json:"generate"`
}

// newSpec generates the design, key, type field and auto-map for a table (OC-035 AC1). They
// don't depend on the target kind: a mapping builds the same keys and documents whether it
// writes to Couchbase or to a directory.
func newSpec(t *schema.Table, gen docschema.GenerateOptions) (mapping.Spec, automap.Result) {
	tpl, _ := mapping.DefaultKeyTemplate(t)
	spec := mapping.Spec{
		DocSchema:    docschema.FromTable(t, gen),
		Key:          mapping.KeySpec{Template: tpl},
		TypeField:    mapping.TypeField{Enabled: true, Name: "type", Value: naming.SingularSnake(t.Name)},
		NullHandling: mapping.NullInclude,
	}
	for _, c := range spec.DocSchema.Root.Children {
		if c.Name == "type" {
			spec.TypeField.Name = "docType" // don't collide with a column called type
		}
	}
	spec.Normalize()
	res := automap.Run(&spec, automap.Columns(t), automap.Options{})
	spec.FieldMappings = res.FieldMappings
	spec.Normalize()
	return spec, res
}

// Create creates a mapping with a generated design and auto-map applied.
func (s *Service) Create(ctx context.Context, ws store.WorkspaceID, userID, projectID string, in CreateInput) (View, error) {
	p, err := s.project(ctx, ws, projectID)
	if err != nil {
		return View{}, err
	}
	return s.create(ctx, ws, userID, projectID, p, in)
}

func (s *Service) create(ctx context.Context, ws store.WorkspaceID, userID, projectID string, p projectCtx, in CreateInput) (View, error) {
	t := p.snap.Table(in.SourceSchema, in.SourceTable)
	if t == nil {
		return View{}, tableNotFound(in.SourceSchema, in.SourceTable)
	}
	var v1 *mapping.Target
	if in.Target != (mapping.Target{}) {
		v1 = &in.Target // only a target the client sent; none lets the project's kind decide
	}
	loc, err := p.location(in.Location, v1, in.SourceTable)
	if err != nil {
		return View{}, err
	}
	spec, res := newSpec(t, in.Generate)
	spec.Root = &mapping.Root{Table: mapping.TableRef{Schema: in.SourceSchema, Name: in.SourceTable}}
	found := issues(&spec, p.vctx(t, loc))
	rawSpec, err := json.Marshal(spec)
	if err != nil {
		return View{}, err
	}
	rawStats, _ := json.Marshal(res.Stats)
	nm := store.Mapping{
		ProjectID: projectID, SourceSchema: in.SourceSchema, SourceTable: in.SourceTable,
		Spec: rawSpec, AutomapStats: rawStats, Status: mapping.Status(found), CreatedBy: userID,
	}
	nm.SetLocation(loc)
	m, err := s.store.Mappings().Create(ctx, ws, nm)
	var ce *store.ConflictError
	if errors.As(err, &ce) {
		return View{}, problem.New(http.StatusConflict, problem.CodeMappingExists, "Already mapped",
			in.SourceSchema+"."+in.SourceTable+" already has a mapping in this project.")
	}
	if err != nil {
		return View{}, err
	}
	s.audit(ctx, ws, userID, "mapping.create", m)
	return s.view(m, p, t, spec, found), nil
}

// BulkResult is one table's outcome in a bulk create.
type BulkResult struct {
	SourceSchema string           `json:"sourceSchema"`
	SourceTable  string           `json:"sourceTable"`
	Mapping      *Summary         `json:"mapping,omitempty"`
	Error        *problem.Problem `json:"error,omitempty"`
}

// BulkCreate creates a mapping per table; one table's failure doesn't stop the others.
func (s *Service) BulkCreate(ctx context.Context, ws store.WorkspaceID, userID, projectID string, items []CreateInput) ([]BulkResult, error) {
	p, err := s.project(ctx, ws, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]BulkResult, 0, len(items))
	for _, in := range items {
		r := BulkResult{SourceSchema: in.SourceSchema, SourceTable: in.SourceTable}
		v, err := s.create(ctx, ws, userID, projectID, p, in)
		if err != nil {
			pr := problem.From(err)
			r.Error = &pr
		} else {
			sum := summaryOf(v)
			r.Mapping = &sum
		}
		out = append(out, r)
	}
	return out, nil
}

func summaryOf(v View) Summary {
	return Summary{
		ID: v.ID, SourceSchema: v.SourceSchema, SourceTable: v.SourceTable, Mode: v.Spec.Mode,
		Driving: mapping.DrivingTable(&v.Spec, mapping.TableRef{Schema: v.SourceSchema, Name: v.SourceTable}),
		Target:  v.Target, Location: v.Location, Status: v.Status,
		AutomapStats: v.AutomapStats, Stats: v.Stats, Version: v.Version, UpdatedAt: v.UpdatedAt,
		Shape: shapeOf(&v.Spec),
	}
}

// List returns the project's mappings with live stats and status (against the latest snapshot).
func (s *Service) List(ctx context.Context, ws store.WorkspaceID, projectID string) ([]Summary, error) {
	ms, err := s.store.Mappings().ListForProject(ctx, ws, projectID)
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	if len(ms) == 0 {
		if _, err := s.store.Projects().Get(ctx, ws, projectID); err != nil {
			return nil, err
		}
		return out, nil
	}
	p, err := s.project(ctx, ws, projectID)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		spec, err := decodeSpec(m.Spec)
		if err != nil {
			return nil, err
		}
		rootOf(m, &spec)
		drv := driving(m, &spec)
		t := p.table(drv.Schema, drv.Name)
		v := s.view(m, p, t, spec, issues(&spec, p.vctx(t, m.Where())))
		out = append(out, summaryOf(v))
	}
	return out, nil
}

// rootOf names the mapping's table in its spec (v2 root; 1.0 specs don't have it until saved).
// Only a composed spec may be driven by another table (OC-047).
func rootOf(m store.Mapping, spec *mapping.Spec) {
	own := mapping.TableRef{Schema: m.SourceSchema, Name: m.SourceTable}
	if spec.Root == nil || (!spec.Composed() && spec.Root.Table != own) {
		spec.Root = &mapping.Root{Table: own}
	}
}

// driving is the table whose rows become m's documents.
func driving(m store.Mapping, spec *mapping.Spec) mapping.TableRef {
	return mapping.DrivingTable(spec, mapping.TableRef{Schema: m.SourceSchema, Name: m.SourceTable})
}

// PlanHash compiles the execution plan of m's spec against the latest snapshot and returns
// its hash (recorded on jobs, OC-037).
func (s *Service) PlanHash(ctx context.Context, ws store.WorkspaceID, m store.Mapping) (string, error) {
	spec, err := decodeSpec(m.Spec)
	if err != nil {
		return "", err
	}
	p, err := s.project(ctx, ws, m.ProjectID)
	if err != nil {
		return "", err
	}
	drv := driving(m, &spec)
	t := p.snap.Table(drv.Schema, drv.Name)
	if t == nil {
		return "", tableNotFound(drv.Schema, drv.Name)
	}
	pl, _, err := plan.Compile(plan.Input{Spec: &spec, Root: drv, Table: t, Related: p.related()})
	if err != nil {
		return "", err
	}
	return pl.Hash, nil
}

func (s *Service) load(ctx context.Context, ws store.WorkspaceID, projectID, id string) (store.Mapping, mapping.Spec, projectCtx, *schema.Table, error) {
	m, err := s.store.Mappings().Get(ctx, ws, id)
	if err == nil && m.ProjectID != projectID {
		err = store.ErrNotFound
	}
	if err != nil {
		return m, mapping.Spec{}, projectCtx{}, nil, err
	}
	spec, err := decodeSpec(m.Spec)
	if err != nil {
		return m, spec, projectCtx{}, nil, err
	}
	rootOf(m, &spec)
	p, err := s.project(ctx, ws, projectID)
	if err != nil {
		return m, spec, p, nil, err
	}
	drv := driving(m, &spec)
	return m, spec, p, p.table(drv.Schema, drv.Name), nil
}

// Get returns one mapping with its stats, issues and explanations.
func (s *Service) Get(ctx context.Context, ws store.WorkspaceID, projectID, id string) (View, error) {
	m, spec, p, t, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return View{}, err
	}
	return s.view(m, p, t, spec, issues(&spec, p.vctx(t, m.Where()))), nil
}

// UpdateInput is a PUT body.
type UpdateInput struct {
	Spec   mapping.Spec    `json:"spec"`
	Target *mapping.Target `json:"target"`
	// Location (2.0) replaces Target (1.0) when both are sent (OC-036).
	Location *mapping.Location `json:"location,omitempty"`
}

// save validates and stores a new spec, mapping version conflicts to 412.
func (s *Service) save(ctx context.Context, ws store.WorkspaceID, userID string, m store.Mapping, version int64, spec mapping.Spec, p projectCtx, t *schema.Table, stats *mapping.Stats, action string) (View, error) {
	spec.Normalize()
	found := issues(&spec, p.vctx(t, m.Where()))
	raw, err := json.Marshal(spec)
	if err != nil {
		return View{}, err
	}
	m.Spec, m.Status, m.Version = raw, mapping.Status(found), version
	if stats != nil {
		m.AutomapStats, _ = json.Marshal(stats)
	}
	saved, err := s.store.Mappings().Update(ctx, ws, m)
	if errors.Is(err, store.ErrVersionConflict) {
		return View{}, ErrVersionConflict
	}
	if err != nil {
		return View{}, err
	}
	s.audit(ctx, ws, userID, action, saved)
	return s.view(saved, p, t, spec, found), nil
}

// Update saves an edited spec (and target). version is the If-Match version (0 = the stored
// one). References to unknown target fields, or to columns that are neither in the source
// nor in the stored spec, are rejected with 422 (OC-019 AC9).
func (s *Service) Update(ctx context.Context, ws store.WorkspaceID, userID, projectID, id string, version int64, in UpdateInput) (View, error) {
	m, prev, p, t, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return View{}, err
	}
	if version == 0 {
		version = m.Version
	}
	in.Spec.Normalize()
	rootOf(m, &in.Spec)
	// A composed mapping may change its driving table to any table or view (OC-047); bindings
	// that no longer resolve are flagged by validation, never removed.
	if drv := driving(m, &in.Spec); drv != driving(m, &prev) {
		if p.snap.Table(drv.Schema, drv.Name) == nil {
			return View{}, tableNotFound(drv.Schema, drv.Name)
		}
		t = p.table(drv.Schema, drv.Name)
	}
	if bad := mapping.CheckReferences(&in.Spec, t, &prev, p.related()); len(bad) > 0 {
		return View{}, invalid(bad)
	}
	if in.Location != nil || in.Target != nil {
		loc, err := p.location(in.Location, in.Target, m.SourceTable)
		if err != nil {
			return View{}, err
		}
		m.SetLocation(loc)
	}
	return s.save(ctx, ws, userID, m, version, in.Spec, p, t, nil, "mapping.update")
}

func invalid(found []mapping.Issue) problem.Problem {
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeMappingInvalid, "Invalid mapping", found[0].Message)
	for _, i := range found {
		p.FieldErrors = append(p.FieldErrors, problem.FieldError{Field: i.Path, Message: i.Message})
	}
	return p
}

// Delete removes a mapping.
func (s *Service) Delete(ctx context.Context, ws store.WorkspaceID, userID, projectID, id string) error {
	m, err := s.store.Mappings().Get(ctx, ws, id)
	if err == nil && m.ProjectID != projectID {
		err = store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := s.store.Mappings().Delete(ctx, ws, id); err != nil {
		return err
	}
	s.audit(ctx, ws, userID, "mapping.delete", m)
	return nil
}

// AutomapInput is POST …/automap.
type AutomapInput struct {
	Mode          string `json:"mode" validate:"omitempty,oneof=fill reset"`
	IncludeManual bool   `json:"includeManual"`
	// NodeID auto-maps one bound scope of a composed document; AllScopes every one of them,
	// from the root down (OC-049). Both only fill unmapped fields.
	NodeID    string `json:"nodeId"`
	AllScopes bool   `json:"allScopes"`
}

// Automap re-runs auto-mapping (OC-017 AC9). The result's stats become the mapping's
// automap snapshot (the "initial score" shown in the header).
func (s *Service) Automap(ctx context.Context, ws store.WorkspaceID, userID, projectID, id string, version int64, in AutomapInput) (View, error) {
	m, spec, p, t, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return View{}, err
	}
	if version == 0 {
		version = m.Version
	}
	_, span := observability.Tracer().Start(ctx, "automap")
	start := time.Now()
	var res automap.Result
	switch {
	case in.AllScopes:
		res, err = automap.RunAll(&spec, t, p.related())
	case in.NodeID != "":
		res, err = automap.RunScope(&spec, in.NodeID, t, p.related())
	default:
		res = automap.Run(&spec, automap.Columns(t), automap.Options{Mode: in.Mode, IncludeManual: in.IncludeManual})
	}
	observability.AutomapDuration.Observe(time.Since(start).Seconds())
	span.End()
	if err != nil {
		return View{}, err
	}
	spec.FieldMappings = res.FieldMappings
	if in.AllScopes || in.NodeID != "" {
		// A scoped run's own stats cover its scope; the header shows the whole mapping's.
		res.Stats = mapping.ComputeStatsWith(&spec, columnNames(t), embeddedColumns(&spec, p))
	}
	return s.save(ctx, ws, userID, m, version, spec, p, t, &res.Stats, "mapping.automap")
}

// DesignInput replaces or merges the document design (OC-015 AC7).
type DesignInput struct {
	DocSchema docschema.Schema `json:"docSchema"`
	Strategy  string           `json:"strategy" validate:"required,oneof=replace merge reconcile"`
	// Declined lists current field IDs whose proposed rename or move the user refused
	// (reconcile, OC-061).
	Declined []string `json:"declined,omitempty"`
}

// DesignResult is the saved mapping plus the field mappings whose target paths disappeared,
// and for reconcile what changed.
type DesignResult struct {
	Mapping View                   `json:"mapping"`
	Orphans []mapping.FieldMapping `json:"orphans"`
	Diff    *docschema.Diff        `json:"diff,omitempty"`
}

// ReplaceDesign applies a new design: field mappings are re-attached by path (orphans are
// returned) and auto-map fills the gaps.
func (s *Service) ReplaceDesign(ctx context.Context, ws store.WorkspaceID, userID, projectID, id string, version int64, in DesignInput) (DesignResult, error) {
	m, spec, p, t, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return DesignResult{}, err
	}
	if version == 0 {
		version = m.Version
	}
	next := in.DocSchema
	if in.Strategy == mapping.StrategyReconcile {
		// Follow the revision; fields that still exist keep their mappings and bindings, and
		// nothing is auto-mapped (the added fields stay unmapped).
		out, diff := docschema.Reconcile(&spec.DocSchema, &in.DocSchema, in.Declined)
		if bad := docschema.Validate(&out); len(bad) > 0 {
			return DesignResult{}, docschema.Problem(bad)
		}
		spec.DocSchema = out
		orphans := mapping.Prune(&spec)
		mapping.SyncBindings(&spec)
		v, err := s.save(ctx, ws, userID, m, version, spec, p, t, nil, "mapping.design")
		if err != nil {
			return DesignResult{}, err
		}
		if orphans == nil {
			orphans = []mapping.FieldMapping{}
		}
		return DesignResult{Mapping: v, Orphans: orphans, Diff: &diff}, nil
	}
	if in.Strategy == mapping.StrategyMerge {
		next = mapping.MergeDesigns(&spec.DocSchema, &in.DocSchema)
	}
	if bad := docschema.Validate(&next); len(bad) > 0 {
		return DesignResult{}, docschema.Problem(bad)
	}
	kept, orphans := mapping.ReattachByPath(&spec.DocSchema, &next, spec.FieldMappings)
	spec.DocSchema, spec.FieldMappings = next, kept
	res := automap.Run(&spec, automap.Columns(t), automap.Options{})
	spec.FieldMappings = res.FieldMappings
	v, err := s.save(ctx, ws, userID, m, version, spec, p, t, &res.Stats, "mapping.design")
	if err != nil {
		return DesignResult{}, err
	}
	return DesignResult{Mapping: v, Orphans: orphans}, nil
}

// ValidationResult is POST …/validate.
type ValidationResult struct {
	Issues []mapping.Issue `json:"issues"`
	Status string          `json:"status"`
	Ready  bool            `json:"ready"`
}

// Validate runs every rule, including whether the target keyspace exists (contacting the
// target cluster) and transform compilation (OC-022).
func (s *Service) Validate(ctx context.Context, ws store.WorkspaceID, projectID, id string) (ValidationResult, error) {
	m, spec, p, t, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return ValidationResult{}, err
	}
	vctx := p.vctx(t, m.Where())
	if s.CaseSensitive != nil && m.Where().Kind == store.KindDirectory {
		if sensitive, err := s.CaseSensitive(ctx, ws, p.target.ID); err == nil {
			vctx.CaseInsensitive = !sensitive
		}
	}
	if checked, cerr := s.statLocation(ctx, ws, p.target.ID, m.Where()); checked {
		exists := cerr == nil
		if cerr == nil || problem.HasCode(cerr, problem.CodeNotFound) {
			vctx.TargetExists = &exists
		}
	}
	found := issues(&spec, vctx)
	return ValidationResult{Issues: found, Status: mapping.Status(found), Ready: mapping.Ready(found)}, nil
}

// AddEmbed adds a relation (OC-021 AC2; nested, lookup-only, many-to-many, shapes and paths
// from OC-043 – OC-045): a new field inside the root or the parent relation's object, optionally
// with generated fields auto-mapped from the table's columns.
func (s *Service) AddEmbed(ctx context.Context, ws store.WorkspaceID, userID, projectID, id string, version int64, in automap.EmbedInput) (View, error) {
	m, spec, p, t, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return View{}, err
	}
	if version == 0 {
		version = m.Version
	}
	rel := p.related()
	cant := func(msg string) (View, error) {
		return View{}, problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Can't embed", msg)
	}
	// Each hop's parent columns must be in its parent's table: the root, the relation it nests
	// under, or the hop before it.
	parent, parentName := t, driving(m, &spec).Name
	if in.ParentID != "" {
		pr := spec.RelationByID(in.ParentID)
		if pr == nil {
			return cant("Relation " + in.ParentID + " doesn't exist.")
		}
		if parent = rel(pr.Table.Schema, pr.Table.Name); parent == nil {
			return View{}, tableNotFound(pr.Table.Schema, pr.Table.Name)
		}
		parentName = pr.Table.String()
	}
	for hop := &in; hop != nil; {
		child := rel(hop.Table.Schema, hop.Table.Name)
		if child == nil {
			return View{}, tableNotFound(hop.Table.Schema, hop.Table.Name)
		}
		cols := []string{}
		for _, j := range hop.Join {
			cols = append(cols, j.Parent)
		}
		if hop.Kind == mapping.RelationManyToMany && hop.Via != nil {
			cols = cols[:0]
			for _, j := range hop.Via.ParentJoin {
				cols = append(cols, j.Parent)
			}
		}
		for _, c := range cols {
			if parent.Column(c) == nil {
				return cant("Column " + c + " isn't in " + parentName + ".")
			}
		}
		parent, parentName = child, hop.Table.String()
		if len(hop.Then) == 0 {
			break
		}
		next := hop.Then[0]
		next.Then = hop.Then[1:]
		hop = &next
	}
	next, _, _, err := automap.AddRelation(spec, rel, in, docschema.GenerateOptions{})
	if err != nil {
		return View{}, err
	}
	return s.save(ctx, ws, userID, m, version, next, p, t, nil, "mapping.update")
}

func (s *Service) audit(ctx context.Context, ws store.WorkspaceID, user, action string, m store.Mapping) {
	// The canvas saves often: one "mapping.update" per mapping per 10 minutes.
	if action == "mapping.update" && s.updates.Throttled(string(ws)+"/"+m.ID, audit.MappingUpdateEvery) {
		return
	}
	meta, _ := json.Marshal(map[string]string{"projectId": m.ProjectID, "table": m.SourceSchema + "." + m.SourceTable})
	if err := s.store.Audit().Record(ctx, store.AuditEvent{WorkspaceID: ws, UserID: user, Action: action, EntityType: "mapping", EntityID: m.ID, Metadata: meta}); err != nil {
		slog.WarnContext(ctx, "audit record failed", "action", action, "error", err.Error())
	}
}

// statLocation checks that loc exists on the stored target; checked is false when the target
// couldn't be asked (the result then says nothing).
func (s *Service) statLocation(ctx context.Context, ws store.WorkspaceID, connID string, loc store.Location) (checked bool, err error) {
	if s.StatLocation != nil {
		_, err := s.StatLocation(ctx, ws, connID, loc)
		return err == nil || problem.HasCode(err, problem.CodeNotFound), err
	}
	if loc.Kind != store.KindCouchbase || s.openKeyspaces == nil {
		return false, nil
	}
	ks, _, err := s.openKeyspaces(ctx, ws, connID)
	if err != nil {
		return false, nil //nolint:nilerr // an unreachable target leaves the location unchecked
	}
	_, err = ks.Collection(ctx, loc.Bucket, loc.Scope, loc.Collection)
	return true, err
}

// embeddedColumns are the column names of each relation's table (for the score's embedded group).
func embeddedColumns(spec *mapping.Spec, p projectCtx) map[string][]string {
	out := map[string][]string{}
	rel := p.related()
	for _, e := range spec.Relations {
		if ct := rel(e.Table.Schema, e.Table.Name); ct != nil {
			out[e.ID] = columnNames(ct)
		}
	}
	return out
}
