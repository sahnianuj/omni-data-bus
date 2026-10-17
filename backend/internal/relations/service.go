package relations

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// Virtual relationship cardinalities, as seen from the From side.
const (
	CardinalityManyToOne = "many-to-one"
	CardinalityOneToOne  = "one-to-one"
	CardinalityOneToMany = "one-to-many"
	CardinalityUnknown   = "unknown"
)

// Problem codes.
const (
	CodeRelationTypeMismatch = "RELATION_TYPE_MISMATCH"
	CodeRelationInvalid      = "RELATION_INVALID"
)

// Cardinality detection: how many distinct keys are sampled, and the statement timeout.
const (
	DetectSample  = 100_000
	DetectTimeout = 10 * time.Second
)

// SchemaReader reads a connection's latest discovery snapshot.
type SchemaReader interface {
	Latest(ctx context.Context, ws store.WorkspaceID, connectionID string) (store.DiscoverySnapshot, *schema.Snapshot, error)
}

// SourceOpener opens a session on a stored source connection.
type SourceOpener func(ctx context.Context, ws store.WorkspaceID, connectionID string) (source.Session, store.Connection, error)

// Service serves the relationship graph and a project's virtual relationships.
type Service struct {
	store      store.Store
	schemas    SchemaReader
	openSource SourceOpener
	now        func() time.Time
}

// NewService builds the service.
func NewService(st store.Store, schemas SchemaReader, openSource SourceOpener) *Service {
	return &Service{store: st, schemas: schemas, openSource: openSource, now: func() time.Time { return time.Now().UTC() }}
}

// Advice is one index recommendation (RELATION_NO_INDEX).
type Advice struct {
	Code    string           `json:"code"`
	Table   mapping.TableRef `json:"table"`
	Columns []string         `json:"columns"`
	Message string           `json:"message"`
}

// GraphResult is GET /connections/{id}/relations.
type GraphResult struct {
	Root      mapping.TableRef `json:"root"`
	Depth     int              `json:"depth"`
	Paths     []Path           `json:"paths"`
	Junctions []Junction       `json:"junctions"`
	Advice    []Advice         `json:"advice"`
}

// Graph lists the root table's relationship paths, junction candidates and index advice for a
// project's source connection, including the project's virtual relationships.
func (s *Service) Graph(ctx context.Context, ws store.WorkspaceID, connectionID string, root mapping.TableRef, depth int) (GraphResult, error) {
	conn, err := s.store.Connections().Get(ctx, ws, connectionID)
	if err != nil {
		return GraphResult{}, err
	}
	_, snap, err := s.schemas.Latest(ctx, ws, connectionID)
	if err != nil {
		return GraphResult{}, err
	}
	if snap.Table(root.Schema, root.Name) == nil {
		return GraphResult{}, problem.New(http.StatusNotFound, problem.CodeNotFound, "Table not found", "The latest schema snapshot has no table "+root.String()+".")
	}
	virtual, err := s.store.Relationships().ListForProject(ctx, ws, conn.ProjectID)
	if err != nil {
		return GraphResult{}, err
	}
	if depth <= 0 {
		depth = DefaultDepth
	}
	g := Build(snap, virtual)
	res := GraphResult{Root: root, Depth: min(depth, MaxDepth), Paths: g.Paths(root, depth), Junctions: Junctions(snap, root), Advice: []Advice{}}
	if res.Paths == nil {
		res.Paths = []Path{}
	}
	seen := map[string]bool{}
	for _, p := range res.Paths {
		for _, h := range p.Hops {
			if h.Kind != mapping.EmbedOneToMany || h.Indexed {
				continue
			}
			cols := childCols(h.Join)
			k := h.To.String() + "(" + strings.Join(cols, ",") + ")"
			if seen[k] {
				continue
			}
			seen[k] = true
			res.Advice = append(res.Advice, Advice{
				Code: mapping.CodeRelationNoIndex, Table: h.To, Columns: cols,
				Message: mapping.IndexAdvice(snap.Table(h.To.Schema, h.To.Name), cols),
			})
		}
	}
	for _, j := range res.Junctions {
		if !j.Indexed {
			cols := make([]string, len(j.ParentJoin))
			for i, pj := range j.ParentJoin {
				cols[i] = pj.Via
			}
			if k := j.Via.String() + "(" + strings.Join(cols, ",") + ")"; !seen[k] {
				seen[k] = true
				res.Advice = append(res.Advice, Advice{
					Code: mapping.CodeRelationNoIndex, Table: j.Via, Columns: cols,
					Message: mapping.IndexAdvice(snap.Table(j.Via.Schema, j.Via.Name), cols),
				})
			}
		}
	}
	return res, nil
}

// MaxHops caps one table's hops in a response (a table with more is unusual).
const MaxHops = 500

// HopsResult is GET /connections/{id}/relations/hops: every direct relationship of one table
// (OC-141), not limited by a root table's path search, self-references included.
type HopsResult struct {
	Table            mapping.TableRef `json:"table"`
	Hops             []Hop            `json:"hops"`
	Junctions        []Junction       `json:"junctions"`
	Advice           []Advice         `json:"advice"`
	MaxRelationDepth int              `json:"maxRelationDepth"`
	Truncated        bool             `json:"truncated,omitempty"`
}

// Hops lists one table's direct relationships for a project's source connection: foreign keys
// both ways and the project's virtual relationships, flagged self-references, junction
// candidates, index advice, and a rows-per-parent estimate for one-to-many hops.
func (s *Service) Hops(ctx context.Context, ws store.WorkspaceID, connectionID string, table mapping.TableRef) (HopsResult, error) {
	conn, err := s.store.Connections().Get(ctx, ws, connectionID)
	if err != nil {
		return HopsResult{}, err
	}
	_, snap, err := s.schemas.Latest(ctx, ws, connectionID)
	if err != nil {
		return HopsResult{}, err
	}
	from := snap.Table(table.Schema, table.Name)
	if from == nil {
		return HopsResult{}, problem.New(http.StatusNotFound, problem.CodeNotFound, "Table not found", "The latest schema snapshot has no table "+table.String()+".")
	}
	virtual, err := s.store.Relationships().ListForProject(ctx, ws, conn.ProjectID)
	if err != nil {
		return HopsResult{}, err
	}
	res := HopsResult{Table: table, Hops: Build(snap, virtual).Hops(table), Junctions: Junctions(snap, table), Advice: []Advice{}, MaxRelationDepth: mapping.MaxRelationDepth()}
	if res.Hops == nil {
		res.Hops = []Hop{}
	}
	if len(res.Hops) > MaxHops {
		res.Hops, res.Truncated = res.Hops[:MaxHops], true
	}
	seen := map[string]bool{}
	for i := range res.Hops {
		h := &res.Hops[i]
		h.Self = h.From == h.To
		if h.Kind != mapping.EmbedOneToMany {
			continue
		}
		to := snap.Table(h.To.Schema, h.To.Name)
		if to != nil && to.RowEstimate != nil && from.RowEstimate != nil && *from.RowEstimate > 0 {
			h.RowsPerParent = float64(*to.RowEstimate) / float64(*from.RowEstimate)
		}
		if cols := childCols(h.Join); !h.Indexed && !seen[h.To.String()+strings.Join(cols, ",")] {
			seen[h.To.String()+strings.Join(cols, ",")] = true
			res.Advice = append(res.Advice, Advice{Code: mapping.CodeRelationNoIndex, Table: h.To, Columns: cols, Message: mapping.IndexAdvice(to, cols)})
		}
	}
	return res, nil
}

// RelationshipInput creates or updates a virtual relationship.
type RelationshipInput struct {
	Name        string                `json:"name" validate:"max=200"`
	From        store.RelationshipEnd `json:"from"`
	To          store.RelationshipEnd `json:"to"`
	Cardinality string                `json:"cardinality"`
	// Detected keeps the result of a detection run (optional).
	Detected *store.RelationshipDetection `json:"detected,omitempty"`
}

// sourceSnapshot is the project's source connection and its latest snapshot; the project must
// be in the workspace (404 otherwise).
func (s *Service) sourceSnapshot(ctx context.Context, ws store.WorkspaceID, projectID string) (store.Connection, *schema.Snapshot, error) {
	if _, err := s.store.Projects().Get(ctx, ws, projectID); err != nil {
		return store.Connection{}, nil, err
	}
	conn, err := s.store.Connections().Get(ctx, ws, store.ConnectionID(projectID, store.RoleSource))
	if err != nil {
		return store.Connection{}, nil, err
	}
	_, snap, err := s.schemas.Latest(ctx, ws, conn.ID)
	return conn, snap, err
}

func invalid(code, detail string) error {
	return problem.New(http.StatusUnprocessableEntity, code, "Invalid relationship", detail)
}

// check validates the two ends against the snapshot: the tables and columns exist, both sides
// have the same number of columns, and each pair's types are compatible (OC-020 coercion rules;
// RELATION_TYPE_MISMATCH otherwise).
func check(snap *schema.Snapshot, in *RelationshipInput) error {
	if len(in.From.Columns) == 0 || len(in.From.Columns) != len(in.To.Columns) {
		return invalid(CodeRelationInvalid, "Pick the same number of columns (at least one) on both sides.")
	}
	ft, tt := snap.Table(in.From.Schema, in.From.Table), snap.Table(in.To.Schema, in.To.Table)
	switch {
	case ft == nil:
		return invalid(CodeRelationInvalid, "Table "+in.From.Schema+"."+in.From.Table+" isn't in the latest schema snapshot.")
	case tt == nil:
		return invalid(CodeRelationInvalid, "Table "+in.To.Schema+"."+in.To.Table+" isn't in the latest schema snapshot.")
	}
	for i := range in.From.Columns {
		fc, tc := ft.Column(in.From.Columns[i]), tt.Column(in.To.Columns[i])
		switch {
		case fc == nil:
			return invalid(CodeRelationInvalid, "Column "+in.From.Columns[i]+" isn't in "+ft.Name+".")
		case tc == nil:
			return invalid(CodeRelationInvalid, "Column "+in.To.Columns[i]+" isn't in "+tt.Name+".")
		case !mapping.JoinCompatible(*fc, *tc):
			return invalid(CodeRelationTypeMismatch, fmt.Sprintf("%s.%s (%s) can't be joined with %s.%s (%s).", ft.Name, fc.Name, fc.UDTName, tt.Name, tc.Name, tc.UDTName))
		}
	}
	if in.From.Schema == in.To.Schema && in.From.Table == in.To.Table && slices.Equal(in.From.Columns, in.To.Columns) {
		return invalid(CodeRelationInvalid, "This joins each row of "+ft.Name+" to itself. A self-reference uses different columns on each side, e.g. manager_id → employee_id.")
	}
	switch in.Cardinality {
	case "":
		in.Cardinality = CardinalityManyToOne
	case CardinalityManyToOne, CardinalityOneToOne, CardinalityOneToMany:
	default:
		return invalid(CodeRelationInvalid, "Cardinality must be many-to-one, one-to-one or one-to-many.")
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = fmt.Sprintf("%s.%s → %s.%s", in.From.Table, strings.Join(in.From.Columns, ","), in.To.Table, strings.Join(in.To.Columns, ","))
	}
	return nil
}

// checkDuplicate refuses a relationship the project already has (same ends and columns, in
// either direction), other than the one being updated (except).
func (s *Service) checkDuplicate(ctx context.Context, ws store.WorkspaceID, projectID, except string, in RelationshipInput) error {
	rels, err := s.store.Relationships().ListForProject(ctx, ws, projectID)
	if err != nil {
		return err
	}
	for _, r := range rels {
		if r.ID != except && (sameEnds(r.From, in.From) && sameEnds(r.To, in.To) || sameEnds(r.From, in.To) && sameEnds(r.To, in.From)) {
			return problem.New(http.StatusConflict, CodeRelationInvalid, "Relationship exists", "This relationship already exists as "+r.Name+".")
		}
	}
	return nil
}

func sameEnds(a, b store.RelationshipEnd) bool {
	return a.Schema == b.Schema && a.Table == b.Table && slices.Equal(a.Columns, b.Columns)
}

// List returns the project's virtual relationships.
func (s *Service) List(ctx context.Context, ws store.WorkspaceID, projectID string) ([]store.Relationship, error) {
	if _, err := s.store.Projects().Get(ctx, ws, projectID); err != nil {
		return nil, err
	}
	return s.store.Relationships().ListForProject(ctx, ws, projectID)
}

// Get returns one relationship.
func (s *Service) Get(ctx context.Context, ws store.WorkspaceID, projectID, id string) (store.Relationship, error) {
	return s.store.Relationships().Get(ctx, ws, projectID, id)
}

// Create stores a new virtual relationship.
func (s *Service) Create(ctx context.Context, ws store.WorkspaceID, userID, projectID string, in RelationshipInput) (store.Relationship, error) {
	_, snap, err := s.sourceSnapshot(ctx, ws, projectID)
	if err != nil {
		return store.Relationship{}, err
	}
	if err := check(snap, &in); err != nil {
		return store.Relationship{}, err
	}
	if err := s.checkDuplicate(ctx, ws, projectID, "", in); err != nil {
		return store.Relationship{}, err
	}
	return s.store.Relationships().Create(ctx, ws, store.Relationship{
		ProjectID: projectID, Name: in.Name, From: in.From, To: in.To, Cardinality: in.Cardinality, Detected: in.Detected, CreatedBy: userID,
	})
}

// Update replaces a relationship's definition; version must be the stored version (0 = any).
func (s *Service) Update(ctx context.Context, ws store.WorkspaceID, projectID, id string, version int64, in RelationshipInput) (store.Relationship, error) {
	cur, err := s.store.Relationships().Get(ctx, ws, projectID, id)
	if err != nil {
		return store.Relationship{}, err
	}
	_, snap, err := s.sourceSnapshot(ctx, ws, projectID)
	if err != nil {
		return store.Relationship{}, err
	}
	if err := check(snap, &in); err != nil {
		return store.Relationship{}, err
	}
	if err := s.checkDuplicate(ctx, ws, projectID, id, in); err != nil {
		return store.Relationship{}, err
	}
	if version == 0 {
		version = cur.Version
	}
	cur.Name, cur.From, cur.To, cur.Cardinality, cur.Version = in.Name, in.From, in.To, in.Cardinality, version
	if in.Detected != nil {
		cur.Detected = in.Detected
	}
	return s.store.Relationships().Update(ctx, ws, cur)
}

// Delete removes a relationship.
func (s *Service) Delete(ctx context.Context, ws store.WorkspaceID, projectID, id string) error {
	return s.store.Relationships().Delete(ctx, ws, projectID, id)
}

// Detection is POST …/relationships/detect: the sampled key counts and the suggested
// cardinality (the user may override it; Warning says when the choice contradicts the data).
type Detection struct {
	store.RelationshipDetection
	Warning string `json:"warning,omitempty"`
}

// Detect samples the source (OC-042 AC4): the most rows per key value on each side, among the
// first 100,000 distinct values, under a 10 s statement timeout. To unique suggests
// many-to-one (one-to-one when From is unique too); duplicates in To suggest one-to-many; a
// timeout suggests "unknown" and the user must choose.
func (s *Service) Detect(ctx context.Context, ws store.WorkspaceID, projectID string, in RelationshipInput) (Detection, error) {
	conn, snap, err := s.sourceSnapshot(ctx, ws, projectID)
	if err != nil {
		return Detection{}, err
	}
	if err := check(snap, &in); err != nil {
		return Detection{}, err
	}
	sess, _, err := s.openSource(ctx, ws, conn.ID)
	if err != nil {
		return Detection{}, err
	}
	defer func() { _ = sess.Close() }()
	kc, ok := sess.(source.KeyCounter)
	if !ok {
		return Detection{}, problem.New(http.StatusUnprocessableEntity, problem.CodeUnsupportedSource, "Can't detect", "This source can't count rows per key.")
	}
	return DetectWith(ctx, kc, in.From, in.To, in.Cardinality, s.now())
}

// DetectWith runs the detection on a key counter; chosen, when set, is compared with the
// suggestion.
func DetectWith(ctx context.Context, kc source.KeyCounter, from, to store.RelationshipEnd, chosen string, at time.Time) (Detection, error) {
	d := Detection{RelationshipDetection: store.RelationshipDetection{At: at, Suggested: CardinalityUnknown}}
	toMax, toKeys, err := kc.MaxRowsPerKey(ctx, to.Schema, to.Table, to.Columns, DetectSample, DetectTimeout)
	if errors.Is(err, source.ErrQueryTimeout) {
		d.Warning = "The sample timed out after 10 s; choose the cardinality yourself."
		return d, nil
	}
	if err != nil {
		return d, err
	}
	fromMax, fromKeys, err := kc.MaxRowsPerKey(ctx, from.Schema, from.Table, from.Columns, DetectSample, DetectTimeout)
	if errors.Is(err, source.ErrQueryTimeout) {
		d.Warning = "The sample timed out after 10 s; choose the cardinality yourself."
		return d, nil
	}
	if err != nil {
		return d, err
	}
	d.MaxPerKey, d.FromMaxPerKey, d.SampledRows = toMax, fromMax, max(toKeys, fromKeys)
	switch {
	case toMax <= 1 && fromMax <= 1:
		d.Suggested = CardinalityOneToOne
	case toMax <= 1:
		d.Suggested = CardinalityManyToOne
	default:
		d.Suggested = CardinalityOneToMany
	}
	if toMax == 0 && fromMax == 0 {
		d.Suggested = CardinalityManyToOne // no data yet: the common case
	}
	if chosen != "" && chosen != d.Suggested && d.Suggested != CardinalityUnknown && contradicts(chosen, toMax) {
		d.Warning = fmt.Sprintf("The data has up to %d %s rows per key, which contradicts %s.", toMax, to.Table, chosen)
	}
	return d, nil
}

// contradicts reports whether a chosen cardinality is impossible with the sampled data: an
// object (many-to-one, one-to-one) needs at most one To row per key.
func contradicts(chosen string, toMax int64) bool {
	return (chosen == CardinalityManyToOne || chosen == CardinalityOneToOne) && toMax > 1
}

// VirtualCandidates are embed candidates from the project's virtual relationships whose From
// or To is table (OC-042): shown with a "virtual" badge next to the foreign-key candidates.
func VirtualCandidates(snap *schema.Snapshot, table mapping.TableRef, rels []store.Relationship, spec *mapping.Spec) []mapping.EmbedCandidate {
	var out []mapping.EmbedCandidate
	for _, h := range Build(snap, rels).Hops(table) {
		if !h.Virtual {
			continue
		}
		c := mapping.EmbedCandidate{Kind: h.Kind, Table: h.To, Join: h.Join, Virtual: h.RelationshipID}
		if t := snap.Table(h.To.Schema, h.To.Name); t != nil {
			c.RowEstimate = t.RowEstimate
		}
		if spec != nil {
			for _, e := range spec.Relations {
				if e.Parent() == "" && e.Kind == c.Kind && e.Table == c.Table && len(e.Join) == len(c.Join) {
					same := true
					for i := range e.Join {
						same = same && e.Join[i] == c.Join[i]
					}
					if same {
						c.EmbedID = e.ID
					}
				}
			}
		}
		out = append(out, c)
	}
	return out
}
