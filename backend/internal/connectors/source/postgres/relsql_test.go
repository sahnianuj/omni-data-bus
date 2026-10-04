package postgres

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

// m2mQuery is orders ↔ products through order_items (OC-044 model).
func m2mQuery() source.RelatedQuery {
	return source.RelatedQuery{
		Table: readOf("products", nil, c("product_id", "int4"), c("sku", "text"), c("name", "text")),
		Join:  []string{"order_id"},
		Via: &source.ViaQuery{
			Table:     readOf("order_items", nil, c("order_id", "int8"), c("product_id", "int4"), c("line_no", "int4"), c("quantity", "int4"), c("unit_price", "numeric")),
			ChildJoin: []source.ViaJoin{{Via: "product_id", Child: "product_id"}},
			Columns:   []string{"order_id", "quantity", "unit_price", "line_no"},
		},
		OrderBy: []source.OrderColumn{{Column: "line_no", Via: true}},
	}
}

func TestRelSQL_AC2_ManyToManyJoin(t *testing.T) {
	require.Equal(t,
		`SELECT t."product_id" AS "product_id", t."sku" AS "sku", t."name" AS "name", j."order_id" AS "__v_order_id", j."quantity" AS "__v_quantity", `+
			`j."unit_price" AS "__v_unit_price", j."line_no" AS "__v_line_no" `+
			`FROM "shop"."order_items" j LEFT JOIN "shop"."products" t ON t."product_id" = j."product_id" `+
			`WHERE j."order_id" = ANY($1::"int8"[]) ORDER BY j."order_id", j."line_no"`,
		RelatedSQL(m2mQuery()), "one query joins junction and target; junction columns aliased __v_*")

	// A row is the table's columns, then the junction's in order.
	row := relatedRow([]any{int32(7), "A-1", "Pen", int64(1001), int32(2), "9.50", int32(1)}, []int{0, 1, 2}, 3)
	require.Equal(t, []any{int32(7), "A-1", "Pen", int64(1001), int32(2), "9.50", int32(1)}, row)

	// A small limit partitions by the junction's parent column.
	q := m2mQuery()
	q.PerKeyLimit = 5
	sql := RelatedSQL(q)
	require.Contains(t, sql, `row_number() OVER (PARTITION BY j."order_id" ORDER BY j."line_no") AS odb_rn`)
	require.Contains(t, sql, `t."product_id" AS "product_id", t."sku" AS "sku"`)
	require.True(t, strings.HasPrefix(sql, `SELECT "product_id", "sku", "name", "__v_order_id", "__v_quantity", "__v_unit_price", "__v_line_no" FROM (SELECT `), sql)
}

func TestRelSQL_AC2_CompositeUnnest(t *testing.T) {
	q := m2mQuery()
	q.Via.Table.Columns = append(q.Via.Table.Columns, c("region", "text"))
	q.Join = []string{"order_id", "region"}
	sql := RelatedSQL(q)
	require.Contains(t, sql, `WHERE (j."order_id", j."region") IN (SELECT * FROM unnest($1::"int8"[], $2::"text"[]))`)
	require.Contains(t, sql, `ORDER BY j."order_id", j."region", j."line_no"`)
	args := relatedArgs(q, [][]any{{int64(1), "eu"}, {int64(2), "us"}})
	require.Equal(t, []any{[]string{"1", "2"}, []string{"eu", "us"}}, args)
}

func TestRelSQL_AC3_FilterParams(t *testing.T) {
	q := source.RelatedQuery{
		Table: readOf("order_items", nil, c("order_id", "int8"), c("status", "text"), c("quantity", "int4")),
		Join:  []string{"order_id"},
		Filter: []source.FilterCond{
			{Column: "status", Op: "eq", Args: []string{"active' OR 1=1; DROP TABLE orders; --"}},
			{Column: "quantity", Op: "gt", Args: []string{"0"}},
			{Column: "status", Op: "in", Args: []string{"a", "b"}},
			{Column: "status", Op: "notNull"},
			{Column: "status", Op: "like", Args: []string{"act%"}},
		},
	}
	sql := RelatedSQL(q)
	require.Equal(t,
		`SELECT "order_id", "status", "quantity" FROM "shop"."order_items" WHERE "order_id" = ANY($1::"int8"[]) AND "status" = $2::"text" `+
			`AND "quantity" > $3::"int4" AND "status" = ANY($4::"text"[]) AND "status" IS NOT NULL AND "status" LIKE $5::text ORDER BY "order_id"`, sql)
	require.NotContains(t, sql, "DROP", "values are never part of the SQL text")
	args := relatedArgs(q, [][]any{{int64(1)}})
	require.Equal(t, []any{[]string{"1"}, "active' OR 1=1; DROP TABLE orders; --", "0", []string{"a", "b"}, "act%"}, args, "they're bind parameters")

	// Junction filters use the junction's alias and types (many-to-many).
	m := m2mQuery()
	m.Filter = []source.FilterCond{{Column: "quantity", Op: "gt", Args: []string{"0"}, Via: true}, {Column: "sku", Op: "ne", Args: []string{"X"}}}
	require.Contains(t, RelatedSQL(m), `WHERE j."order_id" = ANY($1::"int8"[]) AND j."quantity" > $2::"int4" AND t."sku" <> $3::"text" ORDER BY`)
}

func TestRelSQL_SortedMergeRange(t *testing.T) {
	q := source.RelatedQuery{
		Table: readOf("orders", nil, c("order_id", "int8"), c("customer_code", "text")),
		Join:  []string{"customer_code"}, Range: &source.KeyRange{From: "A", To: "m"},
		OrderBy: []source.OrderColumn{{Column: "order_id"}},
	}
	require.Equal(t,
		`SELECT "order_id", "customer_code" FROM "shop"."orders" WHERE "customer_code" COLLATE "C" >= $1::"text" AND "customer_code" COLLATE "C" <= $2::"text" `+
			`ORDER BY "customer_code" COLLATE "C", "order_id"`, RelatedSQL(q), "text keys compare byte-wise")
	require.Equal(t, []any{"A", "m"}, relatedArgs(q, nil))
}

// TestRelSQL_OC056_PrimaryKeyTiebreak: arrays come in the same order on every read: after the
// relation's own order, the table's primary key, then the junction's — so verification's
// re-read and an exported run build the same documents as the migration.
func TestRelSQL_OC056_PrimaryKeyTiebreak(t *testing.T) {
	pk := func(name, udt string) schema.Column {
		return schema.Column{Name: name, UDTName: udt, IsPrimaryKey: true}
	}
	q := source.RelatedQuery{
		Table: readOf("tags", nil, pk("tag_id", "int4"), c("label", "text")),
		Join:  []string{"order_id"},
		Via: &source.ViaQuery{
			Table:     readOf("order_tags", nil, pk("order_id", "int8"), pk("tag_id", "int4")),
			ChildJoin: []source.ViaJoin{{Via: "tag_id", Child: "tag_id"}},
		},
	}
	sql := RelatedSQL(q)
	require.True(t, strings.HasSuffix(sql, `ORDER BY j."order_id", t."tag_id", j."tag_id"`), sql)

	// A one-to-many with an order keeps it first; a key already ordered isn't repeated.
	items := source.RelatedQuery{
		Table: readOf("order_items", nil, pk("order_item_id", "int8"), c("order_id", "int8"), c("line_no", "int4")),
		Join:  []string{"order_id"}, OrderBy: []source.OrderColumn{{Column: "order_item_id", Desc: true}},
	}
	require.True(t, strings.HasSuffix(RelatedSQL(items), `ORDER BY "order_id", "order_item_id" DESC`), RelatedSQL(items))
}
