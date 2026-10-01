package transform

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
)

func TestApplyRelations_AC1_ScalarShape(t *testing.T) {
	spec := shopSpec()
	spec.DocSchema.Root.Children = append(spec.DocSchema.Root.Children, docschema.DocField{
		ID: "f_pids", Name: "productIds", Type: docschema.Array, Items: &docschema.DocField{ID: "f_pid", Name: "[]", Type: docschema.Integer},
	})
	spec.Relations = append(spec.Relations, mapping.Relation{
		ID: "rel_pids", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "public", Name: "order_items"},
		Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, OrderBy: []mapping.OrderColumn{{Column: "line_no"}},
		Shape: mapping.ShapeScalars, ValueColumn: "product_id", TargetFieldID: "f_pids",
	})
	orders, rel := shopTables()
	rel["rel_pids"] = rel["rel_items"]
	p, issues := Compile(spec, orders, Options{Related: rel})
	require.NotNil(t, p, "%v", issues)
	row, related := order1001()
	related["rel_pids"] = &source.RelatedSet{Rows: []source.RelatedRow{
		{Values: []any{int64(1), int64(1001), int32(1), int32(7), int32(2), 9.5}},
		{Values: []any{int64(2), int64(1001), int32(2), int32(9), int32(1), 3.25}},
	}}
	_, doc, _, rerr := p.ApplyWith(row, related, nil)
	require.Nil(t, rerr)
	require.Contains(t, string(doc), `"productIds":[7,9]`, "values in line_no order")

	// A mapping on the item field transforms each value.
	spec.DocSchema.Root.Children[4].Items.Type = docschema.String
	spec.FieldMappings = append(spec.FieldMappings, mapping.FieldMapping{
		ID: "m_pid", TargetFieldID: "f_pid", State: mapping.StateActive,
		Sources:    []mapping.SourceRef{{Relation: "rel_pids", Column: "product_id"}},
		Transforms: []mapping.Step{{Op: "toString"}, {Op: "upper"}},
	})
	p, issues = Compile(spec, orders, Options{Related: rel})
	require.NotNil(t, p, "%v", issues)
	_, doc, _, rerr = p.ApplyWith(row, related, nil)
	require.Nil(t, rerr)
	require.Contains(t, string(doc), `"productIds":["7","9"]`)
}

func TestApplyRelations_AC2_KeyedShape(t *testing.T) {
	spec := m2mSpec()
	f := &spec.DocSchema.Root.Children[1]
	f.Type, f.Children, f.Items = docschema.Object, f.Items.Children[1:], nil // name, quantity, unitPrice
	spec.Relations[0].Shape, spec.Relations[0].KeyColumn = mapping.ShapeKeyed, "sku"
	spec.FieldMappings = append(spec.FieldMappings[:1], spec.FieldMappings[2:]...) // no sku field
	doc, warnings := buildM2M(t, spec, [][]any{
		{int32(1), "A-1", "Pen", int64(1001), int32(2), 9.5, int32(1)},
		{int32(2), "B-2", "Pad", int64(1001), int32(1), 3.25, int32(2)},
	})
	require.Empty(t, warnings)
	require.JSONEq(t, `{"orderId":1001,"products":{"A-1":{"name":"Pen","quantity":2,"unitPrice":9.5},"B-2":{"name":"Pad","quantity":1,"unitPrice":3.25}}}`, doc)

	doc, warnings = buildM2M(t, spec, [][]any{
		{int32(1), "A-1", "Pen", int64(1001), int32(2), 9.5, int32(1)},
		{int32(3), "A-1", "Pen v2", int64(1001), int32(7), 9.0, int32(2)},
	})
	require.JSONEq(t, `{"orderId":1001,"products":{"A-1":{"name":"Pen v2","quantity":7,"unitPrice":9}}}`, doc, "the last item wins")
	require.Len(t, warnings, 1)
	require.Equal(t, CodeRelationDuplicateKey, warnings[0].Code)
}
