package transform

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/pathcodec"
	"omnidatabus/internal/schema"
)

type overflowDoc struct {
	Items         []json.RawMessage `json:"items"`
	ItemsOverflow *struct {
		Count int      `json:"count"`
		Pages []string `json:"pages"`
	} `json:"itemsOverflow"`
}

type pageDoc struct {
	Parent string            `json:"parent"`
	Page   int               `json:"page"`
	Items  []json.RawMessage `json:"items"`
}

func overflowProgram(t *testing.T, policy string) *Program {
	t.Helper()
	spec := linesSpec(false)
	spec.Relations[0].Overflow = policy
	return linesProgram(t, spec)
}

func TestOverflow_AC4_Policies(t *testing.T) {
	row := []any{int64(1001), "x", int32(1)}
	rel := source.Related{"rel_items": lineRows(2500)}

	// truncate: the first 1,000 items and a warning (1.0 behavior).
	p := overflowProgram(t, mapping.OverflowTruncate)
	_, doc, warnings, rerr := p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	var got overflowDoc
	require.NoError(t, json.Unmarshal(doc, &got))
	require.Len(t, got.Items, 1000)
	require.Nil(t, got.ItemsOverflow)
	require.Equal(t, CodeEmbedTruncated, warnings[0].Code)
	require.Empty(t, p.Pages())

	// fail: the row is dead-lettered.
	p = overflowProgram(t, mapping.OverflowFail)
	_, _, _, rerr = p.ApplyWith(row, rel, nil)
	require.NotNil(t, rerr)
	require.Equal(t, CodeRelationLimitExceeded, rerr.Code)
	require.Equal(t, "items", rerr.FieldPath)

	// split: 1,000 items in the order, the rest on pages 2 (1,000) and 3 (500).
	p = overflowProgram(t, mapping.OverflowSplit)
	key, doc, warnings, rerr := p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.Empty(t, warnings)
	got = overflowDoc{}
	require.NoError(t, json.Unmarshal(doc, &got))
	require.Len(t, got.Items, 1000)
	require.NotNil(t, got.ItemsOverflow)
	require.Equal(t, 2500, got.ItemsOverflow.Count)
	require.Equal(t, []string{"order::1001::items::2", "order::1001::items::3"}, got.ItemsOverflow.Pages)
	pages := p.Pages()
	require.Len(t, pages, 2)
	for i, want := range []int{1000, 500} {
		var pg pageDoc
		require.NoError(t, json.Unmarshal(pages[i].Doc, &pg))
		require.Equal(t, got.ItemsOverflow.Pages[i], pages[i].Key)
		require.Equal(t, key, pg.Parent)
		require.Equal(t, i+2, pg.Page)
		require.Len(t, pg.Items, want)
	}
	var first struct {
		LineNo int `json:"lineNo"`
	}
	var pg pageDoc
	require.NoError(t, json.Unmarshal(pages[0].Doc, &pg))
	require.NoError(t, json.Unmarshal(pg.Items[0], &first))
	require.Equal(t, 1001, first.LineNo, "page 2 continues after the parent's items")

	// Under the limit nothing is split.
	_, _, _, rerr = p.ApplyWith(row, source.Related{"rel_items": lineRows(10)}, nil)
	require.Nil(t, rerr)
	require.Empty(t, p.Pages(), "pages are per document")
}

func TestOverflow_AC4_SplitKeysAndPaths(t *testing.T) {
	p := overflowProgram(t, mapping.OverflowSplit)
	_, _, _, rerr := p.ApplyWith([]any{int64(1001), "x", int32(1)}, source.Related{"rel_items": lineRows(2001)}, nil)
	require.Nil(t, rerr)
	for _, pg := range p.Pages() {
		// Page keys go through the same key rules and file-name encoding as any key.
		require.LessOrEqual(t, len(pg.Key), mapping.MaxKeyBytes)
		name, hashed := pathcodec.Encode(pg.Key)
		require.False(t, hashed)
		back, ok := pathcodec.Decode(name)
		require.True(t, ok)
		require.Equal(t, pg.Key, back, "the file name maps back to the page key")
	}

	// A page key over the key limit fails the row.
	spec := linesSpec(false)
	spec.Relations[0].Overflow = mapping.OverflowSplit
	spec.Key.Template = "{order_id}::{ordered_at}"
	p = linesProgram(t, spec)
	long := make([]byte, 240)
	for i := range long {
		long[i] = 'x'
	}
	_, _, _, rerr = p.ApplyWith([]any{int64(1001), string(long), int32(1)}, source.Related{"rel_items": lineRows(1001)}, nil)
	require.NotNil(t, rerr)
	require.Equal(t, mapping.CodeKeyTooLong, rerr.Code)
}

// The size guard halves an array whose overflow policy is truncate (with DOC_SIZE_TRUNCATED)
// until the document fits; with fail it's DOC_TOO_LARGE.
func TestOverflow_SizeGuardShrinks(t *testing.T) {
	compileMax := func(policy string) *Program {
		spec := linesSpec(false)
		spec.Relations[0].Overflow = policy
		orders, rel := shopTables()
		items := *rel["rel_items"]
		items.Columns = append([]schema.Column(nil), items.Columns...)
		items.Columns[5] = col("line_total", "numeric", "number")
		rel["rel_items"] = &items
		p, issues := Compile(spec, orders, Options{Related: rel, MaxDocBytes: 2000})
		require.NotNil(t, p, "%v", issues)
		return p
	}
	row := []any{int64(1001), "x", int32(1)}
	rel := source.Related{"rel_items": lineRows(500)}

	_, doc, warnings, rerr := compileMax(mapping.OverflowTruncate).ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.LessOrEqual(t, len(doc), 2000)
	var got overflowDoc
	require.NoError(t, json.Unmarshal(doc, &got))
	require.NotEmpty(t, got.Items)
	require.Less(t, len(got.Items), 500)
	var codes []string
	for _, w := range warnings {
		codes = append(codes, w.Code)
	}
	require.Contains(t, codes, CodeDocSizeTruncated)

	_, _, _, rerr = compileMax(mapping.OverflowFail).ApplyWith(row, rel, nil)
	require.NotNil(t, rerr)
	require.Equal(t, CodeDocTooLarge, rerr.Code)
}
