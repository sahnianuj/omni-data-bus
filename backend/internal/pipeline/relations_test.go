package pipeline

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/related"
	"omnidatabus/internal/schema"
)

func relCol(name, udt, jsonType string) schema.Column {
	return schema.Column{Name: name, UDTName: udt, JSONType: jsonType}
}

// shop: orders → order_items (4 per order) → products (600), orders ↔ tags via order_tags.
func shopSchema() (orders *schema.Table, rel mapping.RelatedTables) {
	orders = &schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{relCol("order_id", "int8", schema.TypeInteger), relCol("note", "text", schema.TypeString)}}
	tables := map[string]*schema.Table{
		"order_items": {Name: "order_items", Columns: []schema.Column{
			relCol("order_id", "int8", schema.TypeInteger), relCol("line_no", "int4", schema.TypeInteger), relCol("product_id", "int4", schema.TypeInteger), relCol("memo", "text", schema.TypeString),
		}},
		"products": {Name: "products", PrimaryKey: []string{"product_id"}, Columns: []schema.Column{
			relCol("product_id", "int4", schema.TypeInteger), relCol("sku", "text", schema.TypeString), relCol("brand_id", "int4", schema.TypeInteger),
		}},
		"brands":     {Name: "brands", Columns: []schema.Column{relCol("brand_id", "int4", schema.TypeInteger), relCol("brand", "text", schema.TypeString)}},
		"order_tags": {Name: "order_tags", Columns: []schema.Column{relCol("order_id", "int8", schema.TypeInteger), relCol("product_id", "int4", schema.TypeInteger), relCol("qty", "int4", schema.TypeInteger)}},
	}
	return orders, func(_, name string) *schema.Table { return tables[name] }
}

func itemsProductSpec() *mapping.Spec {
	s := &mapping.Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_id", Name: "id", Type: docschema.Integer},
			{ID: "f_items", Name: "items", Type: docschema.Array, Items: &docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
				{ID: "f_line", Name: "line", Type: docschema.Integer},
				{ID: "f_memo", Name: "memo", Type: docschema.String},
				{ID: "f_product", Name: "product", Type: docschema.Object, Children: []docschema.DocField{{ID: "f_sku", Name: "sku", Type: docschema.String}}},
			}}},
		}}},
		Key: mapping.KeySpec{Template: "order::{order_id}"},
		Relations: []mapping.Relation{
			{ID: "rel_items", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "shop", Name: "order_items"}, Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, OrderBy: []mapping.OrderColumn{{Column: "line_no"}}, TargetFieldID: "f_items"},
			{ID: "rel_product", ParentID: strPtr("rel_items"), Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "products"}, Join: []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, TargetFieldID: "f_product"},
		},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m1", TargetFieldID: "f_id", Sources: []mapping.SourceRef{{Column: "order_id"}}, State: mapping.StateActive},
			{ID: "m2", TargetFieldID: "f_line", Sources: []mapping.SourceRef{{Relation: "rel_items", Column: "line_no"}}, State: mapping.StateActive},
			{ID: "m3", TargetFieldID: "f_memo", Sources: []mapping.SourceRef{{Relation: "rel_items", Column: "memo"}}, State: mapping.StateActive},
			{ID: "m4", TargetFieldID: "f_sku", Sources: []mapping.SourceRef{{Relation: "rel_product", Column: "sku"}}, State: mapping.StateActive},
		},
	}
	s.Normalize()
	return s
}

func strPtr(s string) *string { return &s }

// shopReader answers related queries from generated data and records each query's key count.
type shopReader struct {
	mu      sync.Mutex
	queries []string // table:keys
	keys    map[string]int
	memo    string // each line's memo (to make rows big)
}

func (r *shopReader) ReadRelated(_ context.Context, q source.RelatedQuery, keys [][]any) ([][]any, error) {
	r.mu.Lock()
	r.queries = append(r.queries, q.Table.Table)
	if r.keys == nil {
		r.keys = map[string]int{}
	}
	r.keys[q.Table.Table] = len(keys)
	r.mu.Unlock()
	var out [][]any
	for _, k := range keys {
		switch q.Table.Table {
		case "order_items":
			o := k[0].(int64)
			for line := int32(1); line <= 4; line++ {
				out = append(out, []any{o, line, int32((o*4+int64(line))%600 + 1), r.memo})
			}
		case "products":
			if q.Via != nil { // many-to-many: products via order_tags, rows + [__v_order_id, __v_qty]
				o := k[0].(int64)
				out = append(out, []any{int32(o % 600), "sku", nil, o, int32(1)}, []any{int32(o%600 + 1), "sku", nil, o, int32(2)})
				continue
			}
			p := k[0].(int32)
			out = append(out, []any{p, "sku-" + strings.Repeat("x", 1), int32(p % 10)})
		case "brands":
			out = append(out, []any{k[0], "brand"})
		}
	}
	return out, nil
}

func ordersRows(n int) [][]any {
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{int64(i + 1), "n"}
	}
	return rows
}

func TestResolveRelations_AC2_QueryCountByDepth(t *testing.T) {
	orders, rel := shopSchema()
	plans, err := related.Plans(itemsProductSpec(), orders, rel)
	require.NoError(t, err)
	rd := &shopReader{}
	res := &related.Resolver{Plans: plans, Reader: rd, Concurrency: 4}
	got, fails, err := res.Resolve(context.Background(), ordersRows(1000))
	require.NoError(t, err)
	require.Empty(t, fails)
	require.Equal(t, []string{"order_items", "products"}, rd.queries, "exactly 2 lookups, depth by depth")
	require.Equal(t, 1000, rd.keys["order_items"], "1,000 order keys")
	require.Equal(t, 600, rd.keys["products"], "600 distinct product keys from 4,000 items")
	items := got[9]["rel_items"]
	require.Equal(t, 4, items.Len())
	for _, it := range items.Rows {
		require.Equal(t, 1, it.Sub["rel_product"].Len(), "each item holds its product")
		require.Equal(t, it.Values[2], it.Sub["rel_product"].Rows[0].Values[0])
	}
}

func TestResolveRelations_AC2_OneQueryM2M(t *testing.T) {
	orders, rel := shopSchema()
	spec := itemsProductSpec()
	spec.Relations = []mapping.Relation{{
		ID: "rel_p", Kind: mapping.RelationManyToMany, Table: mapping.TableRef{Schema: "shop", Name: "products"}, TargetFieldID: "f_items",
		Via: &mapping.Via{
			Table: mapping.TableRef{Schema: "shop", Name: "order_tags"}, ParentJoin: []mapping.ViaJoin{{Parent: "order_id", Via: "order_id"}},
			ChildJoin: []mapping.ViaJoin{{Via: "product_id", Child: "product_id"}}, Columns: []string{"qty"},
		},
	}}
	spec.FieldMappings = spec.FieldMappings[:1]
	plans, err := related.Plans(spec, orders, rel)
	require.NoError(t, err)
	rd := &shopReader{}
	got, _, err := (&related.Resolver{Plans: plans, Reader: rd}).Resolve(context.Background(), ordersRows(1000))
	require.NoError(t, err)
	require.Equal(t, []string{"products"}, rd.queries, "one query joins the junction and the target")
	require.Equal(t, []string{"order_id"}, plans[0].Query.Join, "keys match the junction's parent column")
	require.Equal(t, 2, got[0]["rel_p"].Len())
}

func TestResolveRelations_AC5_SplitOnMemoryCap(t *testing.T) {
	orders, rel := shopSchema()
	spec := itemsProductSpec()
	plans, err := related.Plans(spec, orders, rel)
	require.NoError(t, err)
	// 100 orders × 4 lines × ~7.5 KB ≈ 3 MB of relation rows against a 1 MB cap.
	rd := &shopReader{memo: strings.Repeat("m", 7500)}
	res := &related.Resolver{Plans: plans, Reader: rd, MemoryBytes: 1 << 20}
	got, fails, err := res.Resolve(context.Background(), ordersRows(100))
	require.NoError(t, err)
	require.Empty(t, fails, "split until each half fits")
	for i := range got {
		require.Equal(t, 4, got[i]["rel_items"].Len(), "every document is still built")
	}
	require.Greater(t, len(rd.queries), 2, "the batch was split and re-read")

	// One parent whose rows alone are 2 MB fails; the others succeed.
	big := &bigParentReader{shopReader: shopReader{memo: "m"}, big: 7}
	res = &related.Resolver{Plans: plans, Reader: big, MemoryBytes: 1 << 20}
	got, fails, err = res.Resolve(context.Background(), ordersRows(20))
	require.NoError(t, err)
	require.Len(t, fails, 1)
	require.Equal(t, related.CodeRelationMemoryExceeded, fails[6].Code, "order 7 (row 6)")
	for i := range got {
		if i != 6 {
			require.Equal(t, 4, got[i]["rel_items"].Len())
		}
	}
}

// bigParentReader gives one order 2 MB of lines.
type bigParentReader struct {
	shopReader
	big int64
}

func (r *bigParentReader) ReadRelated(ctx context.Context, q source.RelatedQuery, keys [][]any) ([][]any, error) {
	out, err := r.shopReader.ReadRelated(ctx, q, keys)
	for i := range out {
		if q.Table.Table == "order_items" && out[i][0] == r.big {
			out[i] = append([]any(nil), out[i]...)
			out[i][3] = strings.Repeat("M", 512<<10)
		}
	}
	return out, err
}
