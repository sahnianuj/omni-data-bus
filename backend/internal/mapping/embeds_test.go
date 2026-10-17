package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

func est(n int64) *int64 { return &n }

// shopSnap: customer —address_id→ address; orders —customer_id→ customer; and a composite
// relation shipment (order_id, line_no) → order_line.
func shopSnap() *schema.Snapshot {
	col := func(name, udt, jt string) schema.Column { return schema.Column{Name: name, UDTName: udt, JSONType: jt} }
	return &schema.Snapshot{Schemas: []schema.Schema{{Name: "shop", Tables: []schema.Table{
		{
			Name: "customer", RowEstimate: est(2000), SizeBytes: 2000 * 200, PrimaryKey: []string{"customer_id"},
			Columns:      []schema.Column{col("customer_id", "int4", schema.TypeInteger), col("name", "text", schema.TypeString), col("address_id", "int4", schema.TypeInteger)},
			ForeignKeys:  []schema.ForeignKey{{Name: "customer_address_fk", Columns: []string{"address_id"}, RefSchema: "shop", RefTable: "address", RefColumns: []string{"address_id"}}},
			ReferencedBy: []schema.Reference{{Schema: "shop", Table: "orders", Name: "orders_customer_fk", Columns: []string{"customer_id"}, RefColumns: []string{"customer_id"}}},
		},
		{
			Name: "address", RowEstimate: est(400), SizeBytes: 400 * 120, PrimaryKey: []string{"address_id"},
			Columns: []schema.Column{col("address_id", "int4", schema.TypeInteger), col("city", "text", schema.TypeString), col("zip", "text", schema.TypeString)},
		},
		{
			Name: "orders", RowEstimate: est(1_200_000), SizeBytes: 1_200_000 * 100, PrimaryKey: []string{"order_id"},
			Columns: []schema.Column{col("order_id", "int8", schema.TypeInteger), col("customer_id", "int4", schema.TypeInteger), col("order_date", "date", schema.TypeString), col("total", "numeric", schema.TypeNumber)},
		},
		{
			Name: "order_line", RowEstimate: est(100), PrimaryKey: []string{"order_id", "line_no"},
			Columns:      []schema.Column{col("order_id", "int8", schema.TypeInteger), col("line_no", "int4", schema.TypeInteger), col("sku", "text", schema.TypeString)},
			ReferencedBy: []schema.Reference{{Schema: "shop", Table: "shipment", Name: "shipment_line_fk", Columns: []string{"order_id", "line_no"}, RefColumns: []string{"order_id", "line_no"}}},
		},
		{
			Name: "shipment", RowEstimate: est(80), PrimaryKey: []string{"shipment_id"},
			Columns:     []schema.Column{col("shipment_id", "int8", schema.TypeInteger), col("order_id", "int8", schema.TypeInteger), col("line_no", "int4", schema.TypeInteger), col("carrier", "text", schema.TypeString)},
			ForeignKeys: []schema.ForeignKey{{Name: "shipment_line_fk", Columns: []string{"order_id", "line_no"}, RefSchema: "shop", RefTable: "order_line", RefColumns: []string{"order_id", "line_no"}}},
		},
	}}}}
}

func TestEmbedCandidates_AC1_ParentsAndChildren(t *testing.T) {
	snap := shopSnap()
	got := Candidates(snap, snap.Table("shop", "customer"), nil)
	require.Len(t, got, 2)
	require.Equal(t, EmbedManyToOne, got[0].Kind, "parents (↗) first")
	require.Equal(t, TableRef{"shop", "address"}, got[0].Table)
	require.Equal(t, []JoinPair{{Parent: "address_id", Child: "address_id"}}, got[0].Join)
	require.EqualValues(t, 400, *got[0].RowEstimate)
	require.Equal(t, EmbedOneToMany, got[1].Kind)
	require.Equal(t, TableRef{"shop", "orders"}, got[1].Table)
	require.Equal(t, []JoinPair{{Parent: "customer_id", Child: "customer_id"}}, got[1].Join)
	require.InDelta(t, 600, got[1].RowsPerParent, 0.001, "1.2M orders over 2,000 customers")

	// An existing embed is marked.
	s := Spec{Relations: []Relation{{ID: "emb_1", Kind: EmbedOneToMany, Table: TableRef{"shop", "orders"}, Join: got[1].Join}}}
	got = Candidates(snap, snap.Table("shop", "customer"), &s)
	require.Empty(t, got[0].EmbedID)
	require.Equal(t, "emb_1", got[1].EmbedID)
}

func TestEmbedCandidates_AC1_CompositeFK(t *testing.T) {
	snap := shopSnap()
	down := Candidates(snap, snap.Table("shop", "order_line"), nil)
	require.Len(t, down, 1)
	require.Equal(t, EmbedOneToMany, down[0].Kind)
	require.Equal(t, []JoinPair{{"order_id", "order_id"}, {"line_no", "line_no"}}, down[0].Join, "column pairs in constraint order")
	up := Candidates(snap, snap.Table("shop", "shipment"), nil)
	require.Len(t, up, 1)
	require.Equal(t, EmbedManyToOne, up[0].Kind)
	require.Equal(t, "shipment_line_fk", up[0].Constraint)
	require.Equal(t, []JoinPair{{"order_id", "order_id"}, {"line_no", "line_no"}}, up[0].Join)
}

func TestEmbedEstimates_AC6_Warnings(t *testing.T) {
	snap := shopSnap()
	customer, orders := snap.Table("shop", "customer"), snap.Table("shop", "orders")
	e := Relation{ID: "emb_2", Kind: EmbedOneToMany, Table: TableRef{"shop", "orders"}, TargetFieldID: "f_orders", Limit: 100}
	issues := EmbedEstimates(e, customer, orders, "orders")
	require.Len(t, issues, 1)
	require.Equal(t, CodeEmbedCardinality, issues[0].Code)
	require.Equal(t, SeverityWarning, issues[0].Severity, "a warning, never an error")
	require.Contains(t, issues[0].Message, "about 600 rows per customer row, more than the limit of 100")

	// Wide child rows with a high limit: the estimated document is over 10 MB.
	wide := *orders
	wide.SizeBytes = 1_200_000 * 50_000
	e.Limit = 1000
	issues = EmbedEstimates(e, customer, &wide, "orders")
	require.Equal(t, []string{"warning:" + CodeEmbedDocSize + "@orders"}, codesOf(issues))
	require.Contains(t, issues[0].Message, "about 28.6 MB")

	// A small child table raises nothing; many-to-one never does.
	e.Limit = 1000
	require.Empty(t, EmbedEstimates(e, customer, snap.Table("shop", "address"), "orders"))
	require.Empty(t, EmbedEstimates(Relation{Kind: EmbedManyToOne}, customer, orders, "x"))

	// They reach validation (OC-022) as warnings.
	s := embedSpec()
	s.Relations[1].Limit = 100
	var warns []string
	for _, i := range ValidateRelations(&s, customer, snapRel(snap)) {
		require.NotEqual(t, SeverityError, i.Severity, i.Message)
		warns = append(warns, i.Code)
	}
	require.Equal(t, []string{CodeEmbedCardinality, CodeRelationNoIndex}, warns, "orders.customer_id has no index in the fixture (OC-042)")
}

func snapRel(snap *schema.Snapshot) RelatedTables {
	return func(s, t string) *schema.Table { return snap.Table(s, t) }
}

// embedSpec embeds address (object) and orders (array of objects) into customer.
func embedSpec() Spec {
	s := Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			f("f_id", "customerId", docschema.Integer, true),
			{ID: "f_addr", Name: "address", Type: docschema.Object, Children: []docschema.DocField{f("f_city", "city", docschema.String, false)}},
			{ID: "f_orders", Name: "orders", Type: docschema.Array, Items: &docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
				f("f_total", "total", docschema.Number, false),
			}}},
		}}},
		Key: KeySpec{Template: "customer::{customer_id}"},
		Relations: []Relation{
			{ID: "emb_1", Kind: EmbedManyToOne, Table: TableRef{"shop", "address"}, Join: []JoinPair{{"address_id", "address_id"}}, TargetFieldID: "f_addr"},
			{
				ID: "emb_2", Kind: EmbedOneToMany, Table: TableRef{"shop", "orders"}, Join: []JoinPair{{"customer_id", "customer_id"}}, TargetFieldID: "f_orders",
				OrderBy: []OrderColumn{{Column: "order_date", Desc: true}}, Limit: 1000,
			},
		},
		FieldMappings: []FieldMapping{
			{ID: "m1", TargetFieldID: "f_id", Sources: []SourceRef{{Column: "customer_id"}}, Origin: OriginAuto, State: StateActive},
			{ID: "m2", TargetFieldID: "f_city", Sources: []SourceRef{{Column: "city", Relation: "emb_1"}}, Origin: OriginAuto, State: StateActive},
			{ID: "m3", TargetFieldID: "f_total", Sources: []SourceRef{{Column: "total", Relation: "emb_2"}}, Origin: OriginManual, State: StateActive},
		},
	}
	s.Normalize()
	return s
}

func TestEmbeds_ScopedAndFields(t *testing.T) {
	s := embedSpec()
	require.Equal(t, map[string]string{"f_city": "emb_1", "f_total": "emb_2"}, RelationFields(&s))
	sc := Scoped(&s, s.Relations[1])
	require.Len(t, sc.DocSchema.Root.Children, 1)
	require.Equal(t, "total", sc.DocSchema.Root.Children[0].Name)
	require.Equal(t, []FieldMapping{{ID: "m3", TargetFieldID: "f_total", Sources: []SourceRef{{Column: "total"}}, Transforms: []Step{}, Origin: OriginManual, State: StateActive}}, sc.FieldMappings)
	require.Equal(t, "emb_2", s.Relations[1].ID)
	require.Equal(t, DefaultEmbedLimit, Relation{}.EffectiveLimit())
}

// OC-157 AC2: a child whose foreign key is unique (here its primary key) is one-to-one: an object
// both ways. Candidates are grouped References first, then Referenced by.
func TestCandidates_OC157_AC2(t *testing.T) {
	snap := shopSnap()
	sc := &snap.Schemas[0]
	sc.Tables = append(sc.Tables, schema.Table{
		Name: "customer_profile", RowEstimate: est(1900), PrimaryKey: []string{"customer_id"},
		Columns:     []schema.Column{{Name: "customer_id", UDTName: "int4", JSONType: schema.TypeInteger}, {Name: "bio", UDTName: "text", JSONType: schema.TypeString}},
		ForeignKeys: []schema.ForeignKey{{Name: "profile_customer_fk", Columns: []string{"customer_id"}, RefSchema: "shop", RefTable: "customer", RefColumns: []string{"customer_id"}}},
	})
	cust := snap.Table("shop", "customer")
	cust.ReferencedBy = append(cust.ReferencedBy, schema.Reference{Schema: "shop", Table: "customer_profile", Name: "profile_customer_fk", Columns: []string{"customer_id"}, RefColumns: []string{"customer_id"}})

	got := Candidates(snap, cust, nil)
	require.Len(t, got, 3)
	require.Equal(t, DirectionReferences, got[0].Direction)
	require.Equal(t, CardinalityManyToOne, got[0].Cardinality)
	require.Equal(t, "customer_profile", got[1].Table.Name)
	require.Equal(t, DirectionReferencedBy, got[1].Direction)
	require.Equal(t, EmbedManyToOne, got[1].Kind, "an object, not an array of one")
	require.Equal(t, CardinalityOneToOne, got[1].Cardinality)
	require.InDelta(t, 1, got[1].RowsPerParent, 0.001)
	require.Equal(t, "orders", got[2].Table.Name)
	require.Equal(t, EmbedOneToMany, got[2].Kind)
	require.Equal(t, CardinalityOneToMany, got[2].Cardinality)

	back := Candidates(snap, snap.Table("shop", "customer_profile"), nil)
	require.Len(t, back, 1)
	require.Equal(t, DirectionReferences, back[0].Direction)
	require.Equal(t, CardinalityOneToOne, back[0].Cardinality)

	// AC5: embedded as an array before OC-157, it still counts as embedded.
	s := Spec{Relations: []Relation{{ID: "emb_old", Kind: EmbedOneToMany, Table: TableRef{"shop", "customer_profile"}, Join: got[1].Join}}}
	require.Equal(t, "emb_old", Candidates(snap, cust, &s)[1].EmbedID)
}
