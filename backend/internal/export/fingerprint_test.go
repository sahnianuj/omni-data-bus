package export

import (
	"encoding/json"
	"testing"

	"omnidatabus/internal/mapping"

	"github.com/stretchr/testify/require"
)

func TestFingerprint_AC5_Diff(t *testing.T) {
	f := newFixture(t)
	// Unmap products.unused: its column is then not referenced.
	var spec mapping.Spec
	require.NoError(t, json.Unmarshal(f.items.Spec, &spec))
	kept := spec.FieldMappings[:0]
	for _, fm := range spec.FieldMappings {
		if len(fm.Sources) == 0 || fm.Sources[0].Column != "unused" {
			kept = append(kept, fm)
		}
	}
	require.Less(t, len(kept), len(spec.FieldMappings))
	spec.FieldMappings = kept
	f.items.Spec, _ = json.Marshal(spec)
	_, err := f.st.Mappings().Update(ctx, ws, f.items)
	require.NoError(t, err)
	b := f.build(t, externalReq()).Bundle
	fp := b.SourceFingerprint
	require.ElementsMatch(t, []string{"public.orders", "public.order_items", "public.products"}, keys(fp.Tables))
	var names []string
	for _, c := range fp.Tables["public.products"].Columns {
		names = append(names, c.Name)
	}
	require.NotContains(t, names, "unused", "only referenced columns count")
	require.Empty(t, CompareFingerprint(fp, shopSnapshot()))

	// An unreferenced column changing isn't drift.
	snap := shopSnapshot()
	snap.Table("public", "products").Columns[2].UDTName = "int8"
	require.Empty(t, CompareFingerprint(fp, snap))

	// quantity int4 → text: drift naming the column, compatible (text → integer coerces, OC-020).
	snap = shopSnapshot()
	snap.Table("public", "order_items").Column("quantity").UDTName = "text"
	ds := CompareFingerprint(fp, snap)
	require.Len(t, ds, 1)
	require.Equal(t, "public.order_items", ds[0].Table)
	require.Equal(t, []string{"quantity: int4 → text"}, ds[0].Retyped)
	require.False(t, ds[0].Incompatible)
	require.Contains(t, DriftMessage(ds), "quantity: int4 → text")

	// A removed column, a missing table or a scalar → array change can't continue.
	snap = shopSnapshot()
	tbl := snap.Table("public", "order_items")
	tbl.Columns = tbl.Columns[:3] // drops quantity
	snap.Table("public", "orders").Column("status").UDTName = "_text"
	ds = CompareFingerprint(fp, snap)
	require.Len(t, ds, 2)
	for _, d := range ds {
		require.True(t, d.Incompatible, d.String())
	}
	snap.Schemas[0].Tables = snap.Schemas[0].Tables[:2] // drops products
	ds = CompareFingerprint(fp, snap)
	require.True(t, ds[len(ds)-1].Missing)
	require.Equal(t, "public.products: table missing", ds[len(ds)-1].String())
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
