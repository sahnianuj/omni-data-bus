package relations

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

func TestJunction_AC2_Rules(t *testing.T) {
	extra := func(n int) []schema.Column {
		var out []schema.Column
		for i := range n {
			out = append(out, cl(string(rune('a'+i))+"_col", "text"))
		}
		return out
	}
	cases := []struct {
		name       string
		table      schema.Table
		confidence string // "" = not a junction
		payload    []string
	}{
		{
			name: "payload junction with a surrogate key",
			table: schema.Table{Name: "order_items", PrimaryKey: []string{"order_item_id"}, Columns: []schema.Column{
				cl("order_item_id", "int8"), cl("order_id", "int8"), cl("product_id", "int4"), cl("quantity", "int4"), cl("unit_price", "numeric"),
			}},
			confidence: ConfidenceMedium, payload: []string{"quantity", "unit_price"},
		},
		{
			name:       "composite primary key of both foreign keys",
			table:      schema.Table{Name: "order_tags", PrimaryKey: []string{"order_id", "product_id"}, Columns: []schema.Column{cl("order_id", "int8"), cl("product_id", "int4")}},
			confidence: ConfidenceHigh, payload: []string{},
		},
		{
			name: "unique constraint on both foreign keys",
			table: schema.Table{
				Name: "order_links", PrimaryKey: []string{"id"}, UniqueConstraints: [][]string{{"product_id", "order_id"}},
				Columns: append([]schema.Column{cl("id", "int8"), cl("order_id", "int8"), cl("product_id", "int4")}, extra(8)...),
			},
			confidence: ConfidenceHigh,
		},
		{
			name:  "two foreign keys and 12 other columns",
			table: schema.Table{Name: "shipments", PrimaryKey: []string{"id"}, Columns: append([]schema.Column{cl("id", "int8"), cl("order_id", "int8"), cl("product_id", "int4")}, extra(12)...)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := shopSnap()
			j := c.table
			j.ForeignKeys = []schema.ForeignKey{
				{Name: "j_order", Columns: []string{"order_id"}, RefSchema: "public", RefTable: "orders", RefColumns: []string{"order_id"}},
				{Name: "j_product", Columns: []string{"product_id"}, RefSchema: "public", RefTable: "products", RefColumns: []string{"product_id"}},
			}
			snap.Schemas[0].Tables = []schema.Table{*snap.Table("public", "orders"), *snap.Table("public", "products"), j}
			snap.Schemas[0].Tables[0].ReferencedBy, snap.Schemas[0].Tables[0].ForeignKeys = nil, nil
			snap.Schemas[0].Tables[1].ReferencedBy = nil
			linkReferences(snap)
			var got *Junction
			for _, c := range Junctions(snap, orders) {
				if c.Via.Name == j.Name {
					got = &c
				}
			}
			if c.confidence == "" {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, c.confidence, got.Confidence)
			require.Equal(t, "products", got.Table.Name)
			require.Equal(t, []mapping.ViaJoin{{Parent: "order_id", Via: "order_id"}}, got.ParentJoin)
			require.Equal(t, []mapping.ViaJoin{{Via: "product_id", Child: "product_id"}}, got.ChildJoin)
			if c.payload != nil {
				require.Equal(t, c.payload, got.PayloadColumns)
			}
		})
	}
}

func TestJunction_AC2_SelfJoin(t *testing.T) {
	users := schema.Table{Name: "users", PrimaryKey: []string{"user_id"}, Columns: []schema.Column{cl("user_id", "int8")}}
	friend := schema.Table{
		Name: "friend", PrimaryKey: []string{"user_a", "user_b"}, Columns: []schema.Column{cl("user_a", "int8"), cl("user_b", "int8")},
		ForeignKeys: []schema.ForeignKey{
			{Name: "friend_a", Columns: []string{"user_a"}, RefSchema: "public", RefTable: "users", RefColumns: []string{"user_id"}},
			{Name: "friend_b", Columns: []string{"user_b"}, RefSchema: "public", RefTable: "users", RefColumns: []string{"user_id"}},
		},
	}
	snap := &schema.Snapshot{Schemas: []schema.Schema{{Name: "public", Tables: []schema.Table{users, friend}}}}
	linkReferences(snap)
	got := Junctions(snap, mapping.TableRef{Schema: "public", Name: "users"})
	require.Len(t, got, 2, "users ↔ users through friend, from either side")
	for _, j := range got {
		require.Equal(t, "users", j.Table.Name)
		require.Equal(t, ConfidenceHigh, j.Confidence)
		require.NotEqual(t, j.ParentJoin[0].Via, j.ChildJoin[0].Via, "two distinct foreign keys")
	}
}

func TestIndexAdvice_AC5(t *testing.T) {
	items := &schema.Table{Name: "order_items", Columns: []schema.Column{cl("order_id", "int8"), cl("product_id", "int4")}}
	require.Equal(t, "Lookups on order_items.order_id will scan the table; consider `CREATE INDEX ON order_items(order_id)`.",
		mapping.IndexAdvice(items, []string{"order_id"}))
	items.Indexes = []schema.Index{{Columns: []string{"order_id", "product_id"}}}
	require.Empty(t, mapping.IndexAdvice(items, []string{"order_id"}), "a composite index leading with order_id")
	require.NotEmpty(t, mapping.IndexAdvice(items, []string{"product_id"}), "product_id isn't a leading column")

	// The graph reports advice for unindexed one-to-many hops.
	snap := shopSnap()
	snap.Table("public", "order_items").Indexes = nil
	for _, p := range Build(snap, nil).Paths(orders, 1) {
		h := p.Hops[0]
		require.Equal(t, h.To.Name == "customers", h.Indexed, "customers' key is its primary key; order_items.order_id has no index")
	}
}
