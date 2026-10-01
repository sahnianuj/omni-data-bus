package transform

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/related"
	"omnidatabus/internal/schema"
)

// linesSpec is an order with items[] (limit 1000) and, when aggregated, itemCount and
// orderTotal over the same lines.
func linesSpec(aggregate bool) *mapping.Spec {
	spec := shopSpec()
	spec.Relations = spec.Relations[1:2] // items only
	spec.FieldMappings = []mapping.FieldMapping{
		{ID: "m_oid", TargetFieldID: "f_oid", Sources: []mapping.SourceRef{{Column: "order_id"}}, State: mapping.StateActive},
		{ID: "m_line", TargetFieldID: "f_line", Sources: []mapping.SourceRef{{Relation: "rel_items", Column: "line_no"}}, State: mapping.StateActive},
	}
	items := &spec.DocSchema.Root.Children[3]
	items.Items.Children = items.Items.Children[:1]
	spec.DocSchema.Root.Children = []docschema.DocField{spec.DocSchema.Root.Children[0], *items}
	if aggregate {
		spec.DocSchema.Root.Children = append(spec.DocSchema.Root.Children,
			docschema.DocField{ID: "f_count", Name: "itemCount", Type: docschema.Integer},
			docschema.DocField{ID: "f_total", Name: "orderTotal", Type: docschema.Number})
		spec.Aggregates = []mapping.Aggregate{
			{ID: "agg_1", RelationID: "rel_items", Fn: "count", TargetFieldID: "f_count"},
			{ID: "agg_2", RelationID: "rel_items", Fn: "sum", Column: "line_total", TargetFieldID: "f_total"},
		}
	}
	return spec
}

func lineRows(n int) *source.RelatedSet {
	set := &source.RelatedSet{}
	for i := 1; i <= n; i++ {
		set.Rows = append(set.Rows, source.RelatedRow{Values: []any{int64(i), int64(1001), int32(i), int32(7), int32(1), mustNumeric("0.10")}})
	}
	return set
}

func linesProgram(t *testing.T, spec *mapping.Spec) *Program {
	t.Helper()
	orders, rel := shopTables()
	items := *rel["rel_items"]
	items.Columns = append([]schema.Column(nil), items.Columns...)
	items.Columns[5] = col("line_total", "numeric", "number")
	rel["rel_items"] = &items
	p, issues := Compile(spec, orders, Options{Related: rel})
	require.NotNil(t, p, "%v", issues)
	return p
}

func TestAggregates_AC4_BeforeLimitDecimal(t *testing.T) {
	spec := linesSpec(true)
	p := linesProgram(t, spec)
	_, doc, warnings, rerr := p.ApplyWith([]any{int64(1001), "x", int32(1)}, source.Related{"rel_items": lineRows(1500)}, nil)
	require.Nil(t, rerr)
	var got struct {
		Items      []json.RawMessage `json:"items"`
		ItemCount  int               `json:"itemCount"`
		OrderTotal json.Number       `json:"orderTotal"`
	}
	require.NoError(t, json.Unmarshal(doc, &got))
	require.Len(t, got.Items, 1000, "the array is truncated at the limit")
	require.Equal(t, 1500, got.ItemCount, "the count is over every line")
	require.Equal(t, json.Number("150"), got.OrderTotal, "1,500 × 0.10 exactly, no float error")
	require.Equal(t, CodeEmbedTruncated, warnings[0].Code)
}

func TestAggregates_AC4_AggregateOnlyRelation(t *testing.T) {
	spec := linesSpec(true)
	spec.DocSchema.Root.Children = append(spec.DocSchema.Root.Children[:1], spec.DocSchema.Root.Children[2:]...) // no items[]
	spec.Relations[0].TargetFieldID = ""
	spec.FieldMappings = spec.FieldMappings[:1]
	p := linesProgram(t, spec)
	_, doc, _, rerr := p.ApplyWith([]any{int64(1001), "x", int32(1)}, source.Related{"rel_items": lineRows(3)}, nil)
	require.Nil(t, rerr)
	require.JSONEq(t, `{"orderId":1001,"itemCount":3,"orderTotal":0.3}`, string(doc))

	// Only the needed columns are read: the join, order and aggregated columns.
	orders, rel := shopTables()
	items := *rel["rel_items"]
	items.Columns = append([]schema.Column(nil), items.Columns...)
	items.Columns[5] = col("line_total", "numeric", "number")
	plans, err := related.Plans(spec, orders, func(_, _ string) *schema.Table { return &items })
	require.NoError(t, err)
	require.Equal(t, []bool{false, true, true, false, false, true}, plans[0].Query.Table.Project)
	require.Zero(t, plans[0].Keep, "aggregates see every row")

	_, doc, _, _ = p.ApplyWith([]any{int64(1001), "x", int32(1)}, nil, nil)
	require.JSONEq(t, `{"orderId":1001,"itemCount":0,"orderTotal":null}`, string(doc), "no lines: count 0, sum null")
}
