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
	"strings"
	"time"

	"omnidatabus/internal/audit"
	"omnidatabus/internal/automap"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/naming"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/problem"
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

// Service manages mappings.
type Service struct {
	store         store.Store
	schemas       SchemaReader
	openSource    SourceOpener
	openKeyspaces KeyspaceOpener
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
}

// Summary is a mapping in the project's list (no spec).
type Summary struct {
	ID           string         `json:"id"`
	SourceSchema string         `json:"sourceSchema"`
	SourceTable  string         `json:"sourceTable"`
	Target       mapping.Target `json:"target"`
	Status       string         `json:"status"`
	AutomapStats *mapping.Stats `json:"automapStats,omitempty"`
	Stats        mapping.Stats  `json:"stats"`
	Version      int64          `json:"version"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// projectCtx is a project with its repositories and latest source snapshot.
type projectCtx struct {
	source, target store.Connection
	snap           *schema.Snapshot
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
	return projectCtx{source: src, target: tgt, snap: snap}, nil
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
func (p projectCtx) vctx(t *schema.Table, target mapping.Target) mapping.Context {
	return mapping.Context{Table: t, Target: target, Related: p.related()}
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
		Target: mapping.Target(m.Target), Spec: spec, Status: mapping.Status(found), Issues: found,
		Explain: automap.Explain(&spec, automap.Columns(t), nil),
		Version: m.Version, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		EmbedTables: map[string]EmbedTable{}, EmbedCandidates: []mapping.EmbedCandidate{},
	}
	embedded := map[string][]string{}
	rel := p.related()
	for _, e := range spec.Embeds {
		if ct := rel(e.Table.Schema, e.Table.Name); ct != nil {
			v.EmbedTables[e.ID] = EmbedTable{Table: e.Table, Columns: ct.Columns}
			embedded[e.ID] = columnNames(ct)
		}
	}
	v.Stats = mapping.ComputeStatsWith(&spec, columnNames(t), embedded)
	if p.snap != nil {
		v.EmbedCandidates = mapping.Candidates(p.snap, t, &spec)
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
	SourceSchema string                    `json:"sourceSchema" validate:"required"`
	SourceTable  string                    `json:"sourceTable" validate:"required"`
	Target       mapping.Target            `json:"target"`
	Generate     docschema.GenerateOptions `json:"generate"`
}

// SanitizeCollectionName turns a table name into a valid collection name: snake_case,
// [a-z0-9_%-] only, no leading "_" or "%", at most 251 characters (Order Details → order_details).
func SanitizeCollectionName(table string) string {
	var b strings.Builder
	for _, r := range naming.Snake(table) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-', r == '%':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteByte('_')
		}
	}
	out := strings.TrimLeft(b.String(), "_%")
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	out = strings.Trim(out, "_")
	if len(out) > 251 {
		out = strings.TrimRight(out[:251], "_")
	}
	if out == "" {
		out = "collection"
	}
	return out
}

// newSpec generates the design, key, type field and auto-map for a table (OC-035 AC1).
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
	if in.Target.Collection == "" {
		in.Target.Collection = SanitizeCollectionName(in.SourceTable)
	}
	spec, res := newSpec(t, in.Generate)
	found := issues(&spec, p.vctx(t, in.Target))
	rawSpec, err := json.Marshal(spec)
	if err != nil {
		return View{}, err
	}
	rawStats, _ := json.Marshal(res.Stats)
	m, err := s.store.Mappings().Create(ctx, ws, store.Mapping{
		ProjectID: projectID, SourceSchema: in.SourceSchema, SourceTable: in.SourceTable, Target: store.MappingTarget(in.Target),
		Spec: rawSpec, AutomapStats: rawStats, Status: mapping.Status(found), CreatedBy: userID,
	})
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
		ID: v.ID, SourceSchema: v.SourceSchema, SourceTable: v.SourceTable, Target: v.Target, Status: v.Status,
		AutomapStats: v.AutomapStats, Stats: v.Stats, Version: v.Version, UpdatedAt: v.UpdatedAt,
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
		t := p.table(m.SourceSchema, m.SourceTable)
		v := s.view(m, p, t, spec, issues(&spec, p.vctx(t, mapping.Target(m.Target))))
		out = append(out, summaryOf(v))
	}
	return out, nil
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
	p, err := s.project(ctx, ws, projectID)
	if err != nil {
		return m, spec, p, nil, err
	}
	return m, spec, p, p.table(m.SourceSchema, m.SourceTable), nil
}

// Get returns one mapping with its stats, issues and explanations.
func (s *Service) Get(ctx context.Context, ws store.WorkspaceID, projectID, id string) (View, error) {
	m, spec, p, t, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return View{}, err
	}
	return s.view(m, p, t, spec, issues(&spec, p.vctx(t, mapping.Target(m.Target)))), nil
}

// UpdateInput is a PUT body.
type UpdateInput struct {
	Spec   mapping.Spec    `json:"spec"`
	Target *mapping.Target `json:"target"`
}

// save validates and stores a new spec, mapping version conflicts to 412.
func (s *Service) save(ctx context.Context, ws store.WorkspaceID, userID string, m store.Mapping, version int64, spec mapping.Spec, p projectCtx, t *schema.Table, stats *mapping.Stats, action string) (View, error) {
	spec.Normalize()
	found := issues(&spec, p.vctx(t, mapping.Target(m.Target)))
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
	if bad := mapping.CheckReferences(&in.Spec, t, &prev, p.related()); len(bad) > 0 {
		return View{}, invalid(bad)
	}
	if in.Target != nil {
		m.Target = store.MappingTarget(*in.Target)
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
	res := automap.Run(&spec, automap.Columns(t), automap.Options{Mode: in.Mode, IncludeManual: in.IncludeManual})
	observability.AutomapDuration.Observe(time.Since(start).Seconds())
	span.End()
	spec.FieldMappings = res.FieldMappings
	return s.save(ctx, ws, userID, m, version, spec, p, t, &res.Stats, "mapping.automap")
}

// DesignInput replaces or merges the document design (OC-015 AC7).
type DesignInput struct {
	DocSchema docschema.Schema `json:"docSchema"`
	Strategy  string           `json:"strategy" validate:"required,oneof=replace merge"`
}

// DesignResult is the saved mapping plus the field mappings whose target paths disappeared.
type DesignResult struct {
	Mapping View                   `json:"mapping"`
	Orphans []mapping.FieldMapping `json:"orphans"`
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
	vctx := p.vctx(t, mapping.Target(m.Target))
	if ks, _, err := s.openKeyspaces(ctx, ws, p.target.ID); err == nil {
		_, cerr := ks.Collection(ctx, m.Target.Bucket, m.Target.Scope, m.Target.Collection)
		exists := cerr == nil
		if cerr == nil || problem.HasCode(cerr, problem.CodeNotFound) {
			vctx.TargetExists = &exists
		}
	}
	found := issues(&spec, vctx)
	return ValidationResult{Issues: found, Status: mapping.Status(found), Ready: mapping.Ready(found)}, nil
}

// AddEmbed embeds a related table (OC-021 AC2): a new object or array field, optionally with
// generated fields auto-mapped from the table's columns.
func (s *Service) AddEmbed(ctx context.Context, ws store.WorkspaceID, userID, projectID, id string, version int64, in automap.EmbedInput) (View, error) {
	m, spec, p, t, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return View{}, err
	}
	if version == 0 {
		version = m.Version
	}
	child := p.related()(in.Table.Schema, in.Table.Name)
	if child == nil {
		return View{}, tableNotFound(in.Table.Schema, in.Table.Name)
	}
	for _, j := range in.Join {
		if t.Column(j.Parent) == nil {
			return View{}, problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Can't embed", "Column "+j.Parent+" isn't in "+m.SourceTable+".")
		}
	}
	next, _, _, err := automap.AddEmbed(spec, child, in, docschema.GenerateOptions{})
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
