package transform

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

func col(name, udt, jsonType string) schema.Column {
	return schema.Column{Name: name, UDTName: udt, JSONType: jsonType}
}

// shopTables are orders → order_items → products, orders → customers (OC-043 target shape).
func shopTables() (orders *schema.Table, related map[string]*schema.Table) {
	orders = &schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{
		col("order_id", "int8", schema.TypeInteger), col("ordered_at", "text", schema.TypeString), col("customer_id", "int4", schema.TypeInteger),
	}}
	customers := &schema.Table{Name: "customers", Columns: []schema.Column{col("customer_id", "int4", schema.TypeInteger), col("name", "text", schema.TypeString)}}
	items := &schema.Table{Name: "order_items", Columns: []schema.Column{
		col("order_item_id", "int8", schema.TypeInteger), col("order_id", "int8", schema.TypeInteger), col("line_no", "int4", schema.TypeInteger),
		col("product_id", "int4", schema.TypeInteger), col("quantity", "int4", schema.TypeInteger), col("unit_price", "float8", schema.TypeNumber),
	}}
	products := &schema.Table{Name: "products", Columns: []schema.Column{
		col("product_id", "int4", schema.TypeInteger), col("sku", "text", schema.TypeString), col("name", "text", schema.TypeString),
	}}
	return orders, map[string]*schema.Table{"rel_cust": customers, "rel_items": items, "rel_product": products}
}

func sp(s string) *string { return &s }

func shopSpec() *mapping.Spec {
	fld := func(id, name string, t docschema.FieldType) docschema.DocField {
		return docschema.DocField{ID: id, Name: name, Type: t}
	}
	spec := &mapping.Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			fld("f_oid", "orderId", docschema.Integer),
			fld("f_at", "orderedAt", docschema.String),
			fld("f_cname", "customerName", docschema.String),
			{ID: "f_items", Name: "items", Type: docschema.Array, Items: &docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
				fld("f_line", "lineNo", docschema.Integer),
				fld("f_qty", "quantity", docschema.Integer),
				fld("f_price", "unitPrice", docschema.Number),
				fld("f_item_oid", "orderId", docschema.Integer),
				{ID: "f_product", Name: "product", Type: docschema.Object, Children: []docschema.DocField{
					fld("f_sku", "sku", docschema.String), fld("f_pname", "name", docschema.String),
				}},
			}}},
		}}},
		Key: mapping.KeySpec{Template: "order::{order_id}"},
		Relations: []mapping.Relation{
			{ID: "rel_cust", Kind: mapping.EmbedManyToOne, LookupOnly: true, Table: mapping.TableRef{Schema: "public", Name: "customers"}, Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}},
			{
				ID: "rel_items", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "public", Name: "order_items"}, Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}},
				OrderBy: []mapping.OrderColumn{{Column: "line_no"}}, TargetFieldID: "f_items",
			},
			{
				ID: "rel_product", ParentID: sp("rel_items"), Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "public", Name: "products"},
				Join: []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, TargetFieldID: "f_product",
			},
		},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m_oid", TargetFieldID: "f_oid", Sources: []mapping.SourceRef{{Column: "order_id"}}, State: mapping.StateActive},
			{ID: "m_at", TargetFieldID: "f_at", Sources: []mapping.SourceRef{{Column: "ordered_at"}}, State: mapping.StateActive},
			{ID: "m_cname", TargetFieldID: "f_cname", Sources: []mapping.SourceRef{{Relation: "rel_cust", Column: "name"}}, State: mapping.StateActive},
			{ID: "m_line", TargetFieldID: "f_line", Sources: []mapping.SourceRef{{Relation: "rel_items", Column: "line_no"}}, State: mapping.StateActive},
			{ID: "m_qty", TargetFieldID: "f_qty", Sources: []mapping.SourceRef{{Relation: "rel_items", Column: "quantity"}}, State: mapping.StateActive},
			{ID: "m_price", TargetFieldID: "f_price", Sources: []mapping.SourceRef{{Relation: "rel_items", Column: "unit_price"}}, State: mapping.StateActive},
			// $root.order_id inside each item (OC-043 AC4).
			{ID: "m_item_oid", TargetFieldID: "f_item_oid", Sources: []mapping.SourceRef{{Column: "order_id"}}, State: mapping.StateActive},
			{ID: "m_sku", TargetFieldID: "f_sku", Sources: []mapping.SourceRef{{Relation: "rel_product", Column: "sku"}}, State: mapping.StateActive},
			{ID: "m_pname", TargetFieldID: "f_pname", Sources: []mapping.SourceRef{{Relation: "rel_product", Column: "name"}}, State: mapping.StateActive},
		},
	}
	spec.Normalize()
	return spec
}

// order1001 is order 1001's related rows: its customer, 3 lines, each with its product (as
// the resolver returns them: lines in line_no order, products nested under their line).
func order1001() ([]any, source.Related) {
	pen := source.Related{"rel_product": {Rows: []source.RelatedRow{{Values: []any{int32(7), "A-1", "Pen"}}}}}
	pad := source.Related{"rel_product": {Rows: []source.RelatedRow{{Values: []any{int32(9), "B-2", "Pad"}}}}}
	return []any{int64(1001), "2026-09-01T10:00:00Z", int32(42)}, source.Related{
		"rel_cust": {Rows: []source.RelatedRow{{Values: []any{int32(42), "Ada Lovelace"}}}},
		"rel_items": {Rows: []source.RelatedRow{
			{Values: []any{int64(1), int64(1001), int32(1), int32(7), int32(2), 9.5}, Sub: pen},
			{Values: []any{int64(2), int64(1001), int32(2), int32(9), int32(1), 3.25}, Sub: pad},
			{Values: []any{int64(3), int64(1001), int32(3), int32(7), int32(5), 9.5}, Sub: pen},
		}},
	}
}

func compileShop(t *testing.T, spec *mapping.Spec) *Program {
	t.Helper()
	orders, rel := shopTables()
	p, issues := Compile(spec, orders, Options{Related: rel})
	require.NotNil(t, p, "%v", issues)
	return p
}

func TestApplyRelations_AC1_OrderItemsProduct(t *testing.T) {
	p := compileShop(t, shopSpec())
	row, rel := order1001()
	key, doc, warnings, rerr := p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.Empty(t, warnings)
	require.Equal(t, "order::1001", key)
	want, err := os.ReadFile("testdata/golden/order_items_product.json")
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(doc), "3 items by line_no, each with its product object")
}

func TestApplyRelations_AC3_LookupOnly(t *testing.T) {
	p := compileShop(t, shopSpec())
	row, rel := order1001()
	_, doc, _, rerr := p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.Contains(t, string(doc), `"customerName":"Ada Lovelace"`)
	require.NotContains(t, string(doc), `"customer"`, "no nested customer object")

	// A dangling FK: null by default, omitted with nullHandling omit.
	delete(rel, "rel_cust")
	_, doc, _, _ = p.ApplyWith(row, rel, nil)
	require.Contains(t, string(doc), `"customerName":null`)
	spec := shopSpec()
	spec.NullHandling = mapping.NullOmit
	p = compileShop(t, spec)
	_, doc, _, _ = p.ApplyWith(row, rel, nil)
	require.NotContains(t, string(doc), `customerName`)
}

func TestApplyRelations_AC4_RootColumnInItem(t *testing.T) {
	p := compileShop(t, shopSpec())
	row, rel := order1001()
	_, doc, _, rerr := p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	for _, item := range []string{`"lineNo":1,`, `"lineNo":2,`, `"lineNo":3,`} {
		require.Contains(t, string(doc), item)
	}
	require.Equal(t, 4, countOf(string(doc), `"orderId":1001`), "the order's id once at the root and in each of the 3 items")

	// An ancestor's relation column works too: the item's line number inside its product.
	spec := shopSpec()
	prod := &spec.DocSchema.Root.Children[3].Items.Children[4]
	prod.Children = append(prod.Children, docschema.DocField{ID: "f_pline", Name: "line", Type: docschema.Integer})
	spec.FieldMappings = append(spec.FieldMappings, mapping.FieldMapping{
		ID: "m_pline", TargetFieldID: "f_pline", State: mapping.StateActive, Sources: []mapping.SourceRef{{Relation: "rel_items", Column: "line_no"}},
	})
	p = compileShop(t, spec)
	_, doc, _, rerr = p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.Contains(t, string(doc), `"product":{"sku":"B-2","name":"Pad","line":2}`)
}

func TestApplyRelations_ScopeErrorsAtCompile(t *testing.T) {
	spec := shopSpec()
	// A product column at the root: products are only in scope inside items[].product.
	spec.FieldMappings[1].Sources = []mapping.SourceRef{{Relation: "rel_product", Column: "sku"}}
	orders, rel := shopTables()
	p, issues := Compile(spec, orders, Options{Related: rel})
	require.Nil(t, p)
	require.Equal(t, mapping.CodeEmbedScope, issues[0].Code)
	require.Equal(t, "orderedAt", issues[0].Path)
}

func countOf(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
