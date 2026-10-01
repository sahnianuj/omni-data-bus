package postgres

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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
	if len(req.DistinctOn) > 0 {
		// A grouped driving table (OC-063): one row per group, its first in OrderBy.
		on := make([]string, len(req.DistinctOn))
		for i, c := range req.DistinctOn {
			on[i] = pgx.Identifier{c}.Sanitize()
		}
		order := append([]string(nil), on...)
		for _, o := range req.OrderBy {
			if !contains(req.DistinctOn, o) {
				order = append(order, pgx.Identifier{o}.Sanitize())
			}
		}
		groups := "SELECT DISTINCT ON (" + strings.Join(on, ", ") + ") " + strings.Join(cols, ", ") + " FROM " +
			pgx.Identifier{req.Schema, req.Table}.Sanitize() + " ORDER BY " + strings.Join(order, ", ")
		if req.Random {
			return "SELECT * FROM (" + groups + ") g ORDER BY random() LIMIT $1"
		}
		return groups + " LIMIT $1"
	}
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

// KeyCountSQL counts rows per key value over the first $1 distinct values (OC-042 cardinality
// detection): the largest group and the number of groups.
func KeyCountSQL(schemaName, table string, columns []string) string {
	cols := make([]string, len(columns))
	notNull := make([]string, len(columns))
	for i, c := range columns {
		cols[i] = pgx.Identifier{c}.Sanitize()
		notNull[i] = cols[i] + " IS NOT NULL"
	}
	return "SELECT coalesce(max(c), 0), count(*) FROM (SELECT count(*) AS c FROM " + pgx.Identifier{schemaName, table}.Sanitize() +
		" WHERE " + strings.Join(notNull, " AND ") + " GROUP BY " + strings.Join(cols, ", ") + " LIMIT $1) odb_k"
}

// MaxRowsPerKey implements source.KeyCounter in a read-only transaction with a statement
// timeout; a timeout returns source.ErrQueryTimeout.
func (s *pgSession) MaxRowsPerKey(ctx context.Context, schemaName, table string, columns []string, sample int, timeout time.Duration) (int64, int64, error) {
	tx, err := s.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	ms := max(timeout.Milliseconds(), 1)
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = "+strconv.FormatInt(ms, 10)); err != nil {
		return 0, 0, err
	}
	var maxPer, keys int64
	err = tx.QueryRow(ctx, KeyCountSQL(schemaName, table, columns), sample).Scan(&maxPer, &keys)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "57014" { // query_canceled: statement timeout
		return 0, 0, source.ErrQueryTimeout
	}
	return maxPer, keys, err
}
