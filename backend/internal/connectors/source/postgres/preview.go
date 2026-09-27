package postgres

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

// nativeTypes decode directly into Go values with pgx; every other type is read as text
// (enums, citext, timetz, xml, tsvector, geometry, …), money as numeric (locale-free), and
// json/jsonb as text so encode.Value keeps large numbers exact.
var nativeTypes = map[string]bool{
	"int2": true, "int4": true, "int8": true, "oid": true, "float4": true, "float8": true, "numeric": true,
	"bool": true, "text": true, "varchar": true, "bpchar": true, "name": true, "uuid": true,
	"date": true, "timestamp": true, "timestamptz": true, "time": true, "interval": true,
	"bytea": true, "inet": true, "cidr": true, "macaddr": true,
}

// selectExpr is the quoted column, cast when pgx can't decode its type natively.
func selectExpr(c schema.Column) string {
	id := pgx.Identifier{c.Name}.Sanitize()
	udt := strings.TrimPrefix(c.UDTName, "_")
	suffix := ""
	if c.IsArray {
		suffix = "[]"
	}
	switch {
	case udt == "money":
		return id + "::numeric" + suffix + " AS " + id
	case !nativeTypes[udt]:
		return id + "::text" + suffix + " AS " + id
	}
	return id
}

// PreviewSQL builds the preview statement. Every identifier is quoted (including names with
// spaces or embedded quotes); the limit is a bound parameter ($1).
func PreviewSQL(req source.PreviewRequest) string {
	cols := make([]string, len(req.Columns))
	for i, c := range req.Columns {
		cols[i] = selectExpr(c)
	}
	sql := "SELECT " + strings.Join(cols, ", ") + " FROM " + pgx.Identifier{req.Schema, req.Table}.Sanitize()
	if req.Random {
		if req.RowEstimate >= tablesampleMinRows {
			// Block sampling reads ~p% of pages; aim for 10× the rows needed, then LIMIT.
			pct := math.Min(100, math.Max(0.0001, float64(max(req.Limit, 1))*10*100/float64(req.RowEstimate)))
			return sql + " TABLESAMPLE SYSTEM (" + strconv.FormatFloat(pct, 'f', 4, 64) + ") LIMIT $1"
		}
		return sql + " ORDER BY random() LIMIT $1"
	}
	if len(req.OrderBy) > 0 {
		order := make([]string, len(req.OrderBy))
		for i, o := range req.OrderBy {
			order[i] = pgx.Identifier{o}.Sanitize()
		}
		sql += " ORDER BY " + strings.Join(order, ", ")
	}
	return sql + " LIMIT $1"
}

// tablesampleMinRows: below this, ORDER BY random() is cheap and exact.
const tablesampleMinRows = 100_000

// Preview reads rows in a read-only transaction with a statement timeout (default 5 s).
func (s *pgSession) Preview(ctx context.Context, req source.PreviewRequest) ([][]any, error) {
	req.Limit = source.ClampPreviewLimit(req.Limit)
	tx, err := s.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	timeout := req.TimeoutSec
	if timeout <= 0 {
		timeout = 5
	}
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '"+strconv.Itoa(timeout)+"s'"); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, PreviewSQL(req), req.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}
