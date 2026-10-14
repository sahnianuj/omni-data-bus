package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

func composedShop() (*Spec, map[string]*schema.Table) {
	c := func(name, udt, jt string) schema.Column { return schema.Column{Name: name, UDTName: udt, JSONType: jt} }
	tables := map[string]*schema.Table{
		"orders": {Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{
			c("order_id", "int8", schema.TypeInteger), c("customer_id", "int4", schema.TypeInteger),
		}},
		"order_items": {Name: "order_items", PrimaryKey: []string{"order_id", "line_no"}, Columns: []schema.Column{
			c("order_id", "int8", schema.TypeInteger), c("line_no", "int4", schema.TypeInteger), c("product_id", "int4", schema.TypeInteger),
		}},
		"customers": {Name: "customers", Columns: []schema.Column{c("customer_id", "int4", schema.TypeInteger), c("name", "text", schema.TypeString)}},
		"products":  {Name: "products", Columns: []schema.Column{c("product_id", "int4", schema.TypeInteger), c("sku", "text", schema.TypeString)}},
	}
	obj := func(id, name string, kids ...docschema.DocField) docschema.DocField {
		return docschema.DocField{ID: id, Name: name, Type: docschema.Object, Children: kids}
	}
	s := &Spec{
		Mode: ModeComposed,
		Root: &Root{Table: TableRef{Schema: "public", Name: "orders"}},
		DocSchema: docschema.Schema{Root: obj(docschema.RootID, "$",
			obj("f_cust", "customer", docschema.DocField{ID: "f_cname", Name: "name", Type: docschema.String}),
			docschema.DocField{ID: "f_items", Name: "items", Type: docschema.Array, Items: &docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
				{ID: "f_line", Name: "lineNo", Type: docschema.Integer},
			}}},
		)},
		Key: KeySpec{Template: "order::{order_id}"},
		Relations: []Relation{
			{ID: "rel_cust", Kind: EmbedManyToOne, Table: TableRef{Schema: "public", Name: "customers"}, Join: []JoinPair{{Parent: "customer_id", Child: "customer_id"}}, TargetFieldID: "f_cust"},
			{ID: "rel_items", Kind: EmbedOneToMany, Table: TableRef{Schema: "public", Name: "order_items"}, Join: []JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetFieldID: "f_items"},
		},
		FieldMappings: []FieldMapping{
			{ID: "m_cname", TargetFieldID: "f_cname", State: StateActive, Sources: []SourceRef{{Relation: "rel_cust", Column: "name"}}},
			{ID: "m_line", TargetFieldID: "f_line", State: StateActive, Sources: []SourceRef{{Relation: "rel_items", Column: "line_no"}}},
		},
	}
	s.Normalize()
	return s, tables
}

func contextFor(s *Spec, tables map[string]*schema.Table) Context {
	return Context{Table: tables[s.Root.Table.Name], Related: func(_, name string) *schema.Table { return tables[name] }}
}

func withCode(issues []Issue, code string) []Issue {
	var out []Issue
	for _, i := range issues {
		if i.Code == code {
			out = append(out, i)
		}
	}
	return out
}

func TestComposed_AC4_ChangeDrivingTable(t *testing.T) {
	s, tables := composedShop()
	require.Empty(t, withCode(Validate(s, contextFor(s, tables)), CodeBindingBroken))

	require.NoError(t, SetDrivingTable(s, TableRef{Schema: "public", Name: "order_items"}))
	issues := Validate(s, contextFor(s, tables))
	broken := withCode(issues, CodeBindingBroken)
	require.Len(t, broken, 1, "only customer's join (customer_id) is missing from order_items: %v", issues)
	require.Equal(t, "customer", broken[0].Path)
	require.Equal(t, "f_cust", broken[0].FieldID)
	require.Contains(t, broken[0].Message, "join column customer_id isn't in order_items")
	require.Len(t, s.Relations, 2, "flagged, not removed")
	require.Len(t, s.FieldMappings, 2)
	require.Equal(t, StatusDraft, Status(issues))

	// A canvas mapping keeps the table picked in the Tables step.
	s.Mode = ModeCanvas
	require.ErrorIs(t, SetDrivingTable(s, TableRef{Schema: "public", Name: "orders"}), ErrNotComposed)
	require.Equal(t, TableRef{Schema: "public", Name: "x"}, DrivingTable(s, TableRef{Schema: "public", Name: "x"}))
}

func TestComposed_SyncBindings(t *testing.T) {
	s, _ := composedShop()
	require.Equal(t, &docschema.Binding{RelationID: "rel_items"}, s.DocSchema.Find("f_items").Binding)
	require.Equal(t, "rel_cust", s.DocSchema.Find("f_cust").Binding.RelationID)
	// The relation is authoritative: unbinding it clears the node's binding.
	s.Relations = s.Relations[1:]
	s.Normalize()
	require.Nil(t, s.DocSchema.Find("f_cust").Binding)
	// Canvas specs aren't annotated.
	c, _ := composedShop()
	c.Mode = ModeCanvas
	c.DocSchema.Find("f_items").Binding = nil
	c.Normalize()
	require.Nil(t, c.DocSchema.Find("f_items").Binding)
}

func TestComposed_KindMismatchAndUnboundArrays(t *testing.T) {
	s, tables := composedShop()
	// An array bound to many-to-one without the wrap adapter; an unbound empty array.
	s.DocSchema.Root.Children = append(s.DocSchema.Root.Children,
		docschema.DocField{ID: "f_buyers", Name: "buyers", Type: docschema.Array, Items: &docschema.DocField{ID: "f_buyer", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{}}},
		docschema.DocField{ID: "f_notes", Name: "notes", Type: docschema.Array, Items: &docschema.DocField{ID: "f_note", Name: "[]", Type: docschema.String}},
	)
	s.Relations = append(s.Relations, Relation{ID: "rel_buyers", Kind: EmbedManyToOne, Table: TableRef{Schema: "public", Name: "customers"}, Join: []JoinPair{{Parent: "customer_id", Child: "customer_id"}}, TargetFieldID: "f_buyers"})
	s.Normalize()
	issues := Validate(s, contextFor(s, tables))
	mismatch := withCode(issues, CodeBindingKindMismatch)
	require.Len(t, mismatch, 1)
	require.Equal(t, "buyers", mismatch[0].Path)
	require.Contains(t, mismatch[0].Message, "use the wrap adapter")
	// An optional array left unmapped is the user's choice, like any unmapped field.
	require.Empty(t, withCode(issues, CodeArrayUnboundEmpty))
	// A required one is always empty, so it's flagged.
	s.DocSchema.Find("f_notes").Required = true
	empty := withCode(Validate(s, contextFor(s, tables)), CodeArrayUnboundEmpty)
	require.Len(t, empty, 1)
	require.Equal(t, SeverityWarning, empty[0].Severity)
	require.Equal(t, "notes", empty[0].Path)
	s.DocSchema.Find("f_notes").Required = false

	// With the adapter it's a 0- or 1-item array.
	s.DocSchema.Find("f_buyers").Binding.Adapter = docschema.AdapterWrap
	require.Empty(t, withCode(Validate(s, contextFor(s, tables)), CodeBindingKindMismatch))
	// An unknown adapter is an error.
	s.DocSchema.Find("f_buyers").Binding.Adapter = "many"
	require.NotEmpty(t, withCode(Validate(s, contextFor(s, tables)), CodeBindingKindMismatch))
}

func TestExprIdentifiers(t *testing.T) {
	got := ExprIdentifiers(`concat($root.order_no, "-", items.line_no, 'x.y', value)`)
	require.Equal(t, map[string]bool{"concat": true, "$root": true, "order_no": true, "items": true, "line_no": true, "value": true}, got)
}
