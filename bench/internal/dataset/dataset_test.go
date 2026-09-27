package dataset

import (
	"strings"
	"testing"
)

func TestGet_ScalesAndRejectsUnknown(t *testing.T) {
	d, err := Get("standard", 0)
	if err != nil || d.Rows != 5_000_000 || !d.Gating {
		t.Fatalf("standard = %+v, %v", d, err)
	}
	d, _ = Get("standard", 1234)
	if d.Rows != 1234 {
		t.Fatalf("scaled rows = %d", d.Rows)
	}
	if All[1].Rows != 5_000_000 {
		t.Fatal("scaling must not change the catalog")
	}
	if _, err := Get("huge", 0); err == nil || !strings.Contains(err.Error(), "narrow, standard, wide, embed, smoke") {
		t.Fatalf("err = %v", err)
	}
}

func TestSQL_ColumnCountsAndDeterminism(t *testing.T) {
	for _, d := range All {
		if d.Embed != nil {
			continue
		}
		sql := d.SQL()
		create := sql[2]
		if !strings.HasPrefix(create, "CREATE TABLE bench."+d.Table+" (id bigint PRIMARY KEY") {
			t.Fatalf("%s: %s", d.Name, create)
		}
		if n := strings.Count(create, ", c") + 1; n != d.Columns { // "id" plus the c### columns
			t.Fatalf("%s: %d columns, want %d", d.Name, n, d.Columns)
		}
		for _, s := range sql {
			if strings.Contains(strings.ToLower(s), "random") {
				t.Fatalf("%s: non-deterministic SQL: %s", d.Name, s)
			}
		}
		if !strings.Contains(sql[3], "generate_series(1::bigint, ") || !strings.HasPrefix(sql[4], "ANALYZE") {
			t.Fatalf("%s: %v", d.Name, sql)
		}
	}
	// The same dataset always yields the same SQL.
	a, b := All[0].SQL(), All[0].SQL()
	if strings.Join(a, ";") != strings.Join(b, ";") {
		t.Fatal("SQL differs between calls")
	}
}

func TestPayload_TextWidthAndTypes(t *testing.T) {
	cols := payload(10, 48)
	types := map[string]int{}
	for _, c := range cols {
		types[c.typ]++
	}
	if types["text"] != 4 || types["bigint"] != 2 || types["numeric(12,2)"] != 2 || types["boolean"] != 1 || types["timestamptz"] != 1 {
		t.Fatalf("types = %v", types)
	}
	if !strings.Contains(cols[0].expr, "repeat(md5(g::text || 'c001'), 2), 1, 48)") {
		t.Fatalf("text expr = %s", cols[0].expr)
	}
}

func TestEmbedSQL_ChildRowsAndForeignKey(t *testing.T) {
	d, _ := Get("embed", 100)
	if d.ChildRows() != 1000 {
		t.Fatalf("child rows = %d", d.ChildRows())
	}
	sql := strings.Join(d.SQL(), "\n")
	for _, want := range []string{
		"DROP TABLE IF EXISTS bench.orders\n", // the child first (it references the parent)
		"REFERENCES bench.customers(id)",
		"((g - 1) % 100) + 1",
		"generate_series(1::bigint, 1000)",
		"CREATE INDEX ON bench.orders (customer_id)",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %q in\n%s", want, sql)
		}
	}
	if strings.Index(sql, "DROP TABLE IF EXISTS bench.orders") > strings.Index(sql, "DROP TABLE IF EXISTS bench.customers") {
		t.Fatal("drop the child before the parent")
	}
	if All[0].ChildRows() != 0 {
		t.Fatal("no embed, no child rows")
	}
	if d.CountSQL() != "SELECT count(*) FROM bench.customers" {
		t.Fatal(d.CountSQL())
	}
}
