package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/plan"
	"omnidatabus/internal/related"
	"omnidatabus/internal/transform"
)

// bigOrder is one order with 250 lines of 100 KiB each: a ~25 MiB document.
func bigOrder() ([]any, source.Related) {
	memo := strings.Repeat("m", 100<<10)
	set := &source.RelatedSet{}
	for line := int32(1); line <= 250; line++ {
		set.Rows = append(set.Rows, source.RelatedRow{Values: []any{int64(1), line, int32(1), memo}})
	}
	return []any{int64(1), "n"}, source.Related{"rel_items": set}
}

func sizeGuardProgram(t *testing.T, policy string, maxBytes int64) *transform.Program {
	t.Helper()
	orders, rel := shopSchema()
	spec := itemsProductSpec()
	spec.Relations = spec.Relations[:1]
	spec.DocSchema.Root.Children[1].Items.Children = spec.DocSchema.Root.Children[1].Items.Children[:2]
	spec.FieldMappings = spec.FieldMappings[:3]
	spec.Relations[0].Overflow = policy
	pl, issues, err := plan.Compile(plan.Input{Spec: spec, Root: mapping.TableRef{Schema: "shop", Name: "orders"}, Table: orders, Related: rel})
	require.NoError(t, err)
	require.NotNil(t, pl.Program, "%v", issues)
	pl.SetMaxDocBytes(maxBytes)
	p, err := pl.NewProgram()
	require.NoError(t, err)
	return p
}

func TestSizeGuard_AC5_Policies(t *testing.T) {
	const limit = 20 << 20
	row, rel := bigOrder()

	// truncate: the largest array is halved until the document fits.
	p := sizeGuardProgram(t, mapping.OverflowTruncate, limit)
	_, doc, warnings, rerr := p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.LessOrEqual(t, len(doc), limit)
	var got struct {
		Items []json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(doc, &got))
	require.Len(t, got.Items, 125, "250 → 125 items")
	require.Len(t, warnings, 1)
	require.Equal(t, transform.CodeDocSizeTruncated, warnings[0].Code)
	require.Equal(t, "the document was too large; kept 125 of 250 items in items", warnings[0].Message)

	// fail (and 1.0 mappings, which set no policy): DOC_TOO_LARGE.
	for _, policy := range []string{mapping.OverflowFail, ""} {
		p = sizeGuardProgram(t, policy, limit)
		_, _, _, rerr = p.ApplyWith(row, rel, nil)
		require.NotNil(t, rerr, policy)
		require.Equal(t, transform.CodeDocTooLarge, rerr.Code)
	}

	// split: the largest array is paginated.
	p = sizeGuardProgram(t, mapping.OverflowSplit, limit)
	key, doc, _, rerr := p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.LessOrEqual(t, len(doc), limit)
	var split struct {
		Items         []json.RawMessage `json:"items"`
		ItemsOverflow struct {
			Count int      `json:"count"`
			Pages []string `json:"pages"`
		} `json:"itemsOverflow"`
	}
	require.NoError(t, json.Unmarshal(doc, &split))
	require.Len(t, split.Items, 125)
	require.Equal(t, 250, split.ItemsOverflow.Count)
	require.Equal(t, []string{key + "::items::2"}, split.ItemsOverflow.Pages)
	require.Len(t, p.Pages(), 1)
	for _, pg := range p.Pages() {
		require.LessOrEqual(t, len(pg.Doc), limit)
	}

	// A directory's smaller limit applies (the writer tells the plan, OC-038 maxDocBytes).
	p = sizeGuardProgram(t, mapping.OverflowTruncate, 1<<20)
	_, doc, _, rerr = p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.LessOrEqual(t, len(doc), 1<<20)
}

func TestTransformer_RelatedFailuresAndPages(t *testing.T) {
	row, rel := bigOrder()
	tr := Transformer{MappingID: "m", NewProgram: func() (*transform.Program, error) {
		return sizeGuardProgram(t, mapping.OverflowSplit, 20<<20), nil
	}}
	p, _ := tr.NewProgram()
	b := &source.Batch{
		Rows:            [][]any{row, {int64(2), "n"}},
		Related:         []source.Related{rel, nil},
		RelatedFailures: map[int]source.RowFailure{1: {Code: related.CodeRelationMemoryExceeded, Message: "too big"}},
	}
	db := tr.apply(p, b)
	require.Len(t, db.Docs, 2, "the order and its one page")
	require.False(t, db.Docs[0].Page)
	require.True(t, db.Docs[1].Page)
	require.Len(t, db.RowErrors, 1)
	require.Equal(t, related.CodeRelationMemoryExceeded, db.RowErrors[0].Code)
}
