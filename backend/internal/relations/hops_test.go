package relations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

func est(n int64) *int64 { return &n }

// hopsService serves snap for a project with a source connection; it returns the service and
// the connection id.
func hopsService(t *testing.T, snap *schema.Snapshot, virtual ...store.Relationship) (*Service, string) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New(storetest.NewClock())
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	_, err = st.Connections().Upsert(ctx, ws, p.ID, store.RoleSource, store.ConnectionInput{Type: "postgres", Name: "src"})
	require.NoError(t, err)
	for _, v := range virtual {
		v.ProjectID = p.ID
		_, err := st.Relationships().Create(ctx, ws, v)
		require.NoError(t, err)
	}
	return NewService(st, fixedSchema{snap: snap}, nil), store.ConnectionID(p.ID, store.RoleSource)
}

// catalogSnap: products (brand_id → brands, category_id → categories), reviews → products,
// categories.parent_id → categories, product_tags(product_id, tag_id) → tags.
func catalogSnap() *schema.Snapshot {
	fk := func(name string, col, ref, refCol string) schema.ForeignKey {
		return schema.ForeignKey{Name: name, Columns: []string{col}, RefSchema: "sales", RefTable: ref, RefColumns: []string{refCol}}
	}
	snap := &schema.Snapshot{Schemas: []schema.Schema{{Name: "sales", Tables: []schema.Table{
		{Name: "brands", PrimaryKey: []string{"id"}, Columns: []schema.Column{cl("id", "int4")}, RowEstimate: est(50)},
		{
			Name: "categories", PrimaryKey: []string{"id"}, Columns: []schema.Column{cl("id", "int4"), cl("parent_id", "int4")}, RowEstimate: est(40),
			ForeignKeys: []schema.ForeignKey{fk("cat_parent_fk", "parent_id", "categories", "id")},
		},
		{
			Name: "products", PrimaryKey: []string{"id"}, Columns: []schema.Column{cl("id", "int4"), cl("brand_id", "int4"), cl("category_id", "int4"), cl("supplier_code", "text")}, RowEstimate: est(1000),
			ForeignKeys: []schema.ForeignKey{fk("prod_brand_fk", "brand_id", "brands", "id"), fk("prod_cat_fk", "category_id", "categories", "id")},
		},
		{
			Name: "reviews", PrimaryKey: []string{"id"}, Columns: []schema.Column{cl("id", "int4"), cl("product_id", "int4")}, RowEstimate: est(12000),
			ForeignKeys: []schema.ForeignKey{fk("rev_prod_fk", "product_id", "products", "id")},
		},
		{Name: "suppliers", PrimaryKey: []string{"code"}, Columns: []schema.Column{cl("code", "text")}},
		{Name: "tags", PrimaryKey: []string{"id"}, Columns: []schema.Column{cl("id", "int4")}},
		{
			Name: "product_tags", PrimaryKey: []string{"product_id", "tag_id"}, Columns: []schema.Column{cl("product_id", "int4"), cl("tag_id", "int4")},
			ForeignKeys: []schema.ForeignKey{fk("pt_prod_fk", "product_id", "products", "id"), fk("pt_tag_fk", "tag_id", "tags", "id")},
		},
	}}}}
	linkReferences(snap)
	return snap
}

func hopNames(hs []Hop) []string {
	var out []string
	for _, h := range hs {
		arrow := "↗"
		if h.Kind == mapping.EmbedOneToMany {
			arrow = "↙"
		}
		self := ""
		if h.Self {
			self = " (self)"
		}
		out = append(out, arrow+h.To.Name+self)
	}
	return out
}

// OC-141 AC1: every direct hop of a table, self-references included.
func TestHops_AC1_DirectHopsAndSelfReferences(t *testing.T) {
	svc, conn := hopsService(t, catalogSnap())
	res, err := svc.Hops(context.Background(), ws, conn, mapping.TableRef{Schema: "sales", Name: "products"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"↗brands", "↗categories", "↙product_tags", "↙reviews"}, hopNames(res.Hops))
	require.Equal(t, mapping.MaxRelationDepth(), res.MaxRelationDepth)

	res, err = svc.Hops(context.Background(), ws, conn, mapping.TableRef{Schema: "sales", Name: "categories"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"↗categories (self)", "↙categories (self)", "↙products"}, hopNames(res.Hops))

	_, err = svc.Hops(context.Background(), ws, conn, mapping.TableRef{Schema: "sales", Name: "nope"})
	require.Error(t, err)
}

// OC-141 AC2: a table's hops don't depend on the driving table's path search and its cap.
func TestHops_AC2_IndependentOfPathCap(t *testing.T) {
	snap := catalogSnap()
	sc := &snap.Schemas[0]
	orders := schema.Table{
		Name: "orders", PrimaryKey: []string{"id"}, Columns: []schema.Column{cl("id", "int4"), cl("product_id", "int4")},
		ForeignKeys: []schema.ForeignKey{{Name: "o_prod", Columns: []string{"product_id"}, RefSchema: "sales", RefTable: "products", RefColumns: []string{"id"}}},
	}
	// 250 more parents of orders: the depth-limited, capped path search fills up before
	// orders → products → brands.
	for i := range 250 {
		name := fmt.Sprintf("t%03d", i)
		sc.Tables = append(sc.Tables, schema.Table{Name: name, PrimaryKey: []string{"id"}, Columns: []schema.Column{cl("id", "int4")}})
		orders.Columns = append(orders.Columns, cl(name+"_id", "int4"))
		orders.ForeignKeys = append(orders.ForeignKeys, schema.ForeignKey{Name: name + "_fk", Columns: []string{name + "_id"}, RefSchema: "sales", RefTable: name, RefColumns: []string{"id"}})
	}
	sc.Tables = append(sc.Tables, orders)
	for i := range sc.Tables {
		sc.Tables[i].ReferencedBy = nil
	}
	linkReferences(snap)

	paths := Build(snap, nil).Paths(mapping.TableRef{Schema: "sales", Name: "orders"}, DefaultDepth)
	for _, p := range paths {
		require.NotEqual(t, "↗products↗brands", pathString(p), "the capped search doesn't reach it")
	}
	svc, conn := hopsService(t, snap)
	res, err := svc.Hops(context.Background(), ws, conn, mapping.TableRef{Schema: "sales", Name: "products"})
	require.NoError(t, err)
	require.Contains(t, hopNames(res.Hops), "↗brands")
}

// OC-141 AC3: virtual relationships, junctions, index advice and estimates.
func TestHops_AC3_VirtualJunctionIndexEstimate(t *testing.T) {
	svc, conn := hopsService(t, catalogSnap(), store.Relationship{
		Name: "supplier", From: store.RelationshipEnd{Schema: "sales", Table: "products", Columns: []string{"supplier_code"}},
		To: store.RelationshipEnd{Schema: "sales", Table: "suppliers", Columns: []string{"code"}}, Cardinality: CardinalityManyToOne,
	})
	res, err := svc.Hops(context.Background(), ws, conn, mapping.TableRef{Schema: "sales", Name: "products"})
	require.NoError(t, err)
	var virtual, reviews *Hop
	for i, h := range res.Hops {
		switch h.To.Name {
		case "suppliers":
			virtual = &res.Hops[i]
		case "reviews":
			reviews = &res.Hops[i]
		}
	}
	require.NotNil(t, virtual)
	require.True(t, virtual.Virtual)
	require.NotEmpty(t, virtual.RelationshipID)
	require.NotNil(t, reviews)
	require.False(t, reviews.Indexed)
	require.InDelta(t, 12, reviews.RowsPerParent, 0.001, "12,000 reviews over 1,000 products")
	require.NotEmpty(t, res.Advice)
	require.Equal(t, "reviews", res.Advice[0].Table.Name)
	require.Len(t, res.Junctions, 1)
	require.Equal(t, "tags", res.Junctions[0].Table.Name)
}

// The endpoint: contract and errors.
func TestHopsHandler_AC1(t *testing.T) {
	f := newAPI(t, ws)
	base := "/api/v1/connections/" + store.ConnectionID(f.project, store.RoleSource) + "/relations/hops"
	rec := f.do(http.MethodGet, base+"?table=public.orders", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var res HopsResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	require.ElementsMatch(t, []string{"↗customers", "↙order_items"}, hopNames(res.Hops))
	require.Equal(t, http.StatusBadRequest, f.do(http.MethodGet, base+"?table=orders", "").Code)
	require.Equal(t, http.StatusNotFound, f.do(http.MethodGet, base+"?table=public.nope", "").Code)
}
