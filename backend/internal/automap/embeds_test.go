package automap

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

func embedTables() (customer, orders *schema.Table) {
	col := func(name, udt, jt string, pk bool) schema.Column {
		return schema.Column{Name: name, UDTName: udt, JSONType: jt, IsPrimaryKey: pk, Nullable: !pk}
	}
	customer = &schema.Table{Name: "customer", PrimaryKey: []string{"customer_id"}, Columns: []schema.Column{
		col("customer_id", "int4", schema.TypeInteger, true), col("name", "text", schema.TypeString, false),
		col("total", "numeric", schema.TypeNumber, false), // same name as an orders column
	}}
	orders = &schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{
		col("order_id", "int8", schema.TypeInteger, true), col("customer_id", "int4", schema.TypeInteger, false),
		col("order_date", "date", schema.TypeString, false), col("total", "numeric", schema.TypeNumber, false),
	}}
	return customer, orders
}

func TestAddEmbed_AC2_GeneratesItemsAndAutomapsScoped(t *testing.T) {
	customer, orders := embedTables()
	spec := mapping.Spec{DocSchema: docschema.FromTable(customer, docschema.GenerateOptions{}), Key: mapping.KeySpec{Template: "customer::{customer_id}"}}
	spec.Normalize()
	spec.FieldMappings = Run(&spec, Columns(customer), Options{}).FieldMappings
	parentMappings := len(spec.FieldMappings)

	in := EmbedInput{
		Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "shop", Name: "orders"},
		Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, TargetName: "orders",
		OrderBy: []mapping.OrderColumn{{Column: "order_date", Desc: true}}, Limit: 500, Generate: true,
	}
	got, e, _, err := AddEmbed(spec, orders, in, docschema.GenerateOptions{})
	require.NoError(t, err)

	// An array-of-object field whose items match the orders columns, join column excluded.
	f := got.DocSchema.Find(e.TargetFieldID)
	require.NotNil(t, f)
	require.Equal(t, "orders", f.Name)
	require.Equal(t, docschema.Array, f.Type)
	require.Equal(t, docschema.Object, f.Items.Type)
	var names []string
	for _, c := range f.Items.Children {
		names = append(names, c.Name)
	}
	require.Equal(t, []string{"orderId", "orderDate", "total"}, names)
	require.Equal(t, got.Relations, []mapping.Relation{e})
	require.Equal(t, 500, e.Limit)

	// Auto-mapped inside the embed only, from the orders columns.
	inEmbed := mapping.RelationFields(&got)
	var embedded []mapping.FieldMapping
	for _, m := range got.FieldMappings {
		if mapping.UsesAnyRelation(m) {
			embedded = append(embedded, m)
			require.Equal(t, e.ID, inEmbed[m.TargetFieldID], "targets a field of the embed")
			require.Equal(t, e.ID, m.Sources[0].Relation)
		} else {
			require.Empty(t, inEmbed[m.TargetFieldID], "parent columns never map into the embed")
		}
	}
	require.Len(t, embedded, 3)
	require.Len(t, got.FieldMappings, parentMappings+3, "the parent's mappings are untouched")

	// The score counts the embed's columns in a separate group; customer.total still counts
	// as the parent's own column (orders.total doesn't make it mapped).
	st := mapping.ComputeStatsWith(&got, []string{"customer_id", "name", "total"}, map[string][]string{e.ID: {"order_id", "customer_id", "order_date", "total"}})
	require.Equal(t, 3, st.TotalSourceColumns)
	require.Equal(t, &mapping.EmbeddedStats{Tables: 1, TotalColumns: 3, AutoMapped: 3}, st.Embedded)

	// A parent re-run (reset) keeps the embed's mappings and still stays out of its fields.
	again := Run(&got, Columns(customer), Options{Mode: ModeReset})
	n := 0
	for _, m := range again.FieldMappings {
		if mapping.UsesAnyRelation(m) {
			n++
		} else {
			require.Empty(t, inEmbed[m.TargetFieldID])
		}
	}
	require.Equal(t, 3, n)

	// Many-to-one without generated fields: an empty object; the name is made unique.
	got2, e2, _, err := AddEmbed(got, orders, EmbedInput{Kind: mapping.EmbedManyToOne, Table: in.Table, Join: in.Join, TargetName: "orders"}, docschema.GenerateOptions{})
	require.NoError(t, err)
	f2 := got2.DocSchema.Find(e2.TargetFieldID)
	require.Equal(t, "orders2", f2.Name)
	require.Equal(t, docschema.Object, f2.Type)
	require.Empty(t, f2.Children)

	_, _, _, err = AddEmbed(got, orders, EmbedInput{Kind: mapping.EmbedManyToOne, Table: in.Table, Join: []mapping.JoinPair{{Parent: "customer_id", Child: "cust"}}}, docschema.GenerateOptions{})
	require.Error(t, err)
}

func TestAddEmbed_ManyToOne_MovesKeyIntoObject(t *testing.T) {
	customer, orders := embedTables()
	spec := mapping.Spec{DocSchema: docschema.FromTable(orders, docschema.GenerateOptions{}), Key: mapping.KeySpec{Template: "order::{order_id}"}}
	spec.Normalize()
	spec.FieldMappings = Run(&spec, Columns(orders), Options{}).FieldMappings
	rootField := func(s mapping.Spec, col string) bool {
		for _, m := range s.FieldMappings {
			if len(m.Sources) == 1 && m.Sources[0].Relation == "" && m.Sources[0].Column == col {
				return true
			}
		}
		return false
	}
	require.True(t, rootField(spec, "customer_id"), "the foreign key starts mapped at the root")

	in := EmbedInput{
		Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "customer"},
		Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, TargetName: "customer", Generate: true,
	}
	got, e, _, err := AddEmbed(spec, customer, in, docschema.GenerateOptions{})
	require.NoError(t, err)

	// The object holds the customer's primary key, mapped from the embedded row.
	f := got.DocSchema.Find(e.TargetFieldID)
	var names []string
	for _, c := range f.Children {
		names = append(names, c.Name)
	}
	require.Equal(t, []string{"customerId", "name", "total"}, names)
	var embedded []string
	for _, m := range got.FieldMappings {
		if mapping.UsesAnyRelation(m) {
			embedded = append(embedded, m.Sources[0].Column)
		}
	}
	require.ElementsMatch(t, []string{"customer_id", "name", "total"}, embedded)

	// The root's foreign key is unmapped; its field stays, and the other root mappings are kept.
	require.False(t, rootField(got, "customer_id"))
	require.True(t, rootField(got, "order_id"))
	require.True(t, rootField(got, "total"))
	var rootNames []string
	for _, c := range got.DocSchema.Root.Children {
		rootNames = append(rootNames, c.Name)
	}
	require.Contains(t, rootNames, "customerId")

	// The score doesn't count the unmapped foreign key; the embedded key counts as mapped.
	st := mapping.ComputeStatsWith(&got, []string{"order_id", "customer_id", "order_date", "total"}, map[string][]string{e.ID: {"customer_id", "name", "total"}})
	require.Equal(t, 3, st.TotalSourceColumns)
	require.Zero(t, st.UnmappedSource)
	require.Equal(t, &mapping.EmbeddedStats{Tables: 1, TotalColumns: 3, AutoMapped: 3}, st.Embedded)
}

// OC-069: in a grouped mapping the canvas nests a per-row relation under the group's rows, so
// a product lookup or object lands in each line next to its productId, not on the document.
func TestAddEmbed_UnderGroupRows_OC069(t *testing.T) {
	col := func(name, jt string, pk bool) schema.Column {
		return schema.Column{Name: name, UDTName: "int4", JSONType: jt, IsPrimaryKey: pk}
	}
	lines := &schema.Table{Name: "Order Details", PrimaryKey: []string{"order_id", "line_no"}, Columns: []schema.Column{
		col("order_id", schema.TypeInteger, true), col("line_no", schema.TypeInteger, true),
		col("product_id", schema.TypeInteger, false), col("quantity", schema.TypeInteger, false),
	}}
	product := &schema.Table{Name: "product", PrimaryKey: []string{"product_id"}, Columns: []schema.Column{
		col("product_id", schema.TypeInteger, true), {Name: "sku", UDTName: "varchar", JSONType: schema.TypeString},
	}}
	table := mapping.TableRef{Schema: "shop", Name: "Order Details"}
	item := docschema.DocField{ID: "f_line", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{{ID: "f_pid", Name: "productId", Type: docschema.Integer}}}
	spec := mapping.Spec{
		SpecVersion: 2, Mode: mapping.ModeComposed, Root: &mapping.Root{Table: table, GroupBy: []string{"order_id"}},
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_items", Name: "items", Type: docschema.Array, Items: &item},
		}}},
		Key:       mapping.KeySpec{Template: "order::{order_id}"},
		Relations: []mapping.Relation{{ID: "rel_lines", Group: true, TargetFieldID: "f_items"}},
	}
	spec.Normalize()
	related := func(_, name string) *schema.Table {
		if name == "product" {
			return product
		}
		return lines
	}
	join := []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}
	for _, lookup := range []bool{true, false} {
		in := EmbedInput{Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "product"}, Join: join, ParentID: "rel_lines", TargetName: "product", Generate: true, LookupOnly: lookup}
		got, added, _, err := AddRelation(spec, related, in, docschema.GenerateOptions{})
		require.NoError(t, err, "lookup=%v", lookup)
		require.Equal(t, "rel_lines", added[0].Parent())
		var names []string
		for _, c := range got.DocSchema.Find("f_line").Children {
			names = append(names, c.Name)
		}
		if lookup {
			require.Contains(t, names, "productSku", "lookup fields sit in each line")
		} else {
			require.Contains(t, names, "product", "the product object sits in each line")
		}
		var root []string
		for _, c := range got.DocSchema.Root.Children {
			root = append(root, c.Name)
		}
		require.Equal(t, []string{"items"}, root, "nothing lands on the document")
	}
}
