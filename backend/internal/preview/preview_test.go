package preview

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/related"
)

func previewSpec() *mapping.Spec {
	parent := "rel_items"
	item := docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
		{ID: "f_product", Name: "product", Type: docschema.Object, Children: []docschema.DocField{}},
	}}
	s := &mapping.Spec{
		Mode: mapping.ModeComposed,
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_items", Name: "items", Type: docschema.Array, Items: &item},
			{ID: "f_last", Name: "lastLine", Type: docschema.Object, Children: []docschema.DocField{}, Binding: &docschema.Binding{Adapter: docschema.AdapterFirst}},
		}}},
		Relations: []mapping.Relation{
			{ID: "rel_items", Kind: mapping.EmbedOneToMany, TargetFieldID: "f_items"},
			{ID: "rel_prod", ParentID: &parent, Kind: mapping.EmbedManyToOne, TargetFieldID: "f_product"},
			{ID: "rel_last", Kind: mapping.EmbedOneToMany, TargetFieldID: "f_last"},
			{ID: "rel_cust", Kind: mapping.EmbedManyToOne, LookupOnly: true},
		},
	}
	s.Normalize()
	return s
}

func rows(n int, sub func(i int) source.Related) *source.RelatedSet {
	set := &source.RelatedSet{}
	for i := range n {
		r := source.RelatedRow{Values: []any{int64(i)}}
		if sub != nil {
			r.Sub = sub(i)
		}
		set.Rows = append(set.Rows, r)
	}
	return set
}

func TestStats_NestedRowsTruncationAndAdapter(t *testing.T) {
	prod := func(int) source.Related { return source.Related{"rel_prod": rows(1, nil)} }
	rel := source.Related{"rel_items": rows(Cap+1, prod), "rel_last": rows(3, nil)}
	got := Stats(previewSpec(), rel, map[string]int64{"rel_items": 1240})
	require.Equal(t, 50, got["items"].Rows, "50 shown")
	require.True(t, got["items"].Truncated)
	require.Equal(t, int64(1240), *got["items"].Total, "of 1,240")
	require.Equal(t, 50, got["items.product"].Rows, "summed over the items shown")
	require.Equal(t, 1, got["lastLine"].Rows)
	require.Equal(t, 2, got["lastLine"].AdapterDiscarded, "the first adapter used 1 of 3")
	require.NotContains(t, got, "rel_cust", "lookups have no node")

	trimmed := Trim(rel)
	require.Len(t, trimmed["rel_items"].Rows, Cap)
	require.Len(t, rel["rel_items"].Rows, Cap+1, "Trim copies")
}

func TestCapped_ReadsOneMoreThanShown(t *testing.T) {
	got := Capped([]related.Plan{
		{ID: "a", Many: true, Keep: 1001},
		{ID: "b", Many: true, Keep: 11}, // a limit of 10: already small
		{ID: "c", Many: true, Keep: 0},  // aggregates need every row
		{ID: "d", Keep: 1},
	})
	require.Equal(t, Cap+1, got[0].Keep)
	require.Equal(t, Cap+1, got[0].Query.PerKeyLimit)
	require.Equal(t, 11, got[1].Keep)
	require.Equal(t, 0, got[2].Keep)
	require.Equal(t, 1, got[3].Keep)
}

func TestEstimateBytes_ScalesTheArray(t *testing.T) {
	items := make([]map[string]int, 50)
	for i := range items {
		items[i] = map[string]int{"n": 10 + i%10} // 8 bytes each: {"n":1x}
	}
	doc, _ := json.Marshal(map[string]any{"id": 1, "summary": map[string]any{"orders": items}})
	est := EstimateBytes(doc, []string{"summary", "orders"}, 50, 1240)
	// 1,190 missing items of 8 bytes, plus a comma each ≈ 9 bytes per item.
	require.InDelta(t, int64(len(doc))+1190*9, est, 1190)
	require.Equal(t, int64(len(doc)), EstimateBytes(doc, []string{"nope"}, 50, 1240))
	require.Equal(t, int64(len(doc)), EstimateBytes(doc, []string{"summary", "orders"}, 50, 50))
}
