package transform

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// groupProgram: one document per order_id of order_detail, products[] ← the group's rows
// (OC-063), capped at 100 with policy.
func groupProgram(t *testing.T, policy string) *Program {
	t.Helper()
	od := &schema.Table{Name: "order_detail", PrimaryKey: []string{"order_id", "line_no"}, Columns: []schema.Column{
		col("type", "text", "string"), col("order_id", "int8", "integer"), col("line_no", "int4", "integer"), col("quantity", "int4", "integer"),
	}}
	item := docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{{ID: "f_line", Name: "lineNo", Type: docschema.Integer}}}
	spec := &mapping.Spec{
		SpecVersion: 2, Mode: mapping.ModeComposed,
		Root:      &mapping.Root{Table: mapping.TableRef{Schema: "shop", Name: "order_detail"}, GroupBy: []string{"order_id"}},
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{{ID: "f_oid", Name: "orderId", Type: docschema.Integer}, {ID: "f_products", Name: "products", Type: docschema.Array, Items: &item}}}},
		Key:       mapping.KeySpec{Template: "order::{order_id}"},
		Relations: []mapping.Relation{{ID: "rel_lines", Group: true, TargetFieldID: "f_products", Limit: 100, Overflow: policy, OrderBy: []mapping.OrderColumn{{Column: "line_no"}}}},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m_oid", TargetFieldID: "f_oid", Sources: []mapping.SourceRef{{Column: "order_id"}}, State: mapping.StateActive},
			{ID: "m_line", TargetFieldID: "f_line", Sources: []mapping.SourceRef{{Relation: "rel_lines", Column: "line_no"}}, State: mapping.StateActive},
		},
	}
	spec.Normalize()
	p, issues := Compile(spec, od, Options{Related: map[string]*schema.Table{"rel_lines": od}})
	require.NotNil(t, p, "%v", issues)
	return p
}

func orderLines(n int) *source.RelatedSet {
	set := &source.RelatedSet{}
	for i := 1; i <= n; i++ {
		set.Rows = append(set.Rows, source.RelatedRow{Values: []any{"order_detail", int64(7), int32(i), int32(1)}})
	}
	return set
}

func TestGroup_AC5_Overflow(t *testing.T) {
	first := []any{"order_detail", int64(7), int32(1), int32(1)} // the group's first row
	rel := source.Related{"rel_lines": orderLines(250)}

	key, doc, warnings, rerr := groupProgram(t, mapping.OverflowTruncate).ApplyWith(first, rel, nil)
	require.Nil(t, rerr)
	require.Equal(t, "order::7", key)
	var got struct {
		OrderID  int               `json:"orderId"`
		Products []json.RawMessage `json:"products"`
	}
	require.NoError(t, json.Unmarshal(doc, &got))
	require.Equal(t, 7, got.OrderID)
	require.Len(t, got.Products, 100)
	require.Equal(t, CodeEmbedTruncated, warnings[0].Code)

	_, _, _, rerr = groupProgram(t, mapping.OverflowFail).ApplyWith(first, rel, nil)
	require.NotNil(t, rerr)
	require.Equal(t, CodeRelationLimitExceeded, rerr.Code)

	p := groupProgram(t, mapping.OverflowSplit)
	_, _, _, rerr = p.ApplyWith(first, rel, nil)
	require.Nil(t, rerr)
	require.Len(t, p.Pages(), 2, "100 in the order, then pages of 100 and 50")
}

// TestGroup_AC2_EachRowOnce (OC-065): read from the group's rows, each line appears once with
// its own values; read from the driving row, every item repeats the first line.
func TestGroup_AC2_EachRowOnce(t *testing.T) {
	first := []any{"order_detail", int64(1), int32(1), int32(3)}
	rows := &source.RelatedSet{}
	for i := 1; i <= 3; i++ {
		rows.Rows = append(rows.Rows, source.RelatedRow{Values: []any{"order_detail", int64(1), int32(i), int32(3)}})
	}
	_, doc, _, rerr := groupProgram(t, mapping.OverflowTruncate).ApplyWith(first, source.Related{"rel_lines": rows}, nil)
	require.Nil(t, rerr)
	require.JSONEq(t, `{"orderId":1,"products":[{"lineNo":1},{"lineNo":2},{"lineNo":3}]}`, string(doc))
}
