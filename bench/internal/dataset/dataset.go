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
}

// Embed is the child table of an embed dataset.
type Embed struct {
	Table     string
	PerParent int
}

// All are the datasets of the story, plus the nightly smoke run.
var All = []Dataset{
	{Name: "narrow", Description: "10M rows, 8 columns (~200 B docs)", Table: "narrow", Rows: 10_000_000, Columns: 8, TextWidth: 16},
	{Name: "standard", Description: "5M rows, 20 columns (~1 KB docs) — the gating dataset", Table: "standard", Rows: 5_000_000, Columns: 20, TextWidth: 48, Gating: true},
	{Name: "wide", Description: "1M rows, 120 columns (~6 KB docs)", Table: "wide", Rows: 1_000_000, Columns: 120, TextWidth: 48},
	{Name: "embed", Description: "1M customers + 10M orders (10 each) embedded as an array", Table: "customers", Rows: 1_000_000, Columns: 6, TextWidth: 24,
		Embed: &Embed{Table: "orders", PerParent: 10}},
	{Name: "smoke", Description: "500k rows shaped like standard (nightly CI regression run)", Table: "smoke", Rows: 500_000, Columns: 20, TextWidth: 48},
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
	return d.Rows * int64(d.Embed.PerParent)
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
	parent, child := Qualified(d.Table), Qualified(d.Embed.Table)
	return []string{
		fmt.Sprintf(`CREATE TABLE %s (id bigint PRIMARY KEY, name text NOT NULL, email text NOT NULL, country text NOT NULL, tier text NOT NULL, created_at timestamptz NOT NULL)`, parent),
		fmt.Sprintf(`INSERT INTO %s SELECT g, 'Customer ' || g, 'customer' || g || '@example.com',
  (ARRAY['US','DE','IN','BR','JP','GB','FR','AU'])[1 + g %% 8], (ARRAY['free','pro','enterprise'])[1 + g %% 3],
  timestamptz '2020-01-01 00:00:00+00' + (g %% 100000000) * interval '1 second'
FROM generate_series(1::bigint, %d) AS g`, parent, d.Rows),
		fmt.Sprintf(`CREATE TABLE %s (id bigint PRIMARY KEY, customer_id bigint NOT NULL REFERENCES %s(id), amount numeric(12,2) NOT NULL, status text NOT NULL, ordered_at timestamptz NOT NULL, note text NOT NULL)`, child, parent),
		// Customer ((g-1) mod customers) + 1: exactly PerParent orders each.
		fmt.Sprintf(`INSERT INTO %s SELECT g, ((g - 1) %% %d) + 1, ((g * 104729) %% 100000) / 100.0,
  (ARRAY['placed','paid','shipped','delivered','returned'])[1 + g %% 5],
  timestamptz '2021-01-01 00:00:00+00' + ((g * 7919) %% 100000000) * interval '1 second', substr(md5(g::text), 1, %d)
FROM generate_series(1::bigint, %d) AS g`, child, d.Rows, d.TextWidth, d.ChildRows()),
		fmt.Sprintf("CREATE INDEX ON %s (customer_id)", child),
		fmt.Sprintf("ANALYZE %s", parent),
		fmt.Sprintf("ANALYZE %s", child),
	}
}

// CountSQL counts the root table's rows (to skip regenerating an existing dataset).
func (d Dataset) CountSQL() string { return "SELECT count(*) FROM " + Qualified(d.Table) }
