package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

func strp(s string) *string { return &s }

func col(rel int64, num int, name, typ string, notNull bool) rawColumn {
	return rawColumn{RelOID: rel, Num: num, Name: name, Display: typ, Declared: baseType(typ), Effective: baseType(typ), Typmod: -1, NotNull: notNull}
}

// fixtureCatalog is a small shop: address ← customer ← orders ← "Order Details" → shipment,
// plus a view, a never-analyzed table and a partitioned table with two partitions.
func fixtureCatalog() rawCatalog {
	status := rawColumn{RelOID: 3, Num: 3, Name: "status", Display: "shop.order_status", Declared: PGType{"order_status", "e"}, Effective: PGType{"order_status", "e"}, EnumOID: 900, Typmod: -1, NotNull: true, Default: strp("'pending'::shop.order_status")}
	tags := rawColumn{RelOID: 2, Num: 3, Name: "tags", Display: "text[]", Declared: baseType("_text"), Effective: baseType("_text"), Elem: baseType("text"), Typmod: -1}
	price := rawColumn{RelOID: 3, Num: 4, Name: "total", Display: "numeric(12,2)", Declared: baseType("numeric"), Effective: baseType("numeric"), Typmod: (12<<16 | 2) + 4, NotNull: true}
	return rawCatalog{
		Database: "shop", ServerVersion: "16.3",
		Relations: []rawRelation{
			{OID: 1, Schema: "shop", Name: "address", Kind: "r", RelTuples: 400, Size: 81920, Readable: true},
			{OID: 2, Schema: "shop", Name: "customer", Kind: "r", RelTuples: 2000, Size: 30932992, Comment: strp("People who buy"), Readable: true},
			{OID: 3, Schema: "shop", Name: "orders", Kind: "r", RelTuples: 10000, Size: 1_000_000, Readable: true},
			{OID: 4, Schema: "shop", Name: "Order Details", Kind: "r", RelTuples: 30000, Size: 2_000_000, Readable: true},
			{OID: 5, Schema: "shop", Name: "shipment", Kind: "r", RelTuples: -1, Size: 8192, Readable: true},
			{OID: 6, Schema: "shop", Name: "v_active_customers", Kind: "v", RelTuples: -1, Readable: true},
			{OID: 7, Schema: "billing", Name: "invoice", Kind: "r", RelTuples: 500, Size: 65536, Readable: false},
			{OID: 8, Schema: "shop", Name: "events", Kind: "p", RelTuples: -1, Readable: true},
			{OID: 9, Schema: "shop", Name: "events_2024", Kind: "r", RelTuples: 700, Size: 1000, IsPartition: true, Parent: 8, Readable: true},
			{OID: 10, Schema: "shop", Name: "events_2025", Kind: "r", RelTuples: 300, Size: 500, IsPartition: true, Parent: 8, Readable: true},
		},
		Columns: []rawColumn{
			col(1, 1, "address_id", "int4", true), col(1, 2, "city", "text", true),
			// Deliberately out of ordinal order: assembly sorts by ordinal.
			col(2, 2, "email_address", "citext", true), col(2, 1, "customer_id", "int4", true), tags, col(2, 4, "address_id", "int4", false),
			col(3, 1, "order_id", "int8", true), col(3, 2, "customer_id", "int4", true), status, price,
			col(4, 1, "order_id", "int8", true), col(4, 2, "line_no", "int2", true), col(4, 3, "quantity", "int4", true),
			col(5, 1, "shipment_id", "int8", true), col(5, 2, "line_no", "int2", true), col(5, 3, "order_id", "int8", true),
			col(5, 4, "search", "tsvector", false),
			col(6, 1, "customer_id", "int4", false),
			col(7, 1, "invoice_id", "int8", true),
			col(8, 1, "event_id", "int8", true),
		},
		Constraints: []rawConstraint{
			{RelOID: 1, Name: "address_pkey", Type: "p", Columns: []string{"address_id"}},
			{RelOID: 2, Name: "customer_pkey", Type: "p", Columns: []string{"customer_id"}},
			{RelOID: 2, Name: "customer_email_key", Type: "u", Columns: []string{"email_address"}},
			{RelOID: 2, Name: "customer_address_fk", Type: "f", Columns: []string{"address_id"}, RefSchema: "shop", RefTable: "address", RefColumns: []string{"address_id"}},
			{RelOID: 3, Name: "orders_pkey", Type: "p", Columns: []string{"order_id"}},
			{RelOID: 3, Name: "orders_customer_fk", Type: "f", Columns: []string{"customer_id"}, RefSchema: "shop", RefTable: "customer", RefColumns: []string{"customer_id"}},
			{RelOID: 4, Name: "Order Details_pkey", Type: "p", Columns: []string{"order_id", "line_no"}},
			{RelOID: 4, Name: "od_order_fk", Type: "f", Columns: []string{"order_id"}, RefSchema: "shop", RefTable: "orders", RefColumns: []string{"order_id"}},
			// Composite FK declared (line_no, order_id): the declared order must survive.
			{RelOID: 5, Name: "shipment_line_fk", Type: "f", Columns: []string{"line_no", "order_id"}, RefSchema: "shop", RefTable: "Order Details", RefColumns: []string{"line_no", "order_id"}},
			{RelOID: 7, Name: "invoice_pkey", Type: "p", Columns: []string{"invoice_id"}},
		},
		Indexes: []rawIndex{
			{RelOID: 2, Name: "customer_pkey", Unique: true, Primary: true, Columns: []string{"customer_id"}},
			{RelOID: 2, Name: "customer_email_key", Unique: true, Columns: []string{"email_address"}},
			{RelOID: 3, Name: "orders_by_day", Columns: []string{"expr"}},
		},
		Enums: map[int64][]string{900: {"pending", "paid", "shipped", "cancelled"}},
	}
}

func TestAssembleSnapshot_AC1_FromRawRows(t *testing.T) {
	snap := AssembleSnapshot(fixtureCatalog(), source.DiscoverOptions{})
	require.Equal(t, "shop", snap.Database)
	require.Equal(t, "16.3", snap.ServerVersion)
	require.Equal(t, []string{"billing", "shop"}, []string{snap.Schemas[0].Name, snap.Schemas[1].Name})

	var names []string
	for _, tb := range snap.Schemas[1].Tables {
		names = append(names, tb.Name)
	}
	require.Equal(t, []string{"Order Details", "address", "customer", "events", "orders", "shipment", "v_active_customers"}, names,
		"sorted by name; partitions hidden under their parent")

	cust := snap.Table("shop", "customer")
	require.NotNil(t, cust)
	require.Equal(t, schema.KindTable, cust.Kind)
	require.Equal(t, int64(2000), *cust.RowEstimate)
	require.Equal(t, int64(30932992), cust.SizeBytes)
	require.Equal(t, "People who buy", cust.Comment)
	require.Equal(t, []string{"customer_id"}, cust.PrimaryKey)
	require.Equal(t, [][]string{{"email_address"}}, cust.UniqueConstraints)
	require.Equal(t, []string{"customer_id", "email_address", "tags", "address_id"}, columnNames(cust), "columns by ordinal")

	id := cust.Column("customer_id")
	require.True(t, id.IsPrimaryKey)
	require.True(t, id.IsUnique)
	require.False(t, id.Nullable)
	require.Equal(t, schema.TypeInteger, id.JSONType)
	require.Equal(t, 32, *id.NumericPrecision)
	email := cust.Column("email_address")
	require.True(t, email.IsUnique)
	require.False(t, email.IsPrimaryKey)
	tags := cust.Column("tags")
	require.True(t, tags.IsArray)
	require.Equal(t, schema.TypeArray, tags.JSONType)
	require.Equal(t, schema.TypeString, tags.ElementType)
	require.True(t, cust.Column("address_id").Nullable)
	require.Len(t, cust.ForeignKeys, 1)
	require.Equal(t, schema.ForeignKey{Name: "customer_address_fk", Columns: []string{"address_id"}, RefSchema: "shop", RefTable: "address", RefColumns: []string{"address_id"}}, cust.ForeignKeys[0])
	require.Len(t, cust.Indexes, 2)

	orders := snap.Table("shop", "orders")
	st := orders.Column("status")
	require.Equal(t, []string{"pending", "paid", "shipped", "cancelled"}, st.EnumValues)
	require.Equal(t, "'pending'::shop.order_status", *st.Default)
	total := orders.Column("total")
	require.Equal(t, schema.TypeNumber, total.JSONType)
	require.Equal(t, 12, *total.NumericPrecision)
	require.Equal(t, 2, *total.NumericScale)
	require.Equal(t, []string{"expr"}, orders.Indexes[0].Columns, "expression index parts")

	view := snap.Table("shop", "v_active_customers")
	require.Equal(t, schema.KindView, view.Kind)
	require.Nil(t, view.RowEstimate)
	require.False(t, view.StatsStale, "views have no statistics, which isn't staleness")

	events := snap.Table("shop", "events")
	require.Equal(t, schema.KindPartitionedTable, events.Kind)
	require.Equal(t, int64(1000), *events.RowEstimate, "sum of partitions")
	require.Equal(t, int64(1500), events.SizeBytes)
	require.ElementsMatch(t, []string{"events_2024", "events_2025"}, events.Partitions)

	inv := snap.Table("billing", "invoice")
	require.False(t, inv.Readable)
	require.True(t, cust.Readable)

	require.Equal(t, schema.WarnUnsupportedType, snap.Table("shop", "shipment").Column("search").Warning)

	// Empty lists are [] (not null) in JSON, so the UI never special-cases them.
	addr := snap.Table("shop", "address")
	require.NotNil(t, addr.ForeignKeys)
	require.NotNil(t, addr.Indexes)
	require.NotNil(t, addr.UniqueConstraints)
}

func columnNames(t *schema.Table) []string {
	out := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		out = append(out, c.Name)
	}
	return out
}

func TestAssembleSnapshot_AC1_CompositeFKOrderPreserved(t *testing.T) {
	snap := AssembleSnapshot(fixtureCatalog(), source.DiscoverOptions{})
	ship := snap.Table("shop", "shipment")
	require.Len(t, ship.ForeignKeys, 1)
	require.Equal(t, []string{"line_no", "order_id"}, ship.ForeignKeys[0].Columns)
	require.Equal(t, []string{"line_no", "order_id"}, ship.ForeignKeys[0].RefColumns)
	require.Equal(t, []string{"order_id", "line_no"}, snap.Table("shop", "Order Details").PrimaryKey)
}

func TestAssembleSnapshot_AC1_ReferencedByComputed(t *testing.T) {
	snap := AssembleSnapshot(fixtureCatalog(), source.DiscoverOptions{})
	require.Equal(t, []schema.Reference{{Schema: "shop", Table: "customer", Name: "customer_address_fk", Columns: []string{"address_id"}, RefColumns: []string{"address_id"}}},
		snap.Table("shop", "address").ReferencedBy)
	require.Equal(t, []schema.Reference{{Schema: "shop", Table: "orders", Name: "orders_customer_fk", Columns: []string{"customer_id"}, RefColumns: []string{"customer_id"}}},
		snap.Table("shop", "customer").ReferencedBy)
	od := snap.Table("shop", "Order Details").ReferencedBy
	require.Len(t, od, 1)
	require.Equal(t, "shipment", od[0].Table)
	require.Empty(t, snap.Table("shop", "shipment").ReferencedBy)
}

func TestAssembleSnapshot_AC4_NegativeReltuples(t *testing.T) {
	snap := AssembleSnapshot(fixtureCatalog(), source.DiscoverOptions{})
	ship := snap.Table("shop", "shipment")
	require.Nil(t, ship.RowEstimate)
	require.True(t, ship.StatsStale)

	raw := fixtureCatalog()
	raw.Relations[9].RelTuples = -1 // one partition never analyzed
	events := AssembleSnapshot(raw, source.DiscoverOptions{}).Table("shop", "events")
	require.Nil(t, events.RowEstimate)
	require.True(t, events.StatsStale)
}

func TestSetEstimate_StaleStatisticsAndExactCounts(t *testing.T) {
	n := int64(20)
	for name, tc := range map[string]struct {
		rel          rawRelation
		want         *int64
		exact, stale bool
	}{
		"exact count wins":                   {rawRelation{RelTuples: 0, Pages: 1, ExactRows: &n}, &n, true, false},
		"scaled to the heap's current size":  {rawRelation{RelTuples: 1000, RelPages: 10, Pages: 40}, ptr(int64(4000)), false, false},
		"analyzed while empty, now has rows": {rawRelation{RelTuples: 0, Pages: 1, LiveTuples: 20}, ptr(int64(20)), false, false},
		"never analyzed, collector knows":    {rawRelation{RelTuples: -1, Pages: 3, LiveTuples: 90}, ptr(int64(90)), false, false},
		"pages but no statistics at all":     {rawRelation{RelTuples: 0, Pages: 1}, nil, false, true},
		"never analyzed, empty heap":         {rawRelation{RelTuples: -1}, nil, false, true},
		"truly empty":                        {rawRelation{RelTuples: 0}, ptr(int64(0)), false, false},
	} {
		var tb schema.Table
		setEstimate(&tb, tc.rel)
		require.Equal(t, tc.want, tb.RowEstimate, name)
		require.Equal(t, tc.exact, tb.RowCountExact, name)
		require.Equal(t, tc.stale, tb.StatsStale, name)
	}

	// A partitioned table is exact only when every partition was counted.
	raw := fixtureCatalog()
	raw.Relations[8].ExactRows, raw.Relations[9].ExactRows = ptr(int64(7)), ptr(int64(3))
	events := AssembleSnapshot(raw, source.DiscoverOptions{}).Table("shop", "events")
	require.Equal(t, int64(10), *events.RowEstimate)
	require.True(t, events.RowCountExact)
	raw.Relations[9].ExactRows = nil
	events = AssembleSnapshot(raw, source.DiscoverOptions{}).Table("shop", "events")
	require.Equal(t, int64(307), *events.RowEstimate)
	require.False(t, events.RowCountExact)
}

func TestCountSmallTables(t *testing.T) {
	rels := []rawRelation{
		{Schema: "shop", Name: "roles", Kind: "r", Pages: 1, Readable: true},
		{Schema: "shop", Name: "big", Kind: "r", Pages: exactCountMaxPages + 1, Readable: true},
		{Schema: "shop", Name: "empty", Kind: "r", Pages: 0, Readable: true},
		{Schema: "shop", Name: "secret", Kind: "r", Pages: 1, Readable: false},
		{Schema: "shop", Name: "v", Kind: "v", Readable: true},
		{Schema: "shop", Name: "tmp_x", Kind: "r", Pages: 1, Readable: true},
		{Schema: "shop", Name: `we"ird`, Kind: "m", Pages: 2, Readable: true},
	}
	want := `SELECT 0::int4, count(*) FROM "shop"."roles" UNION ALL SELECT 6::int4, count(*) FROM "shop"."we""ird"`
	q := &fakeQuerier{results: map[string][][]any{want: {{int32(0), int64(4)}, {int32(6), int64(12)}}}}
	countSmallTables(context.Background(), q, rels, source.DiscoverOptions{ExcludePatterns: []string{"tmp_*"}})
	require.Len(t, q.args, 1, "one statement for all small tables")
	require.Equal(t, int64(4), *rels[0].ExactRows)
	require.Equal(t, int64(12), *rels[6].ExactRows)
	require.Equal(t, int64(0), *rels[2].ExactRows, "an empty heap needs no query")
	for _, i := range []int{1, 3, 4, 5} {
		require.Nil(t, rels[i].ExactRows, rels[i].Name)
	}

	// A failed count leaves the estimates alone.
	rels[0].ExactRows = nil
	failing := &fakeQuerier{fail: `SELECT 0::int4, count(*) FROM "shop"."roles"`}
	countSmallTables(context.Background(), failing, rels[:1], source.DiscoverOptions{})
	require.Len(t, failing.args, 1)
	require.Nil(t, rels[0].ExactRows)
}

func ptr[T any](v T) *T { return &v }

func TestDiscoverOptions_AC3_ExcludeGlob(t *testing.T) {
	raw := fixtureCatalog()
	raw.Relations = append(raw.Relations, rawRelation{OID: 20, Schema: "shop", Name: "orders_archive", Kind: "r", Readable: true})
	snap := AssembleSnapshot(raw, source.DiscoverOptions{ExcludePatterns: []string{"*_archive", "billing.*", "v_*"}})
	require.Nil(t, snap.Table("shop", "orders_archive"))
	require.Nil(t, snap.Table("shop", "v_active_customers"))
	require.Nil(t, snap.Table("billing", "invoice"))
	require.Len(t, snap.Schemas, 1, "a schema with every table excluded disappears")
	require.NotNil(t, snap.Table("shop", "orders"))
	// References from excluded tables are dropped with them.
	raw = fixtureCatalog()
	snap = AssembleSnapshot(raw, source.DiscoverOptions{ExcludePatterns: []string{"orders"}})
	require.Empty(t, snap.Table("shop", "customer").ReferencedBy)
}

func TestDiscoverSQL_AC3_SystemSchemaPredicate(t *testing.T) {
	for name, q := range map[string]string{"count": countSQL, "relations": relationsSQL, "columns": columnsSQL, "constraints": constraintsSQL, "indexes": indexesSQL} {
		require.Contains(t, q, "n.nspname NOT IN ('pg_catalog', 'information_schema')", name)
		require.Contains(t, q, `n.nspname NOT LIKE 'pg\_toast%'`, name)
		require.Contains(t, q, `n.nspname NOT LIKE 'pg\_temp\_%'`, name)
		require.Contains(t, q, "n.nspname = ANY($1::text[])", name, "the include list is a bound parameter")
		require.NotContains(t, q, "%s", name)
	}
	require.Equal(t, []string{}, schemaParam(source.DiscoverOptions{}), "nil would be sent as NULL")
	require.Equal(t, []string{"shop"}, schemaParam(source.DiscoverOptions{Schemas: []string{"shop"}}))
	require.True(t, strings.Contains(columnsSQL, "NOT a.attisdropped"))
}
