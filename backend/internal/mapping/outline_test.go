package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
)

// OC-158 AC2: the outline reads like the mapping, in document order.
func TestOutline_OC158_AC2(t *testing.T) {
	f := func(id, name string, typ docschema.FieldType, kids ...docschema.DocField) docschema.DocField {
		return docschema.DocField{ID: id, Name: name, Type: typ, Children: kids}
	}
	items := f("f_items", "items", docschema.Array)
	items.Items = &docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{f("f_qty", "quantity", docschema.Integer)}}
	root := docschema.NewRoot()
	root.Children = []docschema.DocField{
		f("f_id", "orderId", docschema.Integer),
		f("f_cust", "customer", docschema.Object, f("f_name", "name", docschema.String)),
		items,
		f("f_src", "source", docschema.String),
		f("f_note", "note", docschema.String), // unmapped: not listed
		f("f_label", "label", docschema.String),
	}
	s := &Spec{
		SpecVersion: SpecVersion, Mode: ModeCanvas, DocSchema: docschema.Schema{Root: root},
		Key:       KeySpec{Template: "order::{order_id}"},
		TypeField: TypeField{Enabled: true, Name: "type", Value: "order"},
		Relations: []Relation{
			{ID: "r_c", Kind: EmbedManyToOne, Table: TableRef{Schema: "shop", Name: "customers"}, Join: []JoinPair{{Parent: "customer_id", Child: "customer_id"}}, TargetFieldID: "f_cust"},
			{ID: "r_i", Kind: EmbedOneToMany, Table: TableRef{Schema: "shop", Name: "order_items"}, Join: []JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetFieldID: "f_items"},
		},
		FieldMappings: []FieldMapping{
			{ID: "m1", TargetFieldID: "f_id", Sources: []SourceRef{{Column: "order_id"}}, State: StateActive},
			{ID: "m2", TargetFieldID: "f_name", Sources: []SourceRef{{Relation: "r_c", Column: "name"}}, State: StateActive},
			{ID: "m3", TargetFieldID: "f_qty", Sources: []SourceRef{{Relation: "r_i", Column: "quantity"}}, Transforms: []Step{{Op: "toInteger"}}, State: StateActive},
			{ID: "m4", TargetFieldID: "f_src", Transforms: []Step{{Op: "constant", Args: map[string]any{"value": "pos"}}}, State: StateActive},
			{ID: "m5", TargetFieldID: "f_note", Sources: []SourceRef{{Column: "notes"}}, State: StatePending},
			{ID: "m6", TargetFieldID: "f_label", Transforms: []Step{{Op: "upper"}}, State: StateActive, Computed: &Computed{Lang: ComputedTemplate, Expr: "ORD-${self.orderId}"}},
		},
	}
	require.Equal(t, []string{
		"key = order::{order_id}",
		"orderId ← orders.order_id",
		"customer ← customers (object, customer_id = customer_id)",
		"customer.name ← customers.name",
		"items[] ← order_items (array, order_id = order_id, at most 1000)",
		"items[].quantity ← order_items.quantity | toInteger",
		`source = "pos"`,
		"label = `ORD-${self.orderId}` | upper",
		`type = "order"`,
	}, Outline(s, TableRef{Schema: "shop", Name: "orders"}))
}
