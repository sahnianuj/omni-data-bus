package mappings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"omnidatabus/internal/docschema"

	"github.com/jackc/pgx/v5/pgconn"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/encode"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/plan"
	"omnidatabus/internal/preview"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/related"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/transform"
)

// Preview limits (OC-022 AC4).
const (
	previewDefault    = 10
	previewMin        = 1
	previewMax        = 50
	previewTimeoutSec = 10
)

// transformCodes are the issues only the compiler finds (the others come from Validate).
var transformCodes = map[string]bool{mapping.CodeTransformInvalid: true, transform.CodeTypeMismatch: true}

// compileIssues are the transform compiler's findings (bad args, regex, expressions, types).
func compileIssues(spec *mapping.Spec, t *schema.Table, rel mapping.RelatedTables) []mapping.Issue {
	_, issues := transform.Compile(spec, t, transform.Options{Related: related.Tables(spec, rel)})
	var out []mapping.Issue
	for _, i := range issues {
		// An expression reading a relation outside its field's scope is only found by the
		// compiler (at its position, OC-047); a source outside it is found by Validate.
		if transformCodes[i.Code] || (i.Code == mapping.CodeSourceOutOfScope && i.Line > 0) {
			out = append(out, i)
		}
	}
	return out
}

// PreviewInput is POST …/preview.
type PreviewInput struct {
	Limit          int           `json:"limit"`
	SampleMode     string        `json:"sampleMode" validate:"omitempty,oneof=first random"`
	FieldMappingID string        `json:"fieldMappingId"`
	SpecOverride   *mapping.Spec `json:"specOverride"`
	CheckKeys      bool          `json:"checkKeys"`
	WriteMode      string        `json:"writeMode" validate:"omitempty,oneof=upsert insert"`
	// RootKey previews one root row: driving table column → value (OC-049).
	RootKey map[string]any `json:"rootKey"`
	// WorstCase previews the root row with the most rows of this relation (OC-049).
	WorstCase string `json:"worstCase"`
}

// PreviewItem is one sample row and the document it becomes.
type PreviewItem struct {
	SourceRow map[string]any                   `json:"sourceRow"`
	Key       string                           `json:"key,omitempty"`
	Document  json.RawMessage                  `json:"document,omitempty"`
	SizeBytes int                              `json:"sizeBytes"`
	Warnings  []transform.Warning              `json:"warnings"`
	Error     *transform.RowError              `json:"error"`
	Trace     map[string][]transform.TraceStep `json:"trace,omitempty"`
	// KeyExists is set by the collision check: the target already has this key.
	KeyExists *bool `json:"keyExists,omitempty"`
	// Path is the file the document would be written to (directory targets, OC-039).
	Path string `json:"path,omitempty"`
	// Stats describe each relation node by dotted path (items, items.product; OC-049).
	Stats map[string]preview.NodeStats `json:"stats,omitempty"`
	// Conformance compares the document's shape with the imported design (OC-061).
	Conformance *docschema.Conformance `json:"conformance,omitempty"`
}

// WorstCase is the root row with the most rows of one relation, and its full size (OC-049).
type WorstCase struct {
	RelationID     string `json:"relationId"`
	Path           string `json:"path"`
	Rows           int64  `json:"rows"`
	EstimatedBytes int64  `json:"estimatedBytes"`
}

// PreviewSummary counts the sample.
type PreviewSummary struct {
	OK           int `json:"ok"`
	Errors       int `json:"errors"`
	AvgSizeBytes int `json:"avgSizeBytes"`
	MaxSizeBytes int `json:"maxSizeBytes"`
	Collisions   int `json:"collisions"`
}

// FieldPreview is the Inspector's before/after for one field mapping (OC-019 AC8).
type FieldPreview struct {
	Before any                   `json:"before"`
	After  any                   `json:"after"`
	Error  *transform.RowError   `json:"error,omitempty"`
	Steps  []transform.TraceStep `json:"steps"`
}

// PreviewResult is either items + summary, or fields (with fieldMappingId).
type PreviewResult struct {
	Items     []PreviewItem   `json:"items,omitempty"`
	Summary   *PreviewSummary `json:"summary,omitempty"`
	Fields    []FieldPreview  `json:"fields,omitempty"`
	WriteMode string          `json:"writeMode"`
	WorstCase *WorstCase      `json:"worstCase,omitempty"`
}

// ErrPreviewTimeout is a sample query that exceeded the statement timeout.
var ErrPreviewTimeout = problem.New(http.StatusGatewayTimeout, problem.CodePreviewTimeout, "Sample took too long",
	"Reading sample rows exceeded 10 seconds. Try the First sample mode, or fewer rows.")

// Preview runs sample rows through the compiled program — the same one a migration runs —
// and never writes to the target (OC-022).
func (s *Service) Preview(ctx context.Context, ws store.WorkspaceID, projectID, id string, in PreviewInput) (PreviewResult, error) {
	ctx, span := observability.Tracer().Start(ctx, "mapping.preview")
	defer span.End()
	m, spec, p, t, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return PreviewResult{}, err
	}
	if in.SpecOverride != nil {
		spec = *in.SpecOverride // preview unsaved edits
		spec.Normalize()
		rootOf(m, &spec)
		d := driving(m, &spec)
		t = p.table(d.Schema, d.Name)
	}
	drv := driving(m, &spec)
	switch {
	case in.Limit == 0:
		in.Limit = previewDefault
	case in.Limit < previewMin:
		in.Limit = previewMin
	case in.Limit > previewMax:
		in.Limit = previewMax
	}
	if in.WriteMode == "" {
		in.WriteMode = "upsert"
	}
	if p.snap.Table(drv.Schema, drv.Name) == nil {
		return PreviewResult{}, tableNotFound(drv.Schema, drv.Name)
	}
	// The same execution plan a migration runs (OC-037).
	pl, found, err := plan.Compile(plan.Input{
		Spec: &spec, Root: drv, Table: t, Related: p.related(),
	})
	if err != nil {
		return PreviewResult{}, problem.New(http.StatusUnprocessableEntity, problem.CodeMappingInvalid, "Invalid mapping", err.Error())
	}
	prog := pl.Program
	if prog == nil {
		return PreviewResult{}, invalid(found)
	}
	// One read session for the root rows, their related rows and the counts (OC-049).
	ctx, cancel := context.WithTimeout(ctx, previewTimeoutSec*time.Second)
	defer cancel()
	rs, closeRead, err := s.openRead(ctx, ws, p)
	if err != nil {
		return PreviewResult{}, err
	}
	defer closeRead()
	var rows [][]any
	var worst *WorstCase
	switch {
	case in.WorstCase != "":
		var wc *preview.WorstCase
		rows, wc, err = preview.WorstCaseRows(ctx, rs, pl, &spec, drv, t, in.WorstCase)
		if wc != nil {
			worst = &WorstCase{RelationID: wc.RelationID, Path: wc.Path, Rows: wc.Rows}
		}
	case len(in.RootKey) > 0:
		rows, err = preview.RowsByKey(ctx, rs, drv, t, in.RootKey)
	default:
		rows, err = s.sampleRows(ctx, ws, p, drv.Schema, t, in, spec.GroupBy())
	}
	if err != nil {
		return PreviewResult{}, previewErr(ctx, err)
	}
	rel, fails, totals, err := preview.Related(ctx, rs, pl, &spec, rows)
	if err != nil {
		return PreviewResult{}, previewErr(ctx, err)
	}
	if in.FieldMappingID != "" {
		return s.fieldPreview(prog, &spec, rows, in.FieldMappingID)
	}
	out := PreviewResult{Items: make([]PreviewItem, 0, len(rows)), Summary: &PreviewSummary{}, WriteMode: in.WriteMode, WorstCase: worst}
	docPath := s.docPath(p, m.Where())
	var total int
	buf := make([]byte, 0, 4096)
	for i, row := range rows {
		item := PreviewItem{SourceRow: sourceRow(t, row), Warnings: []transform.Warning{}}
		if f, failed := fails[i]; failed {
			item.Error = &transform.RowError{Code: f.Code, Message: f.Message}
			out.Summary.Errors++
			out.Items = append(out.Items, item)
			continue
		}
		var rowRel source.Related
		if rel != nil {
			rowRel = related.Row(rel, i)
			if pl.HasRelations() {
				item.Stats = preview.Stats(&spec, rowRel, totals[i])
			}
			rowRel = preview.Trim(rowRel)
		}
		key, doc, warnings, trace, rerr := prog.ApplyTraceWith(row, rowRel, buf)
		item.Trace = trace
		if rerr != nil {
			item.Error = rerr
			out.Summary.Errors++
		} else {
			item.Key, item.Document, item.SizeBytes = key, append(json.RawMessage(nil), doc...), len(doc)
			item.Path = docPath(key)
			item.Warnings = append(item.Warnings, warnings...)
			if imp := spec.DocSchema.Imported; imp != nil && len(imp.Fields) > 0 {
				c := docschema.Conform(imp.Fields, item.Document)
				item.Conformance = &c
			}
			out.Summary.OK++
			total += len(doc)
			out.Summary.MaxSizeBytes = max(out.Summary.MaxSizeBytes, len(doc))
			if worst != nil {
				shown := 0
				if st, ok := item.Stats[worst.Path]; ok {
					shown = st.Rows
				}
				worst.EstimatedBytes = preview.EstimateBytes(doc, strings.Split(fieldPathOf(&spec, worst.RelationID), "."), shown, worst.Rows)
			}
		}
		out.Items = append(out.Items, item)
	}
	if out.Summary.OK > 0 {
		out.Summary.AvgSizeBytes = total / out.Summary.OK
	}
	if in.CheckKeys {
		if err := s.checkKeys(ctx, ws, p, m, &out); err != nil {
			return PreviewResult{}, err
		}
	}
	return out, nil
}

func (s *Service) sampleRows(ctx context.Context, ws store.WorkspaceID, p projectCtx, schemaName string, t *schema.Table, in PreviewInput, groupBy []string) ([][]any, error) {
	sess, _, err := s.openSource(ctx, ws, p.source.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sess.Close() }()
	req := source.PreviewRequest{
		Schema: schemaName, Table: t.Name, Columns: t.Columns, OrderBy: t.PrimaryKey, Limit: in.Limit,
		Random: in.SampleMode == "random", TimeoutSec: previewTimeoutSec, DistinctOn: groupBy, // one row per group (OC-063)
	}
	if t.RowEstimate != nil {
		req.RowEstimate = *t.RowEstimate
	}
	rows, err := sess.Preview(ctx, req)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "57014" { // query_canceled: statement timeout
			return nil, ErrPreviewTimeout
		}
		return nil, connectors.Classify(connectors.RoleSource, err)
	}
	return rows, nil
}

// openRead opens a read session on the source for the preview; rs is nil when the source
// can't stream (then the preview has no related rows).
func (s *Service) openRead(ctx context.Context, ws store.WorkspaceID, p projectCtx) (source.ReadSession, func(), error) {
	sess, _, err := s.openSource(ctx, ws, p.source.ID)
	if err != nil {
		return nil, func() {}, err
	}
	sr, ok := sess.(source.SnapshotReader)
	if !ok {
		return nil, func() { _ = sess.Close() }, nil
	}
	rs, err := sr.BeginRead(ctx, "omni-data-bus preview", false)
	if err != nil {
		_ = sess.Close()
		return nil, func() {}, connectors.Classify(connectors.RoleSource, err)
	}
	return rs, func() {
		_ = rs.Close()
		_ = sess.Close()
	}, nil
}

// previewErr maps a read error: a timeout, else a classified source error.
func previewErr(ctx context.Context, err error) error {
	var pr problem.Problem
	if errors.As(err, &pr) {
		return err
	}
	if ctx.Err() != nil {
		return ErrPreviewTimeout
	}
	return connectors.Classify(connectors.RoleSource, err)
}

// fieldPathOf is a relation's field as dotted names from the root (its array's items aside).
func fieldPathOf(spec *mapping.Spec, relID string) string {
	return preview.NodePaths(spec)[relID]
}

// docPath returns where a key's document lands on a directory target ("" for other kinds).
func (s *Service) docPath(p projectCtx, loc store.Location) func(key string) string {
	if loc.Kind != store.KindDirectory {
		return func(string) string { return "" }
	}
	cfg, err := directory.ParseConfig(p.target.Config)
	if err != nil {
		return func(string) string { return "" }
	}
	return func(key string) string {
		rel, _ := cfg.DocPath(loc.Folder, key)
		return rel
	}
}

func sourceRow(t *schema.Table, row []any) map[string]any {
	out := make(map[string]any, len(t.Columns))
	for i, c := range t.Columns {
		if i < len(row) {
			out[c.Name] = encode.Value(row[i], c)
		}
	}
	return out
}

func (s *Service) fieldPreview(prog *transform.Program, spec *mapping.Spec, rows [][]any, mappingID string) (PreviewResult, error) {
	var path string
	paths := spec.DocSchema.Paths()
	for _, fm := range spec.FieldMappings {
		if fm.ID == mappingID {
			path = paths[fm.TargetFieldID]
		}
	}
	if path == "" {
		return PreviewResult{}, problem.New(http.StatusNotFound, problem.CodeNotFound, "Field mapping not found", "No active field mapping "+mappingID+".")
	}
	out := PreviewResult{Fields: []FieldPreview{}}
	for _, row := range rows {
		_, _, _, trace, rerr := prog.ApplyTrace(row, nil)
		steps := trace[path]
		fp := FieldPreview{Steps: steps}
		if len(steps) > 0 {
			fp.Before, fp.After = steps[0].Value, steps[len(steps)-1].Value
		}
		if rerr != nil && rerr.FieldPath == path {
			fp.Error = rerr
		}
		out.Fields = append(out.Fields, fp)
	}
	return out, nil
}

// checkKeys flags preview keys that already exist in the target (≤ 50 keys, OC-022 AC7).
func (s *Service) checkKeys(ctx context.Context, ws store.WorkspaceID, p projectCtx, m store.Mapping, out *PreviewResult) error {
	var keys []string
	for _, it := range out.Items {
		if it.Key != "" {
			keys = append(keys, it.Key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	loc := m.Where()
	if loc.Kind != store.KindCouchbase {
		return nil // other kinds check existing keys with their own reader (OC-041)
	}
	ks, _, err := s.openKeyspaces(ctx, ws, p.target.ID)
	if err != nil {
		return err
	}
	exists, err := ks.ExistingKeys(ctx, loc.Bucket, loc.Scope, loc.Collection, keys)
	if err != nil {
		if problem.HasCode(err, problem.CodeNotFound) {
			exists = map[string]bool{} // the collection doesn't exist yet: nothing collides
		} else {
			return err
		}
	}
	for i := range out.Items {
		if out.Items[i].Key == "" {
			continue
		}
		e := exists[out.Items[i].Key]
		out.Items[i].KeyExists = &e
		if e {
			out.Summary.Collisions++
		}
	}
	return nil
}
