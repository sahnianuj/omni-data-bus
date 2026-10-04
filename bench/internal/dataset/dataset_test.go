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
	if d.CountSQL() != "SELECT (SELECT count(*) FROM bench.customers) || '/' || (SELECT count(*) FROM bench.orders)" {
		t.Fatal(d.CountSQL())
	}
	if !d.Generated("100/1000\n") || d.Generated("100/0") || d.Generated("100") {
		t.Fatal("a dataset is generated when both tables are complete")
	}
	if flat, _ := Get("smoke", 5); !flat.Generated("5") || flat.CountSQL() != "SELECT count(*) FROM bench.smoke" {
		t.Fatal("flat")
	}
}

func TestNestedSQL_OC056_LookupUnderChildren(t *testing.T) {
	d, _ := Get("nested", 100)
	sql := strings.Join(d.SQL(), "\n")
	for _, want := range []string{
		"product_id bigint NOT NULL REFERENCES bench.n_products(id)",
		"order_id bigint NOT NULL REFERENCES bench.n_orders(id)",
		", 1 + (g * 7919) % 100000\nFROM generate_series(1::bigint, 1000)",
		"generate_series(1::bigint, 100000) AS g",
		"ANALYZE bench.n_products",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %q in\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "%%") || strings.Contains(sql, "%!") {
		t.Fatalf("bad format in\n%s", sql)
	}
	drops := []int{strings.Index(sql, "DROP TABLE IF EXISTS bench.n_order_items"), strings.Index(sql, "DROP TABLE IF EXISTS bench.n_orders\n"), strings.Index(sql, "DROP TABLE IF EXISTS bench.n_products")}
	if drops[0] > drops[1] || drops[1] > drops[2] {
		t.Fatalf("drop order %v", drops)
	}
	req := d.EmbedRequest("")
	then := req["then"].([]map[string]any)[0]
	if req["limit"] != 100 || then["kind"] != "many-to-one" || req["strategy"] != nil {
		t.Fatalf("%v", req)
	}
}

func TestSkewedSQL_OC056_OnePercentHeavy(t *testing.T) {
	d, _ := Get("skewed", 1000)
	if d.ChildRows() != 10*500+990*5 || d.MaxPerParent() != 500 {
		t.Fatalf("child rows = %d", d.ChildRows())
	}
	sql := strings.Join(d.SQL(), "\n")
	if !strings.Contains(sql, "CASE WHEN p % 100 = 0 THEN 500 ELSE 5 END") || strings.Contains(sql, "%!") {
		t.Fatalf("%s", sql)
	}
	if d.EmbedRequest("sorted-merge")["strategy"] != "sorted-merge" {
		t.Fatal("strategy")
	}
	dir, _ := Get("directory", 0)
	if !dir.Directory || dir.Release != "2.0.0" || dir.Embed != nil {
		t.Fatalf("%+v", dir)
	}
}
