package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocation_PathAndString(t *testing.T) {
	cb := Location{Kind: KindCouchbase, Bucket: "shop", Scope: "sales", Collection: "orders"}
	require.Equal(t, "shop.sales.orders", cb.String())
	require.Equal(t, "shop/sales/orders", cb.Path())
	require.Equal(t, cb, LocationFromPath(KindCouchbase, "shop/sales/orders"))
	require.Equal(t, Location{Kind: KindCouchbase, Bucket: "shop"}, LocationFromPath(KindCouchbase, "/shop/"))
	require.Equal(t, "shop", Location{Kind: KindCouchbase, Bucket: "shop"}.Path())
	require.Equal(t, Location{Kind: KindCouchbase}, LocationFromPath(KindCouchbase, ""))

	dir := Location{Kind: KindDirectory, Folder: "orders/2026"}
	require.Equal(t, "orders/2026/", dir.String())
	require.Equal(t, dir, LocationFromPath(KindDirectory, "orders/2026"))
	require.True(t, Location{Kind: KindDirectory}.IsZero())
}

func TestLocation_V1TargetRoundTrip(t *testing.T) {
	m := Mapping{Target: MappingTarget{Bucket: "shop", Scope: "sales", Collection: "orders", CreateIfMissing: true}}
	require.Equal(t, Location{Kind: KindCouchbase, Bucket: "shop", Scope: "sales", Collection: "orders", CreateIfMissing: true}, m.Where())
	m.NormalizeLocation()
	require.Equal(t, KindCouchbase, m.Location.Kind)

	// A directory location clears the 1.0 target.
	m.SetLocation(Location{Kind: KindDirectory, Folder: "orders"})
	require.Equal(t, MappingTarget{}, m.Target)
	require.Equal(t, "orders", m.Where().Folder)
	require.Equal(t, Location{}, Mapping{}.Where())
}

func TestUpdatedLocation_WhicheverChanged(t *testing.T) {
	cur := Mapping{Target: MappingTarget{Bucket: "b", Scope: "s", Collection: "c"}}
	cur.NormalizeLocation()

	v1 := cur
	v1.Target.Collection = "c2" // a 1.0 code path edits only Target
	require.Equal(t, "c2", UpdatedLocation(cur, v1).Collection)

	v2 := cur
	v2.Location = Location{Kind: KindDirectory, Folder: "x"}
	require.Equal(t, "x", UpdatedLocation(cur, v2).Folder)

	require.Equal(t, cur.Location, UpdatedLocation(cur, cur))
}
