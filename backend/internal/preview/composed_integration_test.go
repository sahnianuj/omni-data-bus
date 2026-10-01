//go:build integration

package preview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/plan"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/transform"
)

// composedDB is a throwaway schema: order 1001 with 3 lines (each with its product), and
// customer 7 with 1,240 orders (the others have 1 or 2).
type composedDB struct {
	sch  string
	sess source.Session
	snap *schema.Snapshot
}

func openComposedDB(t *testing.T) *composedDB {
	t.Helper()
	host := os.Getenv("OC_IT_PG_HOST")
	if host == "" {
		t.Skip("set OC_IT_PG_HOST/PORT (the dev stack)")
	}
	port, _ := strconv.Atoi(os.Getenv("OC_IT_PG_PORT"))
	if port == 0 {
		port = 5432
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://shop_owner:shop_owner@%s:%d/shop?sslmode=disable", host, port))
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	sch := fmt.Sprintf("it_preview_%d", time.Now().UnixNano()%1_000_000_000)
	_, err = conn.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %[1]s;
		CREATE TABLE %[1]s.customers (customer_id int PRIMARY KEY, name text NOT NULL);
		CREATE TABLE %[1]s.products (product_id int PRIMARY KEY, sku text NOT NULL);
		CREATE TABLE %[1]s.orders (order_id bigint PRIMARY KEY, order_no text NOT NULL, customer_id int NOT NULL REFERENCES %[1]s.customers);
		CREATE INDEX ON %[1]s.orders (customer_id);
		CREATE TABLE %[1]s.order_items (order_id bigint NOT NULL REFERENCES %[1]s.orders, line_no int NOT NULL,
			product_id int NOT NULL REFERENCES %[1]s.products, quantity int NOT NULL, PRIMARY KEY (order_id, line_no));
		INSERT INTO %[1]s.customers SELECT g, 'customer ' || g FROM generate_series(1, 20) g;
		INSERT INTO %[1]s.products VALUES (1, 'A-1'), (2, 'B-2'), (3, 'C-3');
		-- customer 7 has 1,240 orders; every other customer 1 or 2.
		INSERT INTO %[1]s.orders SELECT 100000 + g, 'SO-' || (100000 + g), 7 FROM generate_series(1, 1240) g;
		INSERT INTO %[1]s.orders SELECT 1000 + g, 'SO-' || (1000 + g), g %% 20 + 1 FROM generate_series(1, 30) g WHERE g %% 20 + 1 <> 7;
		INSERT INTO %[1]s.order_items VALUES (1001, 1, 1, 2), (1001, 2, 2, 1), (1001, 3, 3, 5), (1002, 1, 1, 1);
		ANALYZE %[1]s.orders;`, sch))
	require.NoError(t, err)
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), fmt.Sprintf("postgres://shop_owner:shop_owner@%s:%d/shop?sslmode=disable", host, port))
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP SCHEMA "+sch+" CASCADE")
			_ = c.Close(context.Background())
		}
	})
	cfg, _ := json.Marshal(map[string]any{"host": host, "port": port, "database": "shop", "username": "shop_owner", "sslMode": "disable"})
	sess, err := postgres.New().Open(ctx, cfg, connectors.Credentials{Password: "shop_owner"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	snap, err := sess.Discover(ctx, source.DiscoverOptions{Schemas: []string{sch}})
	require.NoError(t, err)
	return &composedDB{sch: sch, sess: sess, snap: snap}
}

func (d *composedDB) ref(table string) mapping.TableRef { return mapping.TableRef{Schema: d.sch, Name: table} }

func (d *composedDB) compile(t *testing.T, spec *mapping.Spec) *plan.ExecutionPlan {
	t.Helper()
	spec.Normalize()
	pl, issues, err := plan.Compile(plan.Input{
		Spec: spec, Root: spec.Root.Table, Table: d.snap.Table(d.sch, spec.Root.Table.Name),
		Related: func(s, n string) *schema.Table { return d.snap.Table(s, n) }, Options: transform.Options{},
	})
	require.NoError(t, err)
	require.NotNil(t, pl.Program, "%v", issues)
	return pl
}

func field(id, name string, typ docschema.FieldType, kids ...docschema.DocField) docschema.DocField {
	return docschema.DocField{ID: id, Name: name, Type: typ, Children: kids}
}

func active(id, target, rel, col string) mapping.FieldMapping {
	return mapping.FieldMapping{ID: id, TargetFieldID: target, State: mapping.StateActive, Sources: []mapping.SourceRef{{Relation: rel, Column: col}}}
}

// build previews rows the way the mapping service does: related rows capped, per-node stats,
// then the documents from the trimmed rows.
func build(t *testing.T, ctx context.Context, rs source.ReadSession, pl *plan.ExecutionPlan, spec *mapping.Spec, rows [][]any) ([][]byte, []map[string]NodeStats) {
	t.Helper()
	rel, fails, totals, err := Related(ctx, rs, pl, spec, rows)
	require.NoError(t, err)
	require.Empty(t, fails)
	var docs [][]byte
	var stats []map[string]NodeStats
	for i, row := range rows {
		r := rel[i]
		stats = append(stats, Stats(spec, r, totals[i]))
		_, doc, _, rerr := pl.Program.ApplyWith(row, Trim(r), nil)
		require.Nil(t, rerr)
		docs = append(docs, append([]byte(nil), doc...))
	}
	return docs, stats
}

func TestPreview_AC1_NestedStats(t *testing.T) {
	d := openComposedDB(t)
	item := field("f_item", "[]", docschema.Object,
		field("f_line", "lineNo", docschema.Integer), field("f_qty", "quantity", docschema.Integer),
		field("f_product", "product", docschema.Object, field("f_sku", "sku", docschema.String)),
	)
	parent := "rel_items"
	spec := &mapping.Spec{
		Mode: mapping.ModeComposed, Root: &mapping.Root{Table: d.ref("orders")},
		DocSchema: docschema.Schema{Root: field(docschema.RootID, "$", docschema.Object,
			field("f_no", "orderNo", docschema.String), docschema.DocField{ID: "f_items", Name: "items", Type: docschema.Array, Items: &item},
		)},
		Key: mapping.KeySpec{Template: "order::{order_id}"},
		Relations: []mapping.Relation{
			{ID: "rel_items", Kind: mapping.EmbedOneToMany, Table: d.ref("order_items"), Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, OrderBy: []mapping.OrderColumn{{Column: "line_no"}}, TargetFieldID: "f_items"},
			{ID: "rel_prod", ParentID: &parent, Kind: mapping.EmbedManyToOne, Table: d.ref("products"), Join: []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, TargetFieldID: "f_product"},
		},
		FieldMappings: []mapping.FieldMapping{
			active("m_no", "f_no", "", "order_no"), active("m_line", "f_line", "rel_items", "line_no"),
			active("m_qty", "f_qty", "rel_items", "quantity"), active("m_sku", "f_sku", "rel_prod", "sku"),
		},
	}
	pl := d.compile(t, spec)
	ctx := context.Background()
	start := time.Now()
	rs, err := d.sess.(source.SnapshotReader).BeginRead(ctx, "odb it preview", false)
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()
	rows, err := RowsByKey(ctx, rs, spec.Root.Table, d.snap.Table(d.sch, "orders"), map[string]any{"order_id": float64(1001)})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	docs, stats := build(t, ctx, rs, pl, spec, rows)
	require.Less(t, time.Since(start), 2*time.Second)

	require.JSONEq(t, `{"orderNo":"SO-1001","items":[
		{"lineNo":1,"quantity":2,"product":{"sku":"A-1"}},
		{"lineNo":2,"quantity":1,"product":{"sku":"B-2"}},
		{"lineNo":3,"quantity":5,"product":{"sku":"C-3"}}]}`, string(docs[0]))
	require.Equal(t, 3, stats[0]["items"].Rows)
	require.Equal(t, int64(3), *stats[0]["items"].Total)
	require.False(t, stats[0]["items"].Truncated)
	require.Equal(t, 3, stats[0]["items.product"].Rows)
}

func TestPreview_AC2_WorstCase(t *testing.T) {
	d := openComposedDB(t)
	order := field("f_order", "[]", docschema.Object, field("f_ono", "orderNo", docschema.String))
	spec := &mapping.Spec{
		Mode: mapping.ModeComposed, Root: &mapping.Root{Table: d.ref("customers")},
		DocSchema: docschema.Schema{Root: field(docschema.RootID, "$", docschema.Object,
			field("f_name", "name", docschema.String), docschema.DocField{ID: "f_orders", Name: "orders", Type: docschema.Array, Items: &order},
		)},
		Key: mapping.KeySpec{Template: "customer::{customer_id}"},
		Relations: []mapping.Relation{
			{ID: "rel_orders", Kind: mapping.EmbedOneToMany, Table: d.ref("orders"), Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, OrderBy: []mapping.OrderColumn{{Column: "order_id"}}, TargetFieldID: "f_orders"},
		},
		FieldMappings: []mapping.FieldMapping{active("m_name", "f_name", "", "name"), active("m_ono", "f_ono", "rel_orders", "order_no")},
	}
	pl := d.compile(t, spec)
	ctx := context.Background()
	rs, err := d.sess.(source.SnapshotReader).BeginRead(ctx, "odb it preview", false)
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()
	rows, wc, err := WorstCaseRows(ctx, rs, pl, spec, spec.Root.Table, d.snap.Table(d.sch, "customers"), "rel_orders")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int32(7), rows[0][0], "the customer with the most orders")
	require.Equal(t, int64(1240), wc.Rows)
	require.Equal(t, "orders", wc.Path)

	docs, stats := build(t, ctx, rs, pl, spec, rows)
	st := stats[0]["orders"]
	require.Equal(t, 50, st.Rows, "showing 50 …")
	require.Equal(t, int64(1240), *st.Total, "… of 1,240")
	require.True(t, st.Truncated)
	var doc struct {
		Orders []any `json:"orders"`
	}
	require.NoError(t, json.Unmarshal(docs[0], &doc))
	require.Len(t, doc.Orders, 50)
	est := EstimateBytes(docs[0], []string{"orders"}, st.Rows, wc.Rows)
	require.Greater(t, est, int64(len(docs[0]))*20, "the full document is ~25× the preview")

	// Only an array directly under the root has a worst case.
	_, _, err = WorstCaseRows(ctx, rs, pl, spec, spec.Root.Table, d.snap.Table(d.sch, "customers"), "nope")
	require.Error(t, err)
}
