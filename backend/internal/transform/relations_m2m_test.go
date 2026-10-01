package transform

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/related"
	"omnidatabus/internal/schema"
)

// m2mTables: orders ↔ products through order_items(order_id, product_id, line_no, quantity, unit_price).
func m2mTables() (orders, products, orderItems *schema.Table) {
	orders = &schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{col("order_id", "int8", schema.TypeInteger)}}
	products = &schema.Table{Name: "products", PrimaryKey: []string{"product_id"}, Columns: []schema.Column{
		col("product_id", "int4", schema.TypeInteger), col("sku", "text", schema.TypeString), col("name", "text", schema.TypeString),
	}}
	orderItems = &schema.Table{Name: "order_items", Columns: []schema.Column{
		col("order_item_id", "int8", schema.TypeInteger), col("order_id", "int8", schema.TypeInteger), col("product_id", "int4", schema.TypeInteger),
		col("line_no", "int4", schema.TypeInteger), col("quantity", "int4", schema.TypeInteger), col("unit_price", "float8", schema.TypeNumber),
	}}
	return orders, products, orderItems
}

func m2mSpec() *mapping.Spec {
	fld := func(id, name string, t docschema.FieldType) docschema.DocField {
		return docschema.DocField{ID: id, Name: name, Type: t}
	}
	spec := &mapping.Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			fld("f_oid", "orderId", docschema.Integer),
			{ID: "f_products", Name: "products", Type: docschema.Array, Items: &docschema.DocField{ID: "f_pitem", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
				fld("f_sku", "sku", docschema.String), fld("f_name", "name", docschema.String),
				fld("f_qty", "quantity", docschema.Integer), fld("f_price", "unitPrice", docschema.Number),
			}}},
		}}},
		Key: mapping.KeySpec{Template: "order::{order_id}"},
		Relations: []mapping.Relation{{
			ID: "rel_products", Kind: mapping.RelationManyToMany, Table: mapping.TableRef{Schema: "public", Name: "products"},
			Via: &mapping.Via{
				Table:      mapping.TableRef{Schema: "public", Name: "order_items"},
				ParentJoin: []mapping.ViaJoin{{Parent: "order_id", Via: "order_id"}},
				ChildJoin:  []mapping.ViaJoin{{Via: "product_id", Child: "product_id"}},
				Columns:    []string{"quantity", "unit_price"},
			},
			OrderBy: []mapping.OrderColumn{{Column: "line_no", From: mapping.FromVia}}, TargetFieldID: "f_products",
		}},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m_oid", TargetFieldID: "f_oid", Sources: []mapping.SourceRef{{Column: "order_id"}}, State: mapping.StateActive},
			{ID: "m_sku", TargetFieldID: "f_sku", Sources: []mapping.SourceRef{{Relation: "rel_products", Column: "sku"}}, State: mapping.StateActive},
			{ID: "m_name", TargetFieldID: "f_name", Sources: []mapping.SourceRef{{Relation: "rel_products", Column: "name"}}, State: mapping.StateActive},
			{ID: "m_qty", TargetFieldID: "f_qty", Sources: []mapping.SourceRef{{Relation: "rel_products", Column: "quantity", From: mapping.FromVia}}, State: mapping.StateActive},
			{ID: "m_price", TargetFieldID: "f_price", Sources: []mapping.SourceRef{{Relation: "rel_products", Column: "unit_price", From: mapping.FromVia}}, State: mapping.StateActive},
		},
	}
	spec.Normalize()
	return spec
}

func m2mRelated() mapping.RelatedTables {
	_, products, items := m2mTables()
	return func(_, name string) *schema.Table {
		switch name {
		case "products":
			return products
		case "order_items":
			return items
		}
		return nil
	}
}

// junctionReader answers the many-to-many query: products left-joined to order_items, rows
// as [product columns…, __v_order_id, __v_quantity, __v_unit_price, __v_line_no].
type junctionReader struct {
	rows  [][]any
	calls int
	query source.RelatedQuery
}

func (r *junctionReader) ReadRelated(_ context.Context, q source.RelatedQuery, _ [][]any) ([][]any, error) {
	r.calls++
	r.query = q
	return r.rows, nil
}

// buildM2M resolves one order's products with the real resolver, then builds its document.
func buildM2M(t *testing.T, spec *mapping.Spec, rows [][]any) (string, []Warning) {
	t.Helper()
	orders, _, _ := m2mTables()
	plans, err := related.Plans(spec, orders, m2mRelated())
	require.NoError(t, err)
	rd := &junctionReader{rows: rows}
	res := &related.Resolver{Plans: plans, Reader: rd}
	got, fails, err := res.Resolve(context.Background(), [][]any{{int64(1001)}})
	require.NoError(t, err)
	require.Empty(t, fails)
	require.Equal(t, 1, rd.calls, "one query joins the junction and the target")
	require.Equal(t, []string{"order_id", "quantity", "unit_price", "line_no"}, rd.query.Via.Columns)
	p, issues := Compile(spec, orders, Options{Related: related.Tables(spec, m2mRelated())})
	require.NotNil(t, p, "%v", issues)
	_, doc, warnings, rerr := p.ApplyWith([]any{int64(1001)}, got[0], nil)
	require.Nil(t, rerr)
	return string(doc), append([]Warning(nil), warnings...)
}

func TestApplyRelations_AC1_ManyToManyPayload(t *testing.T) {
	doc, warnings := buildM2M(t, m2mSpec(), [][]any{
		{int32(1), "A", "Pen", int64(1001), int32(2), 9.5, int32(1)},
		{int32(2), "B", "Pad", int64(1001), int32(1), 3.25, int32(2)},
	})
	require.Empty(t, warnings)
	want, err := os.ReadFile("testdata/golden/order_products_m2m.json")
	require.NoError(t, err)
	require.JSONEq(t, string(want), doc, "products with the junction's quantity and price, by line_no")
}

func TestApplyRelations_AC3_DanglingDropAndKeep(t *testing.T) {
	rows := [][]any{
		{int32(1), "A", "Pen", int64(1001), int32(2), 9.5, int32(1)},
		{nil, nil, nil, int64(1001), int32(4), 1.0, int32(2)}, // product 99 was deleted
	}
	doc, warnings := buildM2M(t, m2mSpec(), rows)
	require.JSONEq(t, `{"orderId":1001,"products":[{"sku":"A","name":"Pen","quantity":2,"unitPrice":9.5}]}`, doc)
	require.Len(t, warnings, 1)
	require.Equal(t, CodeRelationDangling, warnings[0].Code)
	require.Equal(t, "products", warnings[0].FieldPath)
	require.Contains(t, warnings[0].Message, "1 junction row(s)")

	spec := m2mSpec()
	spec.Relations[0].KeepDangling = true
	doc, warnings = buildM2M(t, spec, rows)
	require.Empty(t, warnings)
	require.JSONEq(t, `{"orderId":1001,"products":[{"sku":"A","name":"Pen","quantity":2,"unitPrice":9.5},
		{"sku":null,"name":null,"quantity":4,"unitPrice":1}]}`, doc, "kept with null product fields")
}

func TestApplyRelations_AC3_Distinct(t *testing.T) {
	rows := [][]any{
		{int32(1), "A", "Pen", int64(1001), int32(2), 9.5, int32(1)},
		{int32(1), "A", "Pen", int64(1001), int32(3), 9.0, int32(2)}, // the same product on a second line
	}
	doc, _ := buildM2M(t, m2mSpec(), rows)
	require.Equal(t, 2, countOf(doc, `"sku":"A"`), "without distinct: one item per line")
	spec := m2mSpec()
	spec.Relations[0].Distinct = true
	doc, _ = buildM2M(t, spec, rows)
	require.JSONEq(t, `{"orderId":1001,"products":[{"sku":"A","name":"Pen","quantity":2,"unitPrice":9.5}]}`, doc,
		"one item; junction columns from the first row in order")
}
