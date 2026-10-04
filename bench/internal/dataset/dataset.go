// Package dataset defines the benchmark datasets (OC-034) and the SQL that generates them in
// the source PostgreSQL. Generation is deterministic: every value is a function of the row
// number (generate_series), so two runs of a dataset read identical rows.
package dataset

import (
	"fmt"
	"slices"
	"strings"
)

// Schema holds every benchmark table in the source database.
const Schema = "bench"

// Dataset is one benchmark scenario.
type Dataset struct {
	Name        string
	Description string
	// Table is the migrated (root) table; Rows its row count.
	Table string
	Rows  int64
	// Columns and TextWidth shape the rows (and so the document size).
	Columns   int
	TextWidth int
	// Embed, when set, is a one-to-many child table embedded as an array (PerParent rows each).
	Embed *Embed
	// Gating marks the dataset the release NFR is measured on.
	Gating bool
	// Directory writes the documents to a directory target instead of Couchbase (2.0, OC-056).
	Directory bool
	// Release is the release whose gates the dataset belongs to ("" = 1.0, OC-034).
	Release string
}

// Embed is the child table of an embed dataset.
type Embed struct {
	Table     string
	PerParent int
	// FK is the child's column holding the parent's id ("customer_id" when empty).
	FK string
	// Skewed gives 1% of the parents (every 100th) SkewPerParent children instead of PerParent.
	SkewPerParent int
	// Lookup, when set, is a table every child row references (many-to-one), embedded one level
	// further down: parent → children → lookup (OC-056 nested relations).
	Lookup *Lookup
}

// Lookup is a parent table the embedded children reference.
type Lookup struct {
	Table string
	Rows  int64
	// FK is the child's column holding the lookup row's id.
	FK string
}

// All are the datasets of the story, plus the nightly smoke run.
var All = []Dataset{
	{Name: "narrow", Description: "10M rows, 8 columns (~200 B docs)", Table: "narrow", Rows: 10_000_000, Columns: 8, TextWidth: 16},
	{Name: "standard", Description: "5M rows, 20 columns (~1 KB docs) — the gating dataset", Table: "standard", Rows: 5_000_000, Columns: 20, TextWidth: 48, Gating: true},
	{Name: "wide", Description: "1M rows, 120 columns (~6 KB docs)", Table: "wide", Rows: 1_000_000, Columns: 120, TextWidth: 48},
	{Name: "embed", Description: "1M customers + 10M orders (10 each) embedded as an array", Table: "customers", Rows: 1_000_000, Columns: 6, TextWidth: 24,
		Embed: &Embed{Table: "orders", PerParent: 10}},
	{Name: "smoke", Description: "500k rows shaped like standard (nightly CI regression run)", Table: "smoke", Rows: 500_000, Columns: 20, TextWidth: 48},
	// 2.0 (OC-056, architecture §7; fanout is OC-046 AC5's shape, where sorted-merge should win).
	{Name: "directory", Description: "2M rows shaped like standard (~1 KB docs) to a directory target, durability: file", Table: "files",
		Rows: 2_000_000, Columns: 20, TextWidth: 48, Directory: true, Release: "2.0.0"},
	{Name: "nested", Description: "500k orders, each with 10 items, each with its product (3 tables, 2 levels)", Table: "n_orders",
		Rows: 500_000, Columns: 6, TextWidth: 24, Release: "2.0.0",
		Embed: &Embed{Table: "n_order_items", PerParent: 10, FK: "order_id", Lookup: &Lookup{Table: "n_products", Rows: 100_000, FK: "product_id"}}},
	{Name: "skewed", Description: "500k customers with all their orders: 5 each, 1% with 500 (run with --strategy batched and sorted-merge)",
		Table: "s_customers", Rows: 500_000, Columns: 6, TextWidth: 24, Release: "2.0.0",
		Embed: &Embed{Table: "s_orders", PerParent: 5, SkewPerParent: 500}},
	{Name: "fanout", Description: "100k customers with 500 orders each, 50M rows (OC-046 AC5; run with --strategy batched and sorted-merge)",
		Table: "f_customers", Rows: 100_000, Columns: 6, TextWidth: 24, Release: "2.0.0",
		Embed: &Embed{Table: "f_orders", PerParent: 500}},
}

// Get returns the named dataset, scaled to rows when rows > 0.
func Get(name string, rows int64) (Dataset, error) {
	i := slices.IndexFunc(All, func(d Dataset) bool { return d.Name == name })
	if i < 0 {
		names := make([]string, len(All))
		for j, d := range All {
			names[j] = d.Name
		}
		return Dataset{}, fmt.Errorf("unknown dataset %q (one of %s)", name, strings.Join(names, ", "))
	}
	d := All[i]
	if rows > 0 {
		d.Rows = rows
	}
	return d, nil
}

// Qualified is schema.table.
func Qualified(table string) string { return Schema + "." + table }

// ChildRows is the embed's child row count.
func (d Dataset) ChildRows() int64 {
	if d.Embed == nil {
		return 0
	}
	if d.Embed.SkewPerParent > 0 {
		heavy := d.Rows / 100
		return heavy*int64(d.Embed.SkewPerParent) + (d.Rows-heavy)*int64(d.Embed.PerParent)
	}
	return d.Rows * int64(d.Embed.PerParent)
}

// MaxPerParent is the most children one parent has (the embed's array limit).
func (d Dataset) MaxPerParent() int {
	if d.Embed == nil {
		return 0
	}
	return max(d.Embed.PerParent, d.Embed.SkewPerParent)
}

// fk is the child's column holding the parent's id.
func (e Embed) fk() string {
	if e.FK == "" {
		return "customer_id"
	}
	return e.FK
}

// EmbedRequest is the body of POST /mappings/{id}/embeds that embeds the children as an array
// (and the lookup under each child), with generated, auto-mapped fields. strategy is "" (auto),
// "batched" or "sorted-merge".
func (d Dataset) EmbedRequest(strategy string) map[string]any {
	e := d.Embed
	req := map[string]any{
		"kind": "one-to-many", "table": map[string]string{"schema": Schema, "name": e.Table},
		"join": []map[string]string{{"parent": "id", "child": e.fk()}}, "targetName": e.Table,
		"orderBy": []map[string]any{{"column": "id"}}, "limit": d.MaxPerParent() * 10, "generate": true,
	}
	if strategy != "" {
		req["strategy"] = strategy
	}
	if l := e.Lookup; l != nil {
		req["then"] = []map[string]any{{
			"kind": "many-to-one", "table": map[string]string{"schema": Schema, "name": l.Table},
			"join": []map[string]string{{"parent": l.FK, "child": "id"}}, "targetName": "product", "generate": true,
		}}
	}
	return req
}

// column is one generated payload column: its type and the SQL expression of row g.
type column struct{ name, typ, expr string }

// payload returns the columns after the id: a repeating mix of text, integer, numeric,
// timestamp and boolean, each derived from g.
func payload(n, width int) []column {
	cols := make([]column, 0, n)
	for k := 1; k <= n; k++ {
		name := fmt.Sprintf("c%03d", k)
		switch k % 5 {
		case 0, 1:
			// md5 is 32 hex chars; repeat it to reach width.
			cols = append(cols, column{name, "text",
				fmt.Sprintf("substr(repeat(md5(g::text || '%s'), %d), 1, %d)", name, (width+31)/32, width)})
		case 2:
			cols = append(cols, column{name, "bigint", fmt.Sprintf("(g * %d) %% 1000003", 7919+k)})
		case 3:
			cols = append(cols, column{name, "numeric(12,2)", fmt.Sprintf("((g * %d) %% 1000000) / 100.0", 104729+k)})
		case 4:
			if k%10 == 4 {
				cols = append(cols, column{name, "boolean", fmt.Sprintf("(g + %d) %% 2 = 0", k)})
			} else {
				cols = append(cols, column{name, "timestamptz", fmt.Sprintf("timestamptz '2020-01-01 00:00:00+00' + ((g * %d) %% 100000000) * interval '1 second'", k)})
			}
		}
	}
	return cols
}

// SQL returns the statements that (re)create the dataset's tables and fill them.
func (d Dataset) SQL() []string {
	out := []string{fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", Schema)}
	if d.Embed != nil {
		out = append(out, fmt.Sprintf("DROP TABLE IF EXISTS %s", Qualified(d.Embed.Table)))
	}
	out = append(out, fmt.Sprintf("DROP TABLE IF EXISTS %s", Qualified(d.Table)))
	if d.Embed != nil && d.Embed.Lookup != nil {
		out = append(out, fmt.Sprintf("DROP TABLE IF EXISTS %s", Qualified(d.Embed.Lookup.Table)))
	}
	if d.Embed != nil {
		return append(out, d.embedSQL()...)
	}
	cols := payload(d.Columns-1, d.TextWidth)
	defs, exprs := []string{"id bigint PRIMARY KEY"}, []string{"g"}
	for _, c := range cols {
		defs = append(defs, c.name+" "+c.typ)
		exprs = append(exprs, c.expr)
	}
	return append(out,
		fmt.Sprintf("CREATE TABLE %s (%s)", Qualified(d.Table), strings.Join(defs, ", ")),
		fmt.Sprintf("INSERT INTO %s SELECT %s FROM generate_series(1::bigint, %d) AS g", Qualified(d.Table), strings.Join(exprs, ", "), d.Rows),
		fmt.Sprintf("ANALYZE %s", Qualified(d.Table)),
	)
}

func (d Dataset) embedSQL() []string {
	e := d.Embed
	parent, child := Qualified(d.Table), Qualified(e.Table)
	out := []string{
		fmt.Sprintf(`CREATE TABLE %s (id bigint PRIMARY KEY, name text NOT NULL, email text NOT NULL, country text NOT NULL, tier text NOT NULL, created_at timestamptz NOT NULL)`, parent),
		fmt.Sprintf(`INSERT INTO %s SELECT g, 'Customer ' || g, 'customer' || g || '@example.com',
  (ARRAY['US','DE','IN','BR','JP','GB','FR','AU'])[1 + g %% 8], (ARRAY['free','pro','enterprise'])[1 + g %% 3],
  timestamptz '2020-01-01 00:00:00+00' + (g %% 100000000) * interval '1 second'
FROM generate_series(1::bigint, %d) AS g`, parent, d.Rows),
	}
	lookupCol, lookupExpr := "", ""
	if l := e.Lookup; l != nil {
		lookup := Qualified(l.Table)
		out = append(out,
			fmt.Sprintf(`CREATE TABLE %s (id bigint PRIMARY KEY, sku text NOT NULL, name text NOT NULL, brand text NOT NULL, price numeric(12,2) NOT NULL)`, lookup),
			fmt.Sprintf(`INSERT INTO %s SELECT g, 'SKU-' || lpad(g::text, 8, '0'), 'Product ' || g, 'Brand ' || (1 + g %% 1000), ((g * 7919) %% 100000) / 100.0
FROM generate_series(1::bigint, %d) AS g`, lookup, l.Rows),
		)
		lookupCol = fmt.Sprintf(", %s bigint NOT NULL REFERENCES %s(id)", l.FK, lookup)
		lookupExpr = fmt.Sprintf(", 1 + (g * 7919) %% %d", l.Rows)
	}
	fk := e.fk()
	out = append(out, fmt.Sprintf(`CREATE TABLE %s (id bigint PRIMARY KEY, %s bigint NOT NULL REFERENCES %s(id), amount numeric(12,2) NOT NULL, status text NOT NULL, ordered_at timestamptz NOT NULL, note text NOT NULL%s)`,
		child, fk, parent, lookupCol))
	cols := fmt.Sprintf(`((g * 104729) %%%% 100000) / 100.0,
  (ARRAY['placed','paid','shipped','delivered','returned'])[1 + g %%%% 5],
  timestamptz '2021-01-01 00:00:00+00' + ((g * 7919) %%%% 100000000) * interval '1 second', substr(md5(g::text), 1, %d)%s`, d.TextWidth, strings.ReplaceAll(lookupExpr, "%", "%%"))
	if e.SkewPerParent > 0 {
		// Every 100th parent has SkewPerParent children, the rest PerParent; ids follow (parent, n).
		out = append(out, fmt.Sprintf(`INSERT INTO %s SELECT g, p, `+cols+`
FROM (SELECT row_number() OVER (ORDER BY p, n) AS g, p FROM generate_series(1::bigint, %d) AS p,
  LATERAL generate_series(1, CASE WHEN p %% 100 = 0 THEN %d ELSE %d END) AS n) AS c`, child, d.Rows, e.SkewPerParent, e.PerParent))
	} else {
		// Parent ((g-1) mod parents) + 1: exactly PerParent children each.
		out = append(out, fmt.Sprintf(`INSERT INTO %s SELECT g, ((g - 1) %% %d) + 1, `+cols+`
FROM generate_series(1::bigint, %d) AS g`, child, d.Rows, d.ChildRows()))
	}
	out = append(out,
		fmt.Sprintf("CREATE INDEX ON %s (%s)", child, fk),
		fmt.Sprintf("ANALYZE %s", parent),
		fmt.Sprintf("ANALYZE %s", child),
	)
	if e.Lookup != nil {
		out = append(out, fmt.Sprintf("ANALYZE %s", Qualified(e.Lookup.Table)))
	}
	return out
}

// CountSQL counts the root table's rows, and the child table's for an embed, as "rows" or
// "rows/children" (to skip regenerating an existing, complete dataset; see Generated).
func (d Dataset) CountSQL() string {
	if d.Embed == nil {
		return "SELECT count(*) FROM " + Qualified(d.Table)
	}
	return fmt.Sprintf("SELECT (SELECT count(*) FROM %s) || '/' || (SELECT count(*) FROM %s)", Qualified(d.Table), Qualified(d.Embed.Table))
}

// Generated reports whether CountSQL's output shows the complete dataset.
func (d Dataset) Generated(out string) bool {
	want := fmt.Sprint(d.Rows)
	if d.Embed != nil {
		want += "/" + fmt.Sprint(d.ChildRows())
	}
	return strings.TrimSpace(out) == want
}
