package postgres

import (
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"omnidatabus/internal/connectors/source"
)

// cursorName is the server-side cursor used for tables without a key (one per connection).
const cursorName = "odb_read"

func tableIdent(t source.TableRead) string { return pgx.Identifier{t.Schema, t.Table}.Sanitize() }

// projection selects the projected columns in table order (casts as for preview, so values
// decode like preview rows); projected reports which Columns each selected value fills.
func projection(t source.TableRead) (string, []int) {
	var cols []string
	var idx []int
	for i, c := range t.Columns {
		if i < len(t.Project) && t.Project[i] {
			cols = append(cols, selectExpr(c))
			idx = append(idx, i)
		}
	}
	return strings.Join(cols, ", "), idx
}

func keyList(t source.TableRead) string {
	ks := make([]string, len(t.Key))
	for i, k := range t.Key {
		ks[i] = pgx.Identifier{k}.Sanitize()
	}
	return strings.Join(ks, ", ")
}

func placeholders(from, n int) string {
	ps := make([]string, n)
	for i := range ps {
		ps[i] = "$" + strconv.Itoa(from+i)
	}
	return strings.Join(ps, ", ")
}

// KeysetQuery is one page of a keyset read. Args are bound in order: after (one per key
// column), then lo, then hi, then the limit.
type KeysetQuery struct {
	After, Lo, Hi bool
}

// KeysetSQL builds a keyset page (OC-024 AC1): a row-value comparison on the key so the PK
// index is used, the partition bounds on the first key column, ORDER BY the key, LIMIT.
func KeysetSQL(t source.TableRead, q KeysetQuery) string {
	sel, _ := projection(t)
	keys := keyList(t)
	var where []string
	n := 1
	if q.After {
		where = append(where, "("+keys+") > ("+placeholders(n, len(t.Key))+")")
		n += len(t.Key)
	}
	first := pgx.Identifier{t.Key[0]}.Sanitize()
	if q.Lo {
		where = append(where, first+" >= $"+strconv.Itoa(n))
		n++
	}
	if q.Hi {
		where = append(where, first+" < $"+strconv.Itoa(n))
		n++
	}
	sql := "SELECT " + sel + " FROM " + tableIdent(t)
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	return sql + " ORDER BY " + keys + " LIMIT $" + strconv.Itoa(n)
}

// CursorSQL declares the cursor for a table without a key (OC-024 AC6).
func CursorSQL(t source.TableRead) string {
	sel, _ := projection(t)
	return "DECLARE " + cursorName + " NO SCROLL CURSOR FOR SELECT " + sel + " FROM " + tableIdent(t)
}

// FetchSQL reads the next batch from the cursor (FETCH takes no parameters).
func FetchSQL(limit int) string {
	return "FETCH FORWARD " + strconv.Itoa(limit) + " FROM " + cursorName
}

// BoundsSQL returns min and max of the first key column.
func BoundsSQL(t source.TableRead) string {
	k := pgx.Identifier{t.Key[0]}.Sanitize()
	return "SELECT min(" + k + "), max(" + k + ") FROM " + tableIdent(t)
}

// CountSQL counts the table's rows.
func CountSQL(t source.TableRead) string { return "SELECT count(*) FROM " + tableIdent(t) }

// RetrySQL reads rows by key (OC-026 AC6): one array parameter per key column, zipped with
// unnest so composite keys work: WHERE (k1, k2) IN (SELECT * FROM unnest($1::t1[], $2::t2[])).
func RetrySQL(t source.TableRead) string {
	sel, _ := projection(t)
	arrays := make([]string, len(t.Key))
	for i, k := range t.Key {
		typ := "text"
		for _, c := range t.Columns {
			if c.Name == k && c.UDTName != "" {
				typ = pgx.Identifier{c.UDTName}.Sanitize()
			}
		}
		arrays[i] = "$" + strconv.Itoa(i+1) + "::" + typ + "[]"
	}
	return "SELECT " + sel + " FROM " + tableIdent(t) + " WHERE (" + keyList(t) + ") IN (SELECT * FROM unnest(" +
		strings.Join(arrays, ", ") + ")) ORDER BY " + keyList(t)
}

// relatedJoinTypes are the SQL types of the join columns' array parameters.
func relatedJoinTypes(q source.RelatedQuery) []string {
	out := make([]string, len(q.Join))
	for i, j := range q.Join {
		out[i] = "text"
		for _, c := range q.Table.Columns {
			if c.Name == j && c.UDTName != "" {
				out[i] = pgx.Identifier{c.UDTName}.Sanitize()
			}
		}
	}
	return out
}

// RelatedSQL selects a related table's rows for a batch of join keys (OC-021 AC3). One key
// column uses = ANY($1); several use (a, b) IN (SELECT * FROM unnest($1, $2)). Rows come
// ordered by the join columns, then the embed's order. A per-key limit uses ROW_NUMBER().
func RelatedSQL(q source.RelatedQuery) string {
	sel, _ := projection(q.Table)
	types := relatedJoinTypes(q)
	joins := make([]string, len(q.Join))
	for i, j := range q.Join {
		joins[i] = pgx.Identifier{j}.Sanitize()
	}
	var where string
	if len(q.Join) == 1 {
		where = joins[0] + " = ANY($1::" + types[0] + "[])"
	} else {
		arrays := make([]string, len(q.Join))
		for i := range q.Join {
			arrays[i] = "$" + strconv.Itoa(i+1) + "::" + types[i] + "[]"
		}
		where = "(" + strings.Join(joins, ", ") + ") IN (SELECT * FROM unnest(" + strings.Join(arrays, ", ") + "))"
	}
	order := append([]string(nil), joins...)
	var within []string
	for _, o := range q.OrderBy {
		col := pgx.Identifier{o.Column}.Sanitize()
		if o.Desc {
			col += " DESC"
		}
		order = append(order, col)
		within = append(within, col)
	}
	if q.PerKeyLimit <= 0 {
		return "SELECT " + sel + " FROM " + tableIdent(q.Table) + " WHERE " + where + " ORDER BY " + strings.Join(order, ", ")
	}
	over := "PARTITION BY " + strings.Join(joins, ", ")
	if len(within) > 0 {
		over += " ORDER BY " + strings.Join(within, ", ")
	}
	// The inner query keeps the projected expressions under their column names.
	outer := make([]string, 0, len(q.Table.Columns))
	for i, c := range q.Table.Columns {
		if i < len(q.Table.Project) && q.Table.Project[i] {
			outer = append(outer, pgx.Identifier{c.Name}.Sanitize())
		}
	}
	innerSel := make([]string, 0, len(outer))
	for i, c := range q.Table.Columns {
		if i < len(q.Table.Project) && q.Table.Project[i] {
			innerSel = append(innerSel, selectExpr(c)+" AS "+pgx.Identifier{c.Name}.Sanitize())
		}
	}
	return "SELECT " + strings.Join(outer, ", ") + " FROM (SELECT " + strings.Join(innerSel, ", ") + ", " + strings.Join(joinsFor(q), ", ") +
		", row_number() OVER (" + over + ") AS odb_rn FROM " + tableIdent(q.Table) + " WHERE " + where + ") odb_r WHERE odb_rn <= " +
		strconv.Itoa(q.PerKeyLimit) + " ORDER BY " + strings.Join(outerOrder(q), ", ")
}

// joinsFor are the join and order columns carried out of the inner query under private names.
func joinsFor(q source.RelatedQuery) []string {
	var out []string
	for i, j := range q.Join {
		out = append(out, pgx.Identifier{j}.Sanitize()+" AS odb_j"+strconv.Itoa(i))
	}
	for i, o := range q.OrderBy {
		out = append(out, pgx.Identifier{o.Column}.Sanitize()+" AS odb_o"+strconv.Itoa(i))
	}
	return out
}

func outerOrder(q source.RelatedQuery) []string {
	var out []string
	for i := range q.Join {
		out = append(out, "odb_j"+strconv.Itoa(i))
	}
	for i, o := range q.OrderBy {
		col := "odb_o" + strconv.Itoa(i)
		if o.Desc {
			col += " DESC"
		}
		out = append(out, col)
	}
	return append(out, "odb_rn")
}
