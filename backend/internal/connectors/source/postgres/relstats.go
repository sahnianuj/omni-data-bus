package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"omnidatabus/internal/connectors/source"
)

// relatedFrom is a related query's FROM clause and its key columns (qualified): the table, or
// for many-to-many the junction left-joined to it (keys are the junction's), as RelatedSQL reads.
func relatedFrom(q source.RelatedQuery) (from string, keys []string, alias string) {
	keyAlias := ""
	if q.Via != nil {
		alias, keyAlias = "t", "j"
		on := make([]string, len(q.Via.ChildJoin))
		for i, j := range q.Via.ChildJoin {
			on[i] = "t." + pgx.Identifier{j.Child}.Sanitize() + " = j." + pgx.Identifier{j.Via}.Sanitize()
		}
		from = tableIdent(q.Via.Table) + " j LEFT JOIN " + tableIdent(q.Table) + " t ON " + strings.Join(on, " AND ")
	} else {
		from = tableIdent(q.Table)
	}
	for _, j := range q.Join {
		k := pgx.Identifier{j}.Sanitize()
		if keyAlias != "" {
			k = keyAlias + "." + k
		}
		keys = append(keys, k)
	}
	return from, keys, alias
}

// relatedFilters renders q's filters with parameters from $next.
func relatedFilters(q source.RelatedQuery, alias string, next int) []string {
	var out []string
	for _, f := range q.Filter {
		a, cols := alias, q.Table.Columns
		if f.Via {
			a, cols = "j", q.Via.Table.Columns
		}
		col := pgx.Identifier{f.Column}.Sanitize()
		if a != "" {
			col = a + "." + col
		}
		cond, used := filterSQL(col, columnType(cols, f.Column), f, next)
		out = append(out, cond)
		next += used
	}
	return out
}

// RelatedCountSQL counts a relation's rows per key, in the keys' order: one text array per
// join column ($1…$n, zipped with unnest), then the filters' parameters (OC-049).
func RelatedCountSQL(q source.RelatedQuery) string {
	from, keys, alias := relatedFrom(q)
	types := relatedJoinTypes(q)
	arrays := make([]string, len(keys))
	vars := make([]string, len(keys))
	where := make([]string, 0, len(keys)+len(q.Filter))
	for i := range keys {
		arrays[i] = "$" + strconv.Itoa(i+1) + "::" + types[i] + "[]"
		vars[i] = "odb_k" + strconv.Itoa(i)
		where = append(where, keys[i]+" = k."+vars[i])
	}
	where = append(where, relatedFilters(q, alias, len(keys)+1)...)
	return "SELECT (SELECT count(*) FROM " + from + " WHERE " + strings.Join(where, " AND ") + ") FROM unnest(" +
		strings.Join(arrays, ", ") + ") WITH ORDINALITY AS k(" + strings.Join(vars, ", ") + ", odb_i) ORDER BY k.odb_i"
}

// RelatedTopSQL is the key with the most rows (the preview's worst case, OC-049): the key
// columns as text, then the count; the filters' parameters start at $1.
func RelatedTopSQL(q source.RelatedQuery) string {
	from, keys, alias := relatedFrom(q)
	sel := make([]string, len(keys))
	for i, k := range keys {
		sel[i] = k + "::text"
	}
	where := append([]string{}, relatedFilters(q, alias, 1)...)
	for _, k := range keys {
		where = append(where, k+" IS NOT NULL")
	}
	return "SELECT " + strings.Join(sel, ", ") + ", count(*) AS odb_n FROM " + from + " WHERE " + strings.Join(where, " AND ") +
		" GROUP BY " + strings.Join(keys, ", ") + " ORDER BY odb_n DESC LIMIT 1"
}

// CountRelated implements source.RelatedCounter.
func (r *readSession) CountRelated(ctx context.Context, q source.RelatedQuery, keys [][]any) ([]int64, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	var out []int64
	err := r.onCoord(ctx, func(c readConn) error {
		rows, err := c.Query(ctx, RelatedCountSQL(q), relatedArgs(q, keys)...)
		for _, v := range rows {
			n, _ := v[0].(int64)
			out = append(out, n)
		}
		return err
	})
	return out, wrapRead(err)
}

// TopRelated implements source.RelatedCounter.
func (r *readSession) TopRelated(ctx context.Context, q source.RelatedQuery) ([]any, int64, bool, error) {
	var key []any
	var n int64
	err := r.onCoord(ctx, func(c readConn) error {
		rows, err := c.Query(ctx, RelatedTopSQL(q), relatedArgs(source.RelatedQuery{Filter: q.Filter}, nil)...)
		if err != nil || len(rows) == 0 {
			return err
		}
		row := rows[0]
		key = append(key, row[:len(row)-1]...)
		n, _ = row[len(row)-1].(int64)
		return nil
	})
	if err != nil {
		return nil, 0, false, wrapRead(err)
	}
	return key, n, key != nil, nil
}
