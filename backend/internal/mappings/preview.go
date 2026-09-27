package mappings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/encode"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/observability"
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
		if transformCodes[i.Code] {
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
	}
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
	if p.snap.Table(m.SourceSchema, m.SourceTable) == nil {
		return PreviewResult{}, tableNotFound(m.SourceSchema, m.SourceTable)
	}
	prog, found := transform.Compile(&spec, t, transform.Options{Related: related.Tables(&spec, p.related())})
	if prog == nil {
		return PreviewResult{}, invalid(found)
	}
	rows, err := s.sampleRows(ctx, ws, p, m.SourceSchema, t, in)
	if err != nil {
		return PreviewResult{}, err
	}
	rel, err := s.relatedRows(ctx, ws, p, &spec, t, prog, rows)
	if err != nil {
		return PreviewResult{}, err
	}
	if in.FieldMappingID != "" {
		return s.fieldPreview(prog, &spec, rows, in.FieldMappingID)
	}
	out := PreviewResult{Items: make([]PreviewItem, 0, len(rows)), Summary: &PreviewSummary{}, WriteMode: in.WriteMode}
	var total int
	buf := make([]byte, 0, 4096)
	for i, row := range rows {
		item := PreviewItem{SourceRow: sourceRow(t, row), Warnings: []transform.Warning{}}
		key, doc, warnings, trace, rerr := prog.ApplyTraceRelated(row, related.Row(rel, i), buf)
		item.Trace = trace
		if rerr != nil {
			item.Error = rerr
			out.Summary.Errors++
		} else {
			item.Key, item.Document, item.SizeBytes = key, append(json.RawMessage(nil), doc...), len(doc)
			item.Warnings = append(item.Warnings, warnings...)
			out.Summary.OK++
			total += len(doc)
			out.Summary.MaxSizeBytes = max(out.Summary.MaxSizeBytes, len(doc))
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

func (s *Service) sampleRows(ctx context.Context, ws store.WorkspaceID, p projectCtx, schemaName string, t *schema.Table, in PreviewInput) ([][]any, error) {
	sess, _, err := s.openSource(ctx, ws, p.source.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sess.Close() }()
	req := source.PreviewRequest{
		Schema: schemaName, Table: t.Name, Columns: t.Columns, OrderBy: t.PrimaryKey, Limit: in.Limit,
		Random: in.SampleMode == "random", TimeoutSec: previewTimeoutSec,
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

// relatedRows looks up the sample rows' embedded rows the way a migration does (OC-021).
func (s *Service) relatedRows(ctx context.Context, ws store.WorkspaceID, p projectCtx, spec *mapping.Spec, t *schema.Table, prog *transform.Program, rows [][]any) (map[string][][][]any, error) {
	if !prog.HasEmbeds() || len(rows) == 0 {
		return nil, nil
	}
	plans, err := related.Plans(spec, t, p.related())
	if err != nil {
		return nil, problem.New(http.StatusUnprocessableEntity, problem.CodeMappingInvalid, "Invalid mapping", err.Error())
	}
	sess, _, err := s.openSource(ctx, ws, p.source.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sess.Close() }()
	sr, ok := sess.(source.SnapshotReader)
	if !ok {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeoutSec*time.Second)
	defer cancel()
	rs, err := sr.BeginRead(ctx, "omni-data-bus preview", false)
	if err != nil {
		return nil, connectors.Classify(connectors.RoleSource, err)
	}
	defer func() { _ = rs.Close() }()
	rr, ok := rs.(source.RelatedReader)
	if !ok {
		return nil, nil
	}
	res := &related.Resolver{Plans: plans, Reader: rr, Concurrency: related.DefaultConcurrency}
	out, err := res.Resolve(ctx, rows)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrPreviewTimeout
		}
		return nil, connectors.Classify(connectors.RoleSource, err)
	}
	return out, nil
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
	ks, _, err := s.openKeyspaces(ctx, ws, p.target.ID)
	if err != nil {
		return err
	}
	exists, err := ks.ExistingKeys(ctx, m.Target.Bucket, m.Target.Scope, m.Target.Collection, keys)
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
