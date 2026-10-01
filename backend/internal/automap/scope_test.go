package automap

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// scopeFixture: orders with items[] bound to order_items and items[].product bound to products.
// order_items also has a sku column, so a run that leaked into items[].product would map it.
func scopeFixture() (*mapping.Spec, *schema.Table, mapping.RelatedTables) {
	c := func(name, udt, jt string) schema.Column { return schema.Column{Name: name, UDTName: udt, JSONType: jt} }
	orders := &schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{
		c("order_id", "int8", schema.TypeInteger), c("status", "text", schema.TypeString),
	}}
	tables := map[string]*schema.Table{
		"order_items": {Name: "order_items", Columns: []schema.Column{
			c("order_id", "int8", schema.TypeInteger), c("line_no", "int4", schema.TypeInteger), c("product_id", "int4", schema.TypeInteger),
			c("quantity", "int4", schema.TypeInteger), c("unit_price", "numeric", schema.TypeNumber), c("sku", "text", schema.TypeString),
		}},
		"products": {Name: "products", Columns: []schema.Column{c("product_id", "int4", schema.TypeInteger), c("sku", "text", schema.TypeString), c("name", "text", schema.TypeString)}},
	}
	f := func(id, name string, t docschema.FieldType, kids ...docschema.DocField) docschema.DocField {
		return docschema.DocField{ID: id, Name: name, Type: t, Children: kids}
	}
	item := f("f_item", "[]", docschema.Object,
		f("f_qty", "quantity", docschema.Integer), f("f_price", "unitPrice", docschema.Number), f("f_line", "lineNo", docschema.Integer),
		f("f_product", "product", docschema.Object, f("f_sku", "sku", docschema.String), f("f_pname", "name", docschema.String)),
	)
	parent := "rel_items"
	spec := &mapping.Spec{
		Mode: mapping.ModeComposed,
		DocSchema: docschema.Schema{Root: f(docschema.RootID, "$", docschema.Object,
			f("f_oid", "orderId", docschema.Integer), f("f_status", "status", docschema.String),
			docschema.DocField{ID: "f_items", Name: "items", Type: docschema.Array, Items: &item},
		)},
		Relations: []mapping.Relation{
			{ID: "rel_items", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "shop", Name: "order_items"}, Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetFieldID: "f_items"},
			{ID: "rel_prod", ParentID: &parent, Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "products"}, Join: []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, TargetFieldID: "f_product"},
		},
	}
	spec.Normalize()
	return spec, orders, func(_, name string) *schema.Table { return tables[name] }
}

func targets(ms []mapping.FieldMapping) map[string]mapping.FieldMapping {
	out := map[string]mapping.FieldMapping{}
	for _, m := range ms {
		out[m.TargetFieldID] = m
	}
	return out
}

func TestAutoMapScope_AC4_ScopedCandidates(t *testing.T) {
	spec, orders, rel := scopeFixture()
	res, err := RunScope(spec, "f_items", orders, rel)
	require.NoError(t, err)
	got := targets(res.FieldMappings)
	for field, col := range map[string]string{"f_qty": "quantity", "f_price": "unit_price", "f_line": "line_no"} {
		m, ok := got[field]
		require.True(t, ok, "%s is mapped", field)
		require.Equal(t, []mapping.SourceRef{{Relation: "rel_items", Column: col}}, m.Sources, field)
	}
	require.NotContains(t, got, "f_sku", "items[].product is a deeper bound node: not this scope")
	require.NotContains(t, got, "f_pname")
	require.NotContains(t, got, "f_oid", "root fields are the root scope's")

	// The item node itself names the same scope; the product node maps from products.
	res2, err := RunScope(spec, "f_item", orders, rel)
	require.NoError(t, err)
	require.Equal(t, len(res.FieldMappings), len(res2.FieldMappings))
	spec.FieldMappings = res.FieldMappings
	res3, err := RunScope(spec, "f_product", orders, rel)
	require.NoError(t, err)
	require.Equal(t, []mapping.SourceRef{{Relation: "rel_prod", Column: "sku"}}, targets(res3.FieldMappings)["f_sku"].Sources)

	_, err = RunScope(spec, "f_status", orders, rel)
	require.Error(t, err, "a field that isn't bound")
}

func TestAutoMapScope_AC4_KeepsManual(t *testing.T) {
	spec, orders, rel := scopeFixture()
	manual := mapping.FieldMapping{
		ID: "m_manual", TargetFieldID: "f_line", Origin: mapping.OriginManual, State: mapping.StateActive,
		Sources: []mapping.SourceRef{{Relation: "rel_items", Column: "product_id"}}, Transforms: []mapping.Step{},
	}
	spec.FieldMappings = []mapping.FieldMapping{manual}
	res, err := RunScope(spec, "f_items", orders, rel)
	require.NoError(t, err)
	require.Equal(t, manual, targets(res.FieldMappings)["f_line"], "a manual mapping is never changed")
	require.Contains(t, targets(res.FieldMappings), "f_qty")
}

func TestRunAll_RootDown(t *testing.T) {
	spec, orders, rel := scopeFixture()
	res, err := RunAll(spec, orders, rel)
	require.NoError(t, err)
	got := targets(res.FieldMappings)
	require.Equal(t, "order_id", got["f_oid"].Sources[0].Column)
	require.Equal(t, "", got["f_oid"].Sources[0].Relation)
	require.Equal(t, "rel_items", got["f_qty"].Sources[0].Relation)
	require.Equal(t, mapping.SourceRef{Relation: "rel_prod", Column: "sku"}, got["f_sku"].Sources[0])
	spec.FieldMappings = res.FieldMappings
	st := mapping.ByNode(spec)
	require.Equal(t, []mapping.NodeScore{
		{NodeID: docschema.RootID, Path: "root", Mapped: 2, Total: 2},
		{NodeID: "f_items", RelationID: "rel_items", Path: "items", Mapped: 3, Total: 3},
		{NodeID: "f_product", RelationID: "rel_prod", Path: "items.product", Mapped: 2, Total: 2},
	}, st)
}
