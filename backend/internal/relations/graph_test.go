package relations

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

func cl(name, udt string) schema.Column { return schema.Column{Name: name, UDTName: udt} }

// shopSnap: orders(customer_id → customers), order_items(order_id → orders, product_id → products).
func shopSnap() *schema.Snapshot {
	fk := func(name string, cols []string, ref string, refCols []string) schema.ForeignKey {
		return schema.ForeignKey{Name: name, Columns: cols, RefSchema: "public", RefTable: ref, RefColumns: refCols}
	}
	snap := &schema.Snapshot{Schemas: []schema.Schema{{Name: "public", Tables: []schema.Table{
		{Name: "customers", PrimaryKey: []string{"customer_id"}, Columns: []schema.Column{cl("customer_id", "int4"), cl("code", "text"), cl("name", "text")}},
		{
			Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{cl("order_id", "int8"), cl("customer_id", "int4"), cl("cust_code", "text")},
			ForeignKeys: []schema.ForeignKey{fk("orders_customer_fk", []string{"customer_id"}, "customers", []string{"customer_id"})},
		},
		{
			Name: "order_items", PrimaryKey: []string{"order_item_id"},
			Columns: []schema.Column{cl("order_item_id", "int8"), cl("order_id", "int8"), cl("product_id", "int4"), cl("quantity", "int4"), cl("unit_price", "numeric")},
			ForeignKeys: []schema.ForeignKey{
				fk("items_order_fk", []string{"order_id"}, "orders", []string{"order_id"}),
				fk("items_product_fk", []string{"product_id"}, "products", []string{"product_id"}),
			},
			Indexes: []schema.Index{{Name: "items_order_idx", Columns: []string{"order_id", "product_id"}}},
		},
		{Name: "products", PrimaryKey: []string{"product_id"}, Columns: []schema.Column{cl("product_id", "int4"), cl("sku", "text")}},
	}}}}
	linkReferences(snap)
	return snap
}

// linkReferences fills ReferencedBy from ForeignKeys, as discovery does.
func linkReferences(snap *schema.Snapshot) {
	for si := range snap.Schemas {
		for ti := range snap.Schemas[si].Tables {
			t := &snap.Schemas[si].Tables[ti]
			for _, fk := range t.ForeignKeys {
				ref := snap.Table(fk.RefSchema, fk.RefTable)
				ref.ReferencedBy = append(ref.ReferencedBy, schema.Reference{Schema: snap.Schemas[si].Name, Table: t.Name, Name: fk.Name, Columns: fk.Columns, RefColumns: fk.RefColumns})
			}
		}
	}
}

var orders = mapping.TableRef{Schema: "public", Name: "orders"}

func pathString(p Path) string {
	s := ""
	for _, h := range p.Hops {
		arrow := "↗"
		if h.Kind == mapping.EmbedOneToMany {
			arrow = "↙"
		}
		s += arrow + h.To.Name
	}
	return s
}

func TestPaths_AC1_MultiHop(t *testing.T) {
	paths := Build(shopSnap(), nil).Paths(orders, 4)
	var got []string
	for _, p := range paths {
		got = append(got, pathString(p))
	}
	require.Contains(t, got, "↙order_items↗products", "orders ↙ order_items ↗ products")
	require.Equal(t, []string{"↗customers", "↙order_items"}, got[:2], "direct relations first (both indexed foreign keys: by name)")
	require.Contains(t, got, "↗customers")
	for _, p := range paths {
		if pathString(p) == "↙order_items↗products" {
			require.Equal(t, mapping.EmbedOneToMany, p.Hops[0].Kind)
			require.Equal(t, mapping.EmbedManyToOne, p.Hops[1].Kind)
			require.Equal(t, []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, p.Hops[1].Join)
			require.True(t, p.Hops[0].Indexed, "order_items(order_id, product_id) leads with order_id")
		}
	}
}

func TestPaths_AC1_NoCycles(t *testing.T) {
	for _, p := range Build(shopSnap(), nil).Paths(orders, 6) {
		seen := map[string]bool{"public.orders": true}
		for _, h := range p.Hops {
			require.False(t, seen[key(h.To)], "path %s revisits %s", pathString(p), h.To)
			seen[key(h.To)] = true
		}
	}
}

func TestPaths_VirtualRelationships(t *testing.T) {
	v := []store.Relationship{{
		ID: "vrel_01", From: store.RelationshipEnd{Schema: "public", Table: "orders", Columns: []string{"cust_code"}},
		To: store.RelationshipEnd{Schema: "public", Table: "customers", Columns: []string{"code"}}, Cardinality: CardinalityManyToOne,
	}}
	g := Build(shopSnap(), v)
	var virtual *Hop
	for _, h := range g.Hops(orders) {
		if h.Virtual {
			virtual = &h
		}
	}
	require.NotNil(t, virtual)
	require.Equal(t, "vrel_01", virtual.RelationshipID)
	require.Equal(t, mapping.EmbedManyToOne, virtual.Kind)
	require.Equal(t, []mapping.JoinPair{{Parent: "cust_code", Child: "code"}}, virtual.Join)
	back := g.Hops(mapping.TableRef{Schema: "public", Name: "customers"})
	found := false
	for _, h := range back {
		found = found || (h.Virtual && h.Kind == mapping.EmbedOneToMany && h.To.Name == "orders")
	}
	require.True(t, found, "customers ↙ orders through the virtual relationship")

	cands := VirtualCandidates(shopSnap(), orders, v, nil)
	require.Len(t, cands, 1)
	require.Equal(t, "vrel_01", cands[0].Virtual)
}

// A table joined to itself on the same columns, a repeated relationship and a virtual copy of a
// foreign key add no hops: each would be the same choice listed again.
func TestBuild_SkipsSelfAndDuplicateHops(t *testing.T) {
	end := func(table string, cols ...string) store.RelationshipEnd {
		return store.RelationshipEnd{Schema: "public", Table: table, Columns: cols}
	}
	v := []store.Relationship{
		{ID: "self1", From: end("order_items", "order_id"), To: end("order_items", "order_id")},
		{ID: "self2", From: end("order_items", "order_id"), To: end("order_items", "order_id")},
		{ID: "copy", From: end("orders", "customer_id"), To: end("customers", "customer_id")},
		{ID: "dup1", From: end("orders", "cust_code"), To: end("customers", "code")},
		{ID: "dup2", From: end("orders", "cust_code"), To: end("customers", "code")},
	}
	g := Build(shopSnap(), v)
	for _, h := range g.Hops(mapping.TableRef{Schema: "public", Name: "order_items"}) {
		require.NotEqual(t, "order_items", h.To.Name, "no self hop on the same column")
	}
	var names []string
	for _, h := range g.Hops(orders) {
		names = append(names, h.Kind+" "+h.To.Name+" "+h.Constraint+h.RelationshipID)
	}
	require.Equal(t, []string{
		"many-to-one customers orders_customer_fk", // the foreign key wins over its virtual copy
		"many-to-one customers dup1",
		"one-to-many order_items items_order_fk",
	}, names)
	require.Len(t, VirtualCandidates(shopSnap(), orders, v, nil), 1)
}

// wideSnap is 1,000 tables in a chain with cross links (each has FKs to the 3 before it).
func wideSnap() *schema.Snapshot {
	sc := schema.Schema{Name: "s"}
	for i := range 1000 {
		t := schema.Table{Name: fmt.Sprintf("t%d", i), PrimaryKey: []string{"id"}, Columns: []schema.Column{cl("id", "int8"), cl("a", "int8"), cl("b", "int8"), cl("c", "int8")}}
		for n, col := range []string{"a", "b", "c"} {
			if j := i - n - 1; j >= 0 {
				t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{Name: fmt.Sprintf("fk%d_%s", i, col), Columns: []string{col}, RefSchema: "s", RefTable: fmt.Sprintf("t%d", j), RefColumns: []string{"id"}})
			}
		}
		sc.Tables = append(sc.Tables, t)
	}
	snap := &schema.Snapshot{Schemas: []schema.Schema{sc}}
	linkReferences(snap)
	return snap
}

func TestPaths_BoundedOnLargeSchemas(t *testing.T) {
	snap := wideSnap()
	g := Build(snap, nil)
	start := time.Now()
	paths := g.Paths(mapping.TableRef{Schema: "s", Name: "t500"}, 4)
	require.Less(t, time.Since(start), 50*time.Millisecond)
	require.Len(t, paths, MaxPaths)
}

func BenchmarkPaths_1000Tables(b *testing.B) {
	g := Build(wideSnap(), nil)
	root := mapping.TableRef{Schema: "s", Name: "t500"}
	b.ResetTimer()
	for range b.N {
		_ = g.Paths(root, 4)
	}
}

// OC-157 AC2: a foreign key on unique columns is one-to-one: the hop back is an object too.
func TestBuild_OC157_AC2_OneToOneForeignKeys(t *testing.T) {
	snap := shopSnap()
	sc := &snap.Schemas[0]
	sc.Tables = append(sc.Tables, schema.Table{
		Name: "order_totals", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{cl("order_id", "int8"), cl("grand_total", "numeric")},
		ForeignKeys: []schema.ForeignKey{{Name: "totals_order_fk", Columns: []string{"order_id"}, RefSchema: "public", RefTable: "orders", RefColumns: []string{"order_id"}}},
	})
	g := Build(snap, nil)
	byTable := map[string]Hop{}
	for _, h := range g.Hops(orders) {
		byTable[h.To.Name] = h
	}
	require.Equal(t, mapping.DirectionReferences, byTable["customers"].Direction)
	require.Equal(t, mapping.CardinalityManyToOne, byTable["customers"].Cardinality)
	items := byTable["order_items"]
	require.Equal(t, mapping.DirectionReferencedBy, items.Direction)
	require.Equal(t, mapping.EmbedOneToMany, items.Kind)
	require.Equal(t, mapping.CardinalityOneToMany, items.Cardinality)
	totals := byTable["order_totals"]
	require.Equal(t, mapping.DirectionReferencedBy, totals.Direction)
	require.Equal(t, mapping.EmbedManyToOne, totals.Kind, "an object, not an array of one")
	require.Equal(t, mapping.CardinalityOneToOne, totals.Cardinality)
	require.Equal(t, []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, totals.Join)

	up := g.Hops(mapping.TableRef{Schema: "public", Name: "order_totals"})
	require.Len(t, up, 1)
	require.Equal(t, mapping.DirectionReferences, up[0].Direction)
	require.Equal(t, mapping.CardinalityOneToOne, up[0].Cardinality)
}

// OC-157 AC6: a virtual relationship's From side references its To side, with its cardinality.
func TestBuild_OC157_AC6_VirtualDirection(t *testing.T) {
	v := []store.Relationship{{
		ID: "vrel_1", From: store.RelationshipEnd{Schema: "public", Table: "orders", Columns: []string{"cust_code"}},
		To: store.RelationshipEnd{Schema: "public", Table: "customers", Columns: []string{"code"}}, Cardinality: CardinalityOneToOne,
	}}
	g := Build(shopSnap(), v)
	find := func(from mapping.TableRef, to string) Hop {
		for _, h := range g.Hops(from) {
			if h.Virtual && h.To.Name == to {
				return h
			}
		}
		t.Fatalf("no virtual hop %s → %s", from.Name, to)
		return Hop{}
	}
	fwd := find(orders, "customers")
	require.Equal(t, mapping.DirectionReferences, fwd.Direction)
	require.Equal(t, mapping.CardinalityOneToOne, fwd.Cardinality)
	back := find(mapping.TableRef{Schema: "public", Name: "customers"}, "orders")
	require.Equal(t, mapping.DirectionReferencedBy, back.Direction)
	require.Equal(t, mapping.CardinalityOneToOne, back.Cardinality)
	require.Equal(t, mapping.EmbedManyToOne, back.Kind)
}
