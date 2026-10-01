//go:build integration

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/couchbase"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// groupSchema is the customer's source (OC-063): order_detail holds one row per order line
// and there's no orders table; product_id references products. 250 orders × 1–4 lines.
func groupSchema(t *testing.T, pgCfg json.RawMessage, pgPass string) (string, *schema.Snapshot) {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("it_grp_%d", time.Now().UnixNano()%1_000_000_000)
	_, err := pgxConn(t, pgCfg, pgPass).Exec(ctx, fmt.Sprintf(`
		CREATE SCHEMA %[1]s;
		CREATE TABLE %[1]s.products (product_id int PRIMARY KEY, sku text NOT NULL, name text NOT NULL);
		CREATE TABLE %[1]s.order_detail (type text NOT NULL, order_id bigint NOT NULL, line_no int NOT NULL,
			product_id int NOT NULL REFERENCES %[1]s.products, quantity int NOT NULL, PRIMARY KEY (order_id, line_no));
		INSERT INTO %[1]s.products SELECT i, 'SKU-' || i, 'Product ' || i FROM generate_series(1, 10) i;
		INSERT INTO %[1]s.order_detail SELECT 'order_detail', o, l, 1 + (o + l) %% 10, l + 1
			FROM generate_series(1, 250) o, generate_series(1, 4) l WHERE l <= 1 + o %% 4;
		ANALYZE %[1]s.products, %[1]s.order_detail;`, name))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pgxConn(t, pgCfg, pgPass).Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE") })
	sess, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()
	snap, err := sess.Discover(ctx, source.DiscoverOptions{Schemas: []string{name}})
	require.NoError(t, err)
	return name, snap
}

// groupSpec: {type, orderId, products[]{lineNo, productId, quantity, product{sku, name}}},
// one document per order_id.
func groupSpec(sch string) mapping.Spec {
	f := func(id, name string, t docschema.FieldType, kids ...docschema.DocField) docschema.DocField {
		return docschema.DocField{ID: id, Name: name, Type: t, Children: kids}
	}
	item := f("f_item", "[]", docschema.Object,
		f("f_line", "lineNo", docschema.Integer), f("f_pid", "productId", docschema.Integer), f("f_qty", "quantity", docschema.Integer),
		f("f_prod", "product", docschema.Object, f("f_sku", "sku", docschema.String), f("f_pname", "name", docschema.String)),
	)
	products := docschema.DocField{ID: "f_products", Name: "products", Type: docschema.Array, Items: &item}
	grp := "rel_lines"
	m := func(id, field, rel, col string) mapping.FieldMapping {
		return mapping.FieldMapping{ID: id, TargetFieldID: field, Sources: []mapping.SourceRef{{Relation: rel, Column: col}}, Origin: mapping.OriginManual, State: mapping.StateActive}
	}
	spec := mapping.Spec{
		SpecVersion: 2, Mode: mapping.ModeComposed,
		Root:      &mapping.Root{Table: mapping.TableRef{Schema: sch, Name: "order_detail"}, GroupBy: []string{"order_id"}},
		DocSchema: docschema.Schema{Root: f(docschema.RootID, "$", docschema.Object, f("f_type", "type", docschema.String), f("f_oid", "orderId", docschema.Integer), products)},
		Key:       mapping.KeySpec{Template: "order::{order_id}"}, NullHandling: mapping.NullInclude,
		Relations: []mapping.Relation{
			{ID: grp, Group: true, TargetFieldID: "f_products", OrderBy: []mapping.OrderColumn{{Column: "line_no"}}},
			{ID: "rel_prod", ParentID: &grp, Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: sch, Name: "products"}, Join: []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, TargetFieldID: "f_prod"},
		},
		FieldMappings: []mapping.FieldMapping{
			m("m_type", "f_type", "", "type"), m("m_oid", "f_oid", "", "order_id"),
			m("m_line", "f_line", grp, "line_no"), m("m_pid", "f_pid", grp, "product_id"), m("m_qty", "f_qty", grp, "quantity"),
			m("m_sku", "f_sku", "rel_prod", "sku"), m("m_pname", "f_pname", "rel_prod", "name"),
		},
	}
	spec.Normalize()
	return spec
}

func TestGroup_AC1_OrderDetail(t *testing.T) {
	pgCfg, pgPass, cbCfg, cbPass, cl := e2eEnv(t)
	sch, snap := groupSchema(t, pgCfg, pgPass)
	spec := groupSpec(sch)
	issues := mapping.Validate(&spec, mapping.Context{Table: snap.Table(sch, "order_detail"), Related: func(s, n string) *schema.Table { return snap.Table(s, n) }})
	for _, is := range issues {
		require.NotEqual(t, mapping.SeverityError, is.Severity, "%s %s", is.Code, is.Message)
	}

	// Directory: a small batch, so the grouped keyset read pages many times.
	root := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "orders"), 0o750))
	conn := directory.New([]string{root})
	cfg, _ := json.Marshal(map[string]any{"path": root, "durability": "none"})
	runTableJob(t, sch, "order_detail", 100, snap, spec, store.Location{Kind: store.KindDirectory, Folder: "orders"}, pgCfg, pgPass,
		func(ctx context.Context, o target.WriteOptions) (target.DocWriter, error) {
			return conn.OpenWriter(ctx, cfg, connectors.Credentials{}, o)
		})
	files := readFolder(t, filepath.Join(root, "orders"))
	require.Len(t, files, 250, "one document per order, not per line")

	type line struct {
		LineNo, ProductID, Quantity int
		Product                     struct{ Sku, Name string }
	}
	var doc struct {
		Type     string
		OrderID  int
		Products []line
	}
	require.NoError(t, json.Unmarshal(files["order::7"], &doc))
	require.Equal(t, "order_detail", doc.Type)
	require.Equal(t, 7, doc.OrderID)
	require.Len(t, doc.Products, 4) // 1 + 7 % 4
	for i, l := range doc.Products {
		require.Equal(t, i+1, l.LineNo, "in line order")
		require.Equal(t, 1+(7+i+1)%10, l.ProductID)
		require.Equal(t, i+2, l.Quantity)
		require.Equal(t, fmt.Sprintf("SKU-%d", l.ProductID), l.Product.Sku, "the product embedded through product_id")
	}
	require.JSONEq(t, `{"type":"order_detail","orderId":4,"products":[{"lineNo":1,"productId":6,"quantity":2,"product":{"sku":"SKU-6","name":"Product 6"}}]}`, string(files["order::4"]))

	// Couchbase: the same documents (OC-036 parity).
	scope := fmt.Sprintf("it_grp_%d", time.Now().UnixNano()%1_000_000_000)
	mgr := cl.Bucket("sandbox").CollectionsV2()
	require.NoError(t, mgr.CreateScope(scope, nil))
	t.Cleanup(func() { _ = mgr.DropScope(scope, nil) })
	require.NoError(t, mgr.CreateCollection(scope, "orders", nil, nil))
	coll := cl.Bucket("sandbox").Scope(scope).Collection("orders")
	require.Eventually(t, func() bool {
		_, err := coll.Upsert("probe", map[string]int{}, nil)
		return err == nil
	}, 60*time.Second, 200*time.Millisecond)
	_, _ = coll.Remove("probe", nil)
	runTableJob(t, sch, "order_detail", 100, snap, spec, store.Location{Kind: store.KindCouchbase, Bucket: "sandbox", Scope: scope, Collection: "orders"}, pgCfg, pgPass,
		func(ctx context.Context, o target.WriteOptions) (target.DocWriter, error) {
			return couchbase.New().OpenWriter(ctx, cbCfg, connectors.Credentials{Password: cbPass}, o)
		})
	for key, want := range files {
		res, err := coll.Get(key, nil)
		require.NoError(t, err, key)
		var got json.RawMessage
		require.NoError(t, res.Content(&got))
		require.JSONEq(t, string(want), string(got), key)
	}
}
