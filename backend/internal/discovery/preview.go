package discovery

import (
	"context"
	"errors"
	"net/http"
	"unicode/utf8"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/encode"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// Display limits: longer values are flagged truncated (the UI shows an ellipsis and an Expand
// popover); values are never sent beyond the hard caps.
const (
	displayStringLen = 1024
	displayBytesLen  = 256
	maxStringLen     = 64 << 10
	maxBytesLen      = 48 << 10 // 64 KB once base64-encoded
)

// Cell is one preview value: v is the pipeline encoding, t its jsonType.
type Cell struct {
	V         any    `json:"v"`
	T         string `json:"t"`
	Truncated bool   `json:"truncated,omitempty"`
}

// PreviewColumn describes a preview column.
type PreviewColumn struct {
	Name     string `json:"name"`
	DataType string `json:"dataType"`
	JSONType string `json:"jsonType"`
}

// PreviewResult is the first rows of a table, encoded like a migration would encode them.
type PreviewResult struct {
	Schema     string          `json:"schema"`
	Table      string          `json:"table"`
	SnapshotID string          `json:"snapshotId"`
	Columns    []PreviewColumn `json:"columns"`
	OrderBy    []string        `json:"orderBy"`
	Limit      int             `json:"limit"`
	Rows       [][]Cell        `json:"rows"`
}

// Preview reads up to limit rows (default 50, max 200) of a table from the latest snapshot,
// ordered by orderBy (a column name) or by default by the primary key.
func (s *Service) Preview(ctx context.Context, ws store.WorkspaceID, connectionID, schemaName, tableName, orderBy string, limit int) (PreviewResult, error) {
	ctx, span := observability.Tracer().Start(ctx, "discovery.preview")
	defer span.End()
	rec, snap, err := s.Latest(ctx, ws, connectionID)
	if err != nil {
		return PreviewResult{}, err
	}
	t := snap.Table(schemaName, tableName)
	if t == nil {
		return PreviewResult{}, tableNotFound(schemaName, tableName)
	}
	order := t.PrimaryKey
	if orderBy != "" {
		if t.Column(orderBy) == nil {
			p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid request", "orderBy must be a column of "+schemaName+"."+tableName+".")
			p.FieldErrors = []problem.FieldError{{Field: "orderBy", Message: "unknown column"}}
			return PreviewResult{}, p
		}
		order = []string{orderBy}
	}
	if !t.Readable {
		return PreviewResult{}, &connectors.ConnError{
			Role: connectors.RoleSource, Kind: connectors.KindPermission,
			Cause: errors.New("the repository's user has no SELECT privilege on " + schemaName + "." + tableName),
		}
	}
	req := source.PreviewRequest{Schema: schemaName, Table: tableName, Columns: t.Columns, OrderBy: order, Limit: source.ClampPreviewLimit(limit)}
	sess, _, err := s.open(ctx, ws, connectionID)
	if err != nil {
		return PreviewResult{}, err
	}
	defer func() { _ = sess.Close() }()
	rows, err := sess.Preview(ctx, req)
	if err != nil {
		return PreviewResult{}, connectors.Classify(connectors.RoleSource, err)
	}
	out := PreviewResult{
		Schema: schemaName, Table: tableName, SnapshotID: rec.ID, OrderBy: order, Limit: req.Limit,
		Columns: make([]PreviewColumn, len(t.Columns)), Rows: make([][]Cell, 0, len(rows)),
	}
	if out.OrderBy == nil {
		out.OrderBy = []string{}
	}
	for i, c := range t.Columns {
		out.Columns[i] = PreviewColumn{Name: c.Name, DataType: c.DataType, JSONType: c.JSONType}
	}
	for _, row := range rows {
		cells := make([]Cell, len(t.Columns))
		for i, c := range t.Columns {
			if i < len(row) {
				cells[i] = cell(row[i], c)
			}
		}
		out.Rows = append(out.Rows, cells)
	}
	return out, nil
}

func tableNotFound(schemaName, tableName string) problem.Problem {
	return problem.New(http.StatusNotFound, problem.CodeNotFound, "Table not found",
		"The latest snapshot has no table "+schemaName+"."+tableName+". Refresh the schema if it was just created.")
}

// cell encodes a value and applies the display limits.
func cell(v any, c schema.Column) Cell {
	out := Cell{T: c.JSONType}
	if b, ok := v.([]byte); ok && c.UDTName == "bytea" {
		out.Truncated = len(b) > displayBytesLen
		if len(b) > maxBytesLen {
			v = b[:maxBytesLen]
		}
	}
	out.V = encode.Value(v, c)
	if s, ok := out.V.(string); ok && c.UDTName != "bytea" {
		out.Truncated = utf8.RuneCountInString(s) > displayStringLen
		if len(s) > maxStringLen {
			cut := maxStringLen
			for cut > 0 && !utf8.RuneStart(s[cut]) {
				cut--
			}
			out.V = s[:cut]
		}
	}
	return out
}
