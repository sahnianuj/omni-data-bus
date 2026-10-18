package automap

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/relations"
	"omnidatabus/internal/schema"
)

// shop: customers ← orders → order_items → products ↔ tags (product_tags), orders ← payments,
// and sales.orders_archive (a weaker driving-table candidate).
func shopSnapshot() *schema.Snapshot {
	c := func(name, jt string) schema.Column { return schema.Column{Name: name, JSONType: jt, UDTName: jt} }
	pk := func(name, jt string) schema.Column {
		col := c(name, jt)
		col.IsPrimaryKey = true
		return col
	}
	fk := func(name string, cols []string, ref string, refCols []string) schema.ForeignKey {
		return schema.ForeignKey{Name: name, Columns: cols, RefSchema: "public", RefTable: ref, RefColumns: refCols}
	}
	s, i, n := schema.TypeString, schema.TypeInteger, schema.TypeNumber
	snap := &schema.Snapshot{Schemas: []schema.Schema{
		{Name: "public", Tables: []schema.Table{
			{Name: "customers", PrimaryKey: []string{"customer_id"}, Columns: []schema.Column{pk("customer_id", i), c("name", s), c("email", s)}},
			{
				Name: "orders", PrimaryKey: []string{"order_id"},
				Columns: []schema.Column{
					pk("order_id", i), c("customer_id", i), c("status", s), c("order_date", schema.TypeDatetime), c("total", n),
					c("ship_street", s), c("ship_city", s), c("ship_zip", s),
				},
				ForeignKeys: []schema.ForeignKey{fk("orders_customer_fk", []string{"customer_id"}, "customers", []string{"customer_id"})},
			},
			{
				Name: "order_items", PrimaryKey: []string{"order_id", "line_no"},
				Columns: []schema.Column{c("order_id", i), c("line_no", i), c("product_id", i), c("quantity", i), c("unit_price", n)},
				ForeignKeys: []schema.ForeignKey{
					fk("items_order_fk", []string{"order_id"}, "orders", []string{"order_id"}),
					fk("items_product_fk", []string{"product_id"}, "products", []string{"product_id"}),
				},
			},
			{Name: "products", PrimaryKey: []string{"product_id"}, Columns: []schema.Column{pk("product_id", i), c("sku", s), c("name", s), c("price", n)}},
			{Name: "tags", PrimaryKey: []string{"tag_id"}, Columns: []schema.Column{pk("tag_id", i), c("name", s)}},
			{
				Name: "product_tags", PrimaryKey: []string{"product_id", "tag_id"},
				Columns: []schema.Column{c("product_id", i), c("tag_id", i)},
				ForeignKeys: []schema.ForeignKey{
					fk("pt_product_fk", []string{"product_id"}, "products", []string{"product_id"}),
					fk("pt_tag_fk", []string{"tag_id"}, "tags", []string{"tag_id"}),
				},
			},
			{
				Name: "payments", PrimaryKey: []string{"payment_id"},
				Columns:     []schema.Column{pk("payment_id", i), c("order_id", i), c("amount", n), c("paid_at", schema.TypeDatetime)},
				ForeignKeys: []schema.ForeignKey{fk("payments_order_fk", []string{"order_id"}, "orders", []string{"order_id"})},
			},
		}},
		{Name: "sales", Tables: []schema.Table{{Name: "orders_archive", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{pk("order_id", i), c("status", s)}}}},
	}}
	for si := range snap.Schemas {
		for ti := range snap.Schemas[si].Tables {
			t := &snap.Schemas[si].Tables[ti]
			for _, k := range t.ForeignKeys {
				ref := snap.Table(k.RefSchema, k.RefTable)
				ref.ReferencedBy = append(ref.ReferencedBy, schema.Reference{Schema: snap.Schemas[si].Name, Table: t.Name, Name: k.Name, Columns: k.Columns, RefColumns: k.RefColumns})
			}
		}
	}
	return snap
}

// linksOf adapts the relationship graph, as the mappings service does.
func linksOf(snap *schema.Snapshot) Links {
	g := relations.Build(snap, nil)
	return func(from mapping.TableRef) []Link {
		var out []Link
		for _, h := range g.Hops(from) {
			out = append(out, Link{Kind: h.Kind, Table: h.To, Join: h.Join, Virtual: h.Virtual, Cardinality: h.Cardinality, Direction: h.Direction})
		}
		for _, j := range relations.Junctions(snap, from) {
			out = append(out, Link{Kind: mapping.RelationManyToMany, Table: j.Table, Via: &mapping.Via{Table: j.Via, ParentJoin: j.ParentJoin, ChildJoin: j.ChildJoin, Columns: j.PayloadColumns}})
		}
		return out
	}
}

func allTables(snap *schema.Snapshot) []mapping.TableRef {
	var out []mapping.TableRef
	for _, s := range snap.Schemas {
		for _, t := range s.Tables {
			out = append(out, mapping.TableRef{Schema: s.Name, Name: t.Name})
		}
	}
	return out
}

func arr(id, name string, item docschema.DocField) docschema.DocField {
	item.Name = "[]"
	return docschema.DocField{ID: id, Name: name, Type: docschema.Array, Items: &item}
}

// orderDesign is the customer's imported order document, nothing bound.
func orderDesign() *mapping.Spec {
	spec := &mapping.Spec{Mode: mapping.ModeComposed, DocSchema: docschema.Schema{Root: fld(docschema.RootID, "$", docschema.Object,
		fld("f_oid", "orderId", docschema.Integer), fld("f_status", "status", docschema.String),
		fld("f_date", "orderDate", docschema.Datetime), fld("f_total", "total", docschema.Number),
		fld("f_cust", "customer", docschema.Object, fld("f_cname", "name", docschema.String), fld("f_email", "email", docschema.String)),
		fld("f_ship", "shipping", docschema.Object, fld("f_street", "street", docschema.String), fld("f_city", "city", docschema.String), fld("f_zip", "zip", docschema.String)),
		arr("f_items", "items", fld("f_item", "[]", docschema.Object,
			fld("f_line", "lineNo", docschema.Integer), fld("f_qty", "quantity", docschema.Integer), fld("f_price", "unitPrice", docschema.Number),
			fld("f_prod", "product", docschema.Object, fld("f_sku", "sku", docschema.String), fld("f_pname", "name", docschema.String)),
		)),
		fld("f_pay", "latestPayment", docschema.Object, fld("f_amt", "amount", docschema.Number), fld("f_paid", "paidAt", docschema.Datetime)),
		fld("f_loy", "loyalty", docschema.Object, fld("f_tier", "tier", docschema.String), fld("f_pts", "points", docschema.Integer)),
	)}}
	spec.Normalize()
	return spec
}

var ordersRef = mapping.TableRef{Schema: "public", Name: "orders"}

func suggest(t *testing.T, spec *mapping.Spec, driving mapping.TableRef, root bool) (Suggestions, map[string]BindingSuggestion) {
	t.Helper()
	snap := shopSnapshot()
	res := SuggestBindings(SuggestInput{
		Spec: spec, Driving: driving, Root: root, Tables: allTables(snap), Links: linksOf(snap),
		Table: func(r mapping.TableRef) *schema.Table { return snap.Table(r.Schema, r.Name) },
	})
	by := map[string]BindingSuggestion{}
	for _, s := range res.Suggestions {
		by[s.NodeID] = s
	}
	return res, by
}

func TestSuggestBindings_AC1_DrivingTable(t *testing.T) {
	res, _ := suggest(t, orderDesign(), ordersRef, true)
	require.NotEmpty(t, res.Root)
	require.Equal(t, ordersRef, res.Root[0].Table)
	require.GreaterOrEqual(t, res.Root[0].Score, bandHigh)
	require.Equal(t, []string{"fields 4/4"}, res.Root[0].Reasons)
	archive := -1
	for i, r := range res.Root {
		if r.Table.Name == "orders_archive" {
			archive = i
		}
	}
	require.Positive(t, archive, "orders_archive ranks below orders")
}

func TestSuggestBindings_AC2_Chain(t *testing.T) {
	_, by := suggest(t, orderDesign(), ordersRef, false)
	items := by["f_items"]
	require.Equal(t, ConfidenceHigh, items.Confidence)
	require.Equal(t, "order_items", items.Candidate.Table.Name)
	require.Equal(t, mapping.EmbedOneToMany, items.Candidate.Kind)
	require.Equal(t, []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, items.Candidate.Join)
	require.Equal(t, "items[]", items.Path)
	require.Contains(t, items.Candidate.Reasons, "fields 3/3")

	prod := by["f_prod"] // scored in order_items' scope, as if items were accepted
	require.Equal(t, ConfidenceHigh, prod.Confidence)
	require.Equal(t, "products", prod.Candidate.Table.Name)
	require.Equal(t, mapping.EmbedManyToOne, prod.Candidate.Kind)
	require.Equal(t, []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, prod.Candidate.Join)
	require.Equal(t, "items[].product", prod.Path)

	cust := by["f_cust"]
	require.Equal(t, ConfidenceHigh, cust.Confidence)
	require.Equal(t, "customers", cust.Candidate.Table.Name)
	require.Empty(t, cust.Candidate.Adapter)

	// Only the asked-for node.
	one := SuggestBindings(SuggestInput{Spec: orderDesign(), Driving: ordersRef, NodeID: "f_prod", Links: linksOf(shopSnapshot()), Table: func(r mapping.TableRef) *schema.Table { return shopSnapshot().Table(r.Schema, r.Name) }})
	require.Len(t, one.Suggestions, 1)
	require.Equal(t, "f_prod", one.Suggestions[0].NodeID)
}

func TestSuggestBindings_AC3_ScalarJunction(t *testing.T) {
	spec := &mapping.Spec{Mode: mapping.ModeComposed, DocSchema: docschema.Schema{Root: fld(docschema.RootID, "$", docschema.Object,
		fld("p_sku", "sku", docschema.String), arr("p_tags", "tags", fld("p_tag", "[]", docschema.String)),
	)}}
	_, by := suggest(t, spec, mapping.TableRef{Schema: "public", Name: "products"}, false)
	tags := by["p_tags"]
	require.NotEqual(t, ConfidenceUnresolved, tags.Confidence)
	require.Equal(t, mapping.RelationManyToMany, tags.Candidate.Kind)
	require.Equal(t, "tags", tags.Candidate.Table.Name)
	require.Equal(t, "product_tags", tags.Candidate.Via.Table.Name)
	require.Equal(t, "scalars", tags.Candidate.Shape)
	require.Equal(t, "name", tags.Candidate.ValueColumn, "not the key tag_id")
}

func TestSuggestBindings_AC4_MapFromParent(t *testing.T) {
	_, by := suggest(t, orderDesign(), ordersRef, false)
	ship := by["f_ship"]
	require.True(t, ship.Candidate.FromParent)
	require.NotEqual(t, ConfidenceUnresolved, ship.Confidence)
	require.Contains(t, ship.Candidate.Reasons, "fields 3/3 from orders")
}

func TestSuggestBindings_AC5_Adapter(t *testing.T) {
	_, by := suggest(t, orderDesign(), ordersRef, false)
	pay := by["f_pay"]
	require.NotEqual(t, ConfidenceUnresolved, pay.Confidence)
	require.Equal(t, "payments", pay.Candidate.Table.Name)
	require.Equal(t, mapping.EmbedOneToMany, pay.Candidate.Kind)
	require.Equal(t, docschema.AdapterFirst, pay.Candidate.Adapter)
	require.Contains(t, pay.Candidate.Reasons, "object ← one-to-many: first row")
}

func TestSuggestBindings_AC7_Unresolved(t *testing.T) {
	_, by := suggest(t, orderDesign(), ordersRef, false)
	loy := by["f_loy"]
	require.Equal(t, ConfidenceUnresolved, loy.Confidence)
	require.Less(t, loy.Candidate.Score, bandMedium)
}

func TestSuggestBindings_BoundNodesKeepTheirScope(t *testing.T) {
	spec := orderDesign()
	// items is already bound (by the user) to order_items: it gets no suggestion, and product
	// is scored in its scope.
	spec.Relations = []mapping.Relation{{ID: "rel_items", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "public", Name: "order_items"}, Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetFieldID: "f_items"}}
	spec.Normalize()
	_, by := suggest(t, spec, ordersRef, false)
	require.NotContains(t, by, "f_items")
	require.Equal(t, "products", by["f_prod"].Candidate.Table.Name)
}

func TestSuggestBindings_Deterministic(t *testing.T) {
	a, _ := suggest(t, orderDesign(), ordersRef, true)
	b, _ := suggest(t, orderDesign(), ordersRef, true)
	require.Equal(t, a, b)
}

// accept applies suggestions of the given bands top-down, as the composer does with its bind
// operation: a relation under the node's scope, and the node's binding.
func accept(spec *mapping.Spec, sugs []BindingSuggestion, bands ...string) {
	ok := map[string]bool{}
	for _, b := range bands {
		ok[b] = true
	}
	for _, s := range sugs {
		c := s.Candidate
		if !ok[s.Confidence] || c.FromParent || c.Table == nil {
			continue
		}
		var parent *string
		if p := mapping.FieldScopes(spec)[s.NodeID]; p != "" {
			parent = &p
		}
		r := mapping.Relation{ID: "rel_" + s.NodeID, ParentID: parent, Kind: c.Kind, Table: *c.Table, Join: c.Join, Via: c.Via, TargetFieldID: s.NodeID, Shape: c.Shape, ValueColumn: c.ValueColumn}
		spec.Relations = append(spec.Relations, r)
		f := spec.DocSchema.Find(s.NodeID)
		f.Binding = &docschema.Binding{RelationID: r.ID, Adapter: c.Adapter}
		spec.Normalize()
	}
}

func TestApplySuggestions_AC6_EndToEnd(t *testing.T) {
	snap := shopSnapshot()
	spec := orderDesign()
	res, _ := suggest(t, spec, ordersRef, false)
	accept(spec, res.Suggestions, ConfidenceHigh)
	require.Len(t, spec.Relations, 4) // customer, items, items[].product, latestPayment
	spec.Key.Template = "order::{order_id}"
	for _, is := range mapping.Validate(spec, mapping.Context{Table: snap.Table("public", "orders"), Related: func(s, n string) *schema.Table { return snap.Table(s, n) }}) {
		require.NotEqual(t, mapping.SeverityError, is.Severity, "the accepted bindings are valid: %+v", is)
	}

	out, err := RunAll(spec, snap.Table("public", "orders"), func(s, n string) *schema.Table { return snap.Table(s, n) })
	require.NoError(t, err)
	proposed := map[string]bool{}
	for _, m := range out.FieldMappings {
		if m.State != mapping.StateRejected {
			proposed[m.TargetFieldID] = true
		}
	}
	// Every field of the design gets a mapping or a suggestion, except loyalty's two (no table
	// holds them: unresolved): 16 of 18.
	var missing []string
	for _, l := range Leaves(&spec.DocSchema) {
		if !proposed[l.FieldID] {
			missing = append(missing, l.Path)
		}
	}
	require.ElementsMatch(t, []string{"loyalty.tier", "loyalty.points"}, missing)
}

// TestSuggestBindings_AC7_Group: the customer's order_detail table (no orders table) and their
// order document: grouping by order_id is proposed for products[] (OC-063).
func TestSuggestBindings_AC7_Group(t *testing.T) {
	c := func(name, jt string) schema.Column { return schema.Column{Name: name, JSONType: jt, UDTName: jt} }
	snap := &schema.Snapshot{Schemas: []schema.Schema{{Name: "public", Tables: []schema.Table{
		{Name: "products", PrimaryKey: []string{"product_id"}, Columns: []schema.Column{{Name: "product_id", JSONType: schema.TypeInteger, IsPrimaryKey: true}, c("sku", schema.TypeString), c("name", schema.TypeString)}},
		{
			Name: "order_detail", PrimaryKey: []string{"order_id", "line_no"},
			Columns:     []schema.Column{c("type", schema.TypeString), c("order_id", schema.TypeInteger), c("line_no", schema.TypeInteger), c("product_id", schema.TypeInteger), c("quantity", schema.TypeInteger)},
			ForeignKeys: []schema.ForeignKey{{Name: "od_product_fk", Columns: []string{"product_id"}, RefSchema: "public", RefTable: "products", RefColumns: []string{"product_id"}}},
		},
	}}}}
	snap.Table("public", "products").ReferencedBy = []schema.Reference{{Schema: "public", Table: "order_detail", Name: "od_product_fk", Columns: []string{"product_id"}, RefColumns: []string{"product_id"}}}
	ds, _, err := docschema.Import(docschema.KindJSONSample, `{"type":"order_detail","orderId":1,"products":[{"lineNo":1,"productId":2,"quantity":3},{"lineNo":2,"productId":4,"quantity":2}]}`, "")
	require.NoError(t, err)
	driving := mapping.TableRef{Schema: "public", Name: "order_detail"}
	spec := &mapping.Spec{Mode: mapping.ModeComposed, Root: &mapping.Root{Table: driving}, DocSchema: ds}
	spec.Normalize()
	res := SuggestBindings(SuggestInput{Spec: spec, Driving: driving, Links: linksOf(snap), Table: func(r mapping.TableRef) *schema.Table { return snap.Table(r.Schema, r.Name) }})
	require.Len(t, res.Suggestions, 1)
	s := res.Suggestions[0]
	require.Equal(t, "products[]", s.Path)
	require.Equal(t, ConfidenceHigh, s.Confidence)
	require.True(t, s.Candidate.Group)
	require.Equal(t, []string{"order_id"}, s.Candidate.GroupBy)
	require.Equal(t, []mapping.OrderColumn{{Column: "line_no"}}, s.Candidate.OrderBy)
	require.Contains(t, s.Candidate.Reasons, "one document per distinct order_id")
	require.Equal(t, "products", s.Alternatives[0].Table.Name, "the products table is the runner-up")

	// Accepted and auto-mapped: all 5 fields are mapped from order_detail.
	spec.Root.GroupBy = s.Candidate.GroupBy
	f := spec.DocSchema.Find(s.NodeID)
	spec.Relations = []mapping.Relation{{ID: "rel_lines", Group: true, TargetFieldID: s.NodeID, OrderBy: s.Candidate.OrderBy}}
	f.Binding = &docschema.Binding{RelationID: "rel_lines"}
	spec.Normalize()
	out, err := RunAll(spec, snap.Table("public", "order_detail"), func(sc, n string) *schema.Table { return snap.Table(sc, n) })
	require.NoError(t, err)
	mapped := map[string]string{}
	for _, m := range out.FieldMappings {
		if m.State != mapping.StateRejected {
			mapped[spec.DocSchema.Paths()[m.TargetFieldID]] = m.Sources[0].Column
		}
	}
	require.Equal(t, map[string]string{"type": "type", "orderId": "order_id", "products[].lineNo": "line_no", "products[].productId": "product_id", "products[].quantity": "quantity"}, mapped)
}
