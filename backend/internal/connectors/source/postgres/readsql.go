package postgres

import (
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
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

// selectHead is "SELECT" or, for a grouped read, "SELECT DISTINCT ON (keys)" (OC-063).
func selectHead(t source.TableRead) string {
	if t.Distinct {
		return "SELECT DISTINCT ON (" + keyList(t) + ")"
	}
	return "SELECT"
}

// orderList orders by the key, then, for a grouped read, by Within (which row of a group is
// its first).
func orderList(t source.TableRead) string {
	order := keyList(t)
	if t.Distinct {
		for _, w := range t.Within {
			if !contains(t.Key, w) {
				order += ", " + pgx.Identifier{w}.Sanitize()
			}
		}
	}
	return order
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
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
	sql := selectHead(t) + " " + sel + " FROM " + tableIdent(t)
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	return sql + " ORDER BY " + orderList(t) + " LIMIT $" + strconv.Itoa(n)
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

// CountSQL counts the table's rows (its groups, for a grouped read).
func CountSQL(t source.TableRead) string {
	if t.Distinct {
		return "SELECT count(*) FROM (SELECT DISTINCT " + keyList(t) + " FROM " + tableIdent(t) + ") g"
	}
	return "SELECT count(*) FROM " + tableIdent(t)
}

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
	return selectHead(t) + " " + sel + " FROM " + tableIdent(t) + " WHERE (" + keyList(t) + ") IN (SELECT * FROM unnest(" +
		strings.Join(arrays, ", ") + ")) ORDER BY " + orderList(t)
}

// relatedJoinTypes are the SQL types of the join columns' array parameters (the junction's
// columns for many-to-many).
func relatedJoinTypes(q source.RelatedQuery) []string {
	cols := q.Table.Columns
	if q.Via != nil {
		cols = q.Via.Table.Columns
	}
	out := make([]string, len(q.Join))
	for i, j := range q.Join {
		out[i] = columnType(cols, j)
	}
	return out
}

// columnType is a column's quoted SQL type ("text" when unknown).
func columnType(cols []schema.Column, name string) string {
	for _, c := range cols {
		if c.Name == name && c.UDTName != "" {
			return pgx.Identifier{c.UDTName}.Sanitize()
		}
	}
	return "text"
}

func findColumn(cols []schema.Column, name string) schema.Column {
	for _, c := range cols {
		if c.Name == name {
			return c
		}
	}
	return schema.Column{Name: name}
}

// textFamily: text-like types compare byte-wise under COLLATE "C" (sorted-merge, OC-046).
func textFamily(c schema.Column) bool {
	switch c.UDTName {
	case "text", "varchar", "bpchar", "citext", "name":
		return true
	}
	return false
}

// colExpr reads a column (qualified by alias when set) with the preview's casts, without an
// output name.
func colExpr(alias string, c schema.Column) string {
	id := pgx.Identifier{c.Name}.Sanitize()
	if alias != "" {
		id = alias + "." + id
	}
	udt := strings.TrimPrefix(c.UDTName, "_")
	suffix := ""
	if c.IsArray {
		suffix = "[]"
	}
	switch {
	case udt == "money":
		return id + "::numeric" + suffix
	case !nativeTypes[udt]:
		return id + "::text" + suffix
	}
	return id
}

// relatedItem is one selected value: its expression and output name.
type relatedItem struct{ expr, name string }

func (i relatedItem) sel() string {
	if i.expr == i.name {
		return i.expr
	}
	return i.expr + " AS " + i.name
}

// relatedParts are the pieces of a related query.
type relatedParts struct {
	items  []relatedItem // the row's values, in row order
	from   string
	where  []string
	joins  []string // the key columns, for ordering and partitioning
	orders []string // the relation's order expressions (with DESC)
}

// RelatedSQL selects a related table's rows for a batch of join keys (OC-021 AC3). One key
// column uses = ANY($1); several use (a, b) IN (SELECT * FROM unnest($1, $2)); a Range reads
// the key column between $1 and $2 in byte order (OC-046). A many-to-many relation reads the
// junction left-joined to the table, with junction columns aliased __v_* after the table's
// (OC-044). Filters follow as further bind parameters (OC-045). Rows come ordered by the join
// columns, then the relation's order. A per-key limit uses ROW_NUMBER().
func RelatedSQL(q source.RelatedQuery) string {
	p := relatedParts{}
	alias := ""
	keyAlias := ""
	keyCols := q.Table.Columns
	if q.Via != nil {
		alias, keyAlias, keyCols = "t", "j", q.Via.Table.Columns
		p.from = tableIdent(q.Via.Table) + " j LEFT JOIN " + tableIdent(q.Table) + " t ON "
		on := make([]string, len(q.Via.ChildJoin))
		for i, j := range q.Via.ChildJoin {
			on[i] = "t." + pgx.Identifier{j.Child}.Sanitize() + " = j." + pgx.Identifier{j.Via}.Sanitize()
		}
		p.from += strings.Join(on, " AND ")
	} else {
		p.from = tableIdent(q.Table)
	}
	for i, c := range q.Table.Columns {
		if i < len(q.Table.Project) && q.Table.Project[i] {
			p.items = append(p.items, relatedItem{expr: colExpr(alias, c), name: pgx.Identifier{c.Name}.Sanitize()})
		}
	}
	if q.Via != nil {
		for _, name := range q.Via.Columns {
			p.items = append(p.items, relatedItem{expr: colExpr("j", findColumn(q.Via.Table.Columns, name)), name: pgx.Identifier{"__v_" + name}.Sanitize()})
		}
	}
	qual := func(a, col string) string {
		id := pgx.Identifier{col}.Sanitize()
		if a != "" {
			return a + "." + id
		}
		return id
	}
	types := relatedJoinTypes(q)
	var next int // the next bind parameter
	for _, j := range q.Join {
		p.joins = append(p.joins, qual(keyAlias, j))
	}
	switch {
	case q.Range != nil:
		k := p.joins[0]
		typ := types[0]
		if textFamily(findColumn(keyCols, q.Join[0])) {
			k += ` COLLATE "C"`
			p.joins[0] = k
		}
		p.where = append(p.where, k+" >= $1::"+typ, k+" <= $2::"+typ)
		next = 3
	case len(q.Join) == 1:
		p.where = append(p.where, p.joins[0]+" = ANY($1::"+types[0]+"[])")
		next = 2
	default:
		arrays := make([]string, len(q.Join))
		for i := range q.Join {
			arrays[i] = "$" + strconv.Itoa(i+1) + "::" + types[i] + "[]"
		}
		p.where = append(p.where, "("+strings.Join(p.joins, ", ")+") IN (SELECT * FROM unnest("+strings.Join(arrays, ", ")+"))")
		next = len(q.Join) + 1
	}
	for _, f := range q.Filter {
		a, cols := alias, q.Table.Columns
		if f.Via {
			a, cols = "j", q.Via.Table.Columns
		}
		cond, used := filterSQL(qual(a, f.Column), columnType(cols, f.Column), f, next)
		p.where = append(p.where, cond)
		next += used
	}
	for _, o := range q.OrderBy {
		a := alias
		if o.Via {
			a = "j"
		}
		col := qual(a, o.Column)
		if o.Desc {
			col += " DESC"
		}
		p.orders = append(p.orders, col)
	}
	where := strings.Join(p.where, " AND ")
	if q.PerKeyLimit <= 0 {
		sel := make([]string, len(p.items))
		for i, it := range p.items {
			sel[i] = it.sel()
		}
		return "SELECT " + strings.Join(sel, ", ") + " FROM " + p.from + " WHERE " + where + " ORDER BY " + strings.Join(append(append([]string(nil), p.joins...), p.orders...), ", ")
	}
	over := "PARTITION BY " + strings.Join(p.joins, ", ")
	if len(p.orders) > 0 {
		over += " ORDER BY " + strings.Join(p.orders, ", ")
	}
	// The inner query keeps the projected expressions under their column names, and carries
	// the join and order columns out under private names.
	outer := make([]string, len(p.items))
	inner := make([]string, 0, len(p.items)+len(p.joins)+len(p.orders))
	for i, it := range p.items {
		outer[i] = it.name
		inner = append(inner, it.expr+" AS "+it.name)
	}
	var outerOrder []string
	for i, j := range p.joins {
		inner = append(inner, strings.TrimSuffix(j, ` COLLATE "C"`)+" AS odb_j"+strconv.Itoa(i))
		outerOrder = append(outerOrder, "odb_j"+strconv.Itoa(i))
	}
	for i, o := range p.orders {
		inner = append(inner, strings.TrimSuffix(o, " DESC")+" AS odb_o"+strconv.Itoa(i))
		col := "odb_o" + strconv.Itoa(i)
		if strings.HasSuffix(o, " DESC") {
			col += " DESC"
		}
		outerOrder = append(outerOrder, col)
	}
	outerOrder = append(outerOrder, "odb_rn")
	return "SELECT " + strings.Join(outer, ", ") + " FROM (SELECT " + strings.Join(inner, ", ") +
		", row_number() OVER (" + over + ") AS odb_rn FROM " + p.from + " WHERE " + where + ") odb_r WHERE odb_rn <= " +
		strconv.Itoa(q.PerKeyLimit) + " ORDER BY " + strings.Join(outerOrder, ", ")
}

// filterSQL renders one filter condition on col with its values as parameters from $next;
// used is how many parameters it takes (OC-045: values are never part of the SQL text).
func filterSQL(col, typ string, f source.FilterCond, next int) (cond string, used int) {
	param := "$" + strconv.Itoa(next) + "::" + typ
	switch f.Op {
	case "isNull":
		return col + " IS NULL", 0
	case "notNull":
		return col + " IS NOT NULL", 0
	case "in":
		return col + " = ANY(" + param + "[])", 1
	case "notIn":
		return col + " <> ALL(" + param + "[])", 1
	case "like":
		return col + " LIKE $" + strconv.Itoa(next) + "::text", 1
	}
	op := map[string]string{"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}[f.Op]
	if op == "" {
		op = "=" // validated upstream; never reached
	}
	return col + " " + op + " " + param, 1
}
