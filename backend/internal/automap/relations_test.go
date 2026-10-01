package automap

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

func relTables() (*schema.Table, mapping.RelatedTables) {
	c := func(n, udt, jt string, pk bool) schema.Column {
		return schema.Column{Name: n, UDTName: udt, JSONType: jt, IsPrimaryKey: pk}
	}
	orders := &schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{c("order_id", "int8", "integer", true), c("placed_at", "timestamptz", "datetime", false)}}
	tables := map[string]*schema.Table{
		"order_items": {Name: "order_items", PrimaryKey: []string{"order_item_id"}, Columns: []schema.Column{
			c("order_item_id", "int8", "integer", true), c("order_id", "int8", "integer", false), c("product_id", "int4", "integer", false),
			c("quantity", "int4", "integer", false), c("unit_price", "numeric", "number", false),
		}},
		"products": {Name: "products", PrimaryKey: []string{"product_id"}, Columns: []schema.Column{c("product_id", "int4", "integer", true), c("sku", "text", "string", false), c("name", "text", "string", false)}},
	}
	return orders, func(_, n string) *schema.Table { return tables[n] }
}

func baseOrderSpec(orders *schema.Table) mapping.Spec {
	s := mapping.Spec{DocSchema: docschema.FromTable(orders, docschema.GenerateOptions{}), Key: mapping.KeySpec{Template: "orders::{order_id}"}}
	s.Normalize()
	s.FieldMappings = Run(&s, Columns(orders), Options{}).FieldMappings
	return s
}

func fieldNames(f *docschema.DocField) []string {
	var out []string
	for _, c := range f.Children {
		out = append(out, c.Name)
	}
	return out
}

func TestAddRelationPath_AC7_GeneratesAndAutomaps(t *testing.T) {
	orders, rel := relTables()
	spec, added, _, err := AddRelation(baseOrderSpec(orders), rel, EmbedInput{
		Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "public", Name: "order_items"}, Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}},
		TargetName: "items", Generate: true,
		Then: []EmbedInput{{Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "public", Name: "products"}, Join: []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, TargetName: "product", Generate: true}},
	}, docschema.GenerateOptions{})
	require.NoError(t, err)
	require.Len(t, added, 2)
	require.Equal(t, added[0].ID, added[1].Parent(), "the second hop nests under the first")

	items := spec.DocSchema.Find(added[0].TargetFieldID)
	require.Equal(t, docschema.Array, items.Type)
	product := spec.DocSchema.Find(added[1].TargetFieldID)
	require.Contains(t, fieldNames(items.Items), "product", "items[].product")
	require.Equal(t, []string{"productId", "sku", "name"}, fieldNames(product))

	// Both levels are auto-mapped, each within its own scope.
	scopes := mapping.FieldScopes(&spec)
	byTarget := map[string]mapping.FieldMapping{}
	for _, m := range spec.FieldMappings {
		byTarget[m.TargetFieldID] = m
	}
	for _, f := range items.Items.Children {
		if f.Name == "product" || f.Name == "productId" {
			continue // the product object carries the key the foreign key held (as in 1.0)
		}
		m, ok := byTarget[f.ID]
		require.True(t, ok, "items[].%s is mapped", f.Name)
		require.Equal(t, added[0].ID, m.Sources[0].Relation)
		require.Equal(t, added[0].ID, scopes[f.ID])
	}
	for _, f := range product.Children {
		m, ok := byTarget[f.ID]
		require.True(t, ok, "items[].product.%s is mapped", f.Name)
		require.Equal(t, added[1].ID, m.Sources[0].Relation)
	}
	for _, m := range spec.FieldMappings {
		if m.Sources[0].Relation == added[0].ID {
			require.NotEqual(t, "product_id", m.Sources[0].Column, "the product's key replaces the item's foreign key")
		}
	}
	for _, is := range mapping.Validate(&spec, mapping.Context{Table: orders, Related: rel}) {
		require.NotEqual(t, mapping.SeverityError, is.Severity, "%s: %s", is.Path, is.Message)
	}
}

func TestAddRelation_AC4_FromJunctionCandidate(t *testing.T) {
	orders, rel := relTables()
	spec, added, _, err := AddRelation(baseOrderSpec(orders), rel, EmbedInput{
		Kind: mapping.RelationManyToMany, Table: mapping.TableRef{Schema: "public", Name: "products"},
		Via: &mapping.Via{
			Table: mapping.TableRef{Schema: "public", Name: "order_items"}, ParentJoin: []mapping.ViaJoin{{Parent: "order_id", Via: "order_id"}},
			ChildJoin: []mapping.ViaJoin{{Via: "product_id", Child: "product_id"}}, Columns: []string{"quantity", "unit_price"},
		},
		Generate: true,
	}, docschema.GenerateOptions{})
	require.NoError(t, err)
	e := added[0]
	require.Equal(t, mapping.RelationManyToMany, e.Kind)
	require.Empty(t, e.Join)
	f := spec.DocSchema.Find(e.TargetFieldID)
	require.Equal(t, "products", f.Name)
	require.Equal(t, []string{"productId", "sku", "name", "quantity", "unitPrice"}, fieldNames(f.Items), "product and payload fields")
	via := 0
	for _, m := range spec.FieldMappings {
		if m.Sources[0].Relation == e.ID && m.Sources[0].From == mapping.FromVia {
			via++
		}
	}
	require.Equal(t, 2, via, "quantity and unit_price are read from the junction")
	for _, is := range mapping.Validate(&spec, mapping.Context{Table: orders, Related: rel}) {
		require.NotEqual(t, mapping.SeverityError, is.Severity, "%s: %s", is.Path, is.Message)
	}
}

func TestAddRelation_LookupAndScalars(t *testing.T) {
	orders, rel := relTables()
	spec, added, _, err := AddRelation(baseOrderSpec(orders), rel, EmbedInput{
		Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "public", Name: "order_items"}, Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}},
		TargetName: "productIds", Shape: mapping.ShapeScalars, ValueColumn: "product_id",
		Aggregates: []AggregateInput{{Fn: "count", TargetName: "itemCount"}, {Fn: "sum", Column: "unit_price", TargetName: "total"}},
	}, docschema.GenerateOptions{})
	require.NoError(t, err)
	f := spec.DocSchema.Find(added[0].TargetFieldID)
	require.Equal(t, docschema.Integer, f.Items.Type, "an array of product ids")
	require.Len(t, spec.Aggregates, 2)
	require.Contains(t, fieldNames(&spec.DocSchema.Root), "itemCount")

	spec, added, _, err = AddRelation(spec, rel, EmbedInput{
		Kind: mapping.EmbedManyToOne, LookupOnly: true, ParentID: "", Table: mapping.TableRef{Schema: "public", Name: "products"},
		Join: []mapping.JoinPair{{Parent: "order_id", Child: "product_id"}}, Generate: true,
	}, docschema.GenerateOptions{})
	require.NoError(t, err)
	require.Empty(t, added[0].TargetFieldID, "values only: no object")
	require.Contains(t, fieldNames(&spec.DocSchema.Root), "productSku", "flattened next to the root fields")
}
