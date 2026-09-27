package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

func readOf(table string, key []string, cols ...schema.Column) source.TableRead {
	project := make([]bool, len(cols))
	for i := range project {
		project[i] = true
	}
	return source.TableRead{Schema: "shop", Table: table, Columns: cols, Project: project, Key: key}
}

func c(name, udt string) schema.Column { return schema.Column{Name: name, UDTName: udt} }

func TestKeysetSQL_AC1_SinglePK(t *testing.T) {
	tr := readOf("customer", []string{"customer_id"}, c("customer_id", "int4"), c("email", "text"))
	require.Equal(t, `SELECT "customer_id", "email" FROM "shop"."customer" ORDER BY "customer_id" LIMIT $1`, KeysetSQL(tr, KeysetQuery{}))
	require.Equal(t, `SELECT "customer_id", "email" FROM "shop"."customer" WHERE ("customer_id") > ($1) ORDER BY "customer_id" LIMIT $2`,
		KeysetSQL(tr, KeysetQuery{After: true}))
}

func TestKeysetSQL_AC1_CompositePK(t *testing.T) {
	tr := readOf("order_line", []string{"order_id", "line_no"}, c("order_id", "int8"), c("line_no", "int2"), c("qty", "int4"))
	require.Equal(t,
		`SELECT "order_id", "line_no", "qty" FROM "shop"."order_line" WHERE ("order_id", "line_no") > ($1, $2) ORDER BY "order_id", "line_no" LIMIT $3`,
		KeysetSQL(tr, KeysetQuery{After: true}))
}

func TestKeysetSQL_AC1_PartitionBounds(t *testing.T) {
	tr := readOf("big_events", []string{"id"}, c("id", "int8"), c("payload", "jsonb"))
	require.Equal(t, `SELECT "id", "payload"::text AS "payload" FROM "shop"."big_events" WHERE "id" >= $1 AND "id" < $2 ORDER BY "id" LIMIT $3`,
		KeysetSQL(tr, KeysetQuery{Lo: true, Hi: true}))
	require.Equal(t, `SELECT "id", "payload"::text AS "payload" FROM "shop"."big_events" WHERE ("id") > ($1) AND "id" < $2 ORDER BY "id" LIMIT $3`,
		KeysetSQL(tr, KeysetQuery{After: true, Hi: true}), "resuming inside a partition keeps its upper bound")
}

func TestKeysetSQL_AC1_ProjectionAndQuoting(t *testing.T) {
	tr := readOf("Order Details", []string{"Order ID"}, c("Order ID", "int4"), c("unit price", "money"), c("unused", "text"), c(`we"ird`, "text"))
	tr.Project[2] = false
	sql := KeysetSQL(tr, KeysetQuery{After: true})
	require.Equal(t, `SELECT "Order ID", "unit price"::numeric AS "unit price", "we""ird" FROM "shop"."Order Details" WHERE ("Order ID") > ($1) ORDER BY "Order ID" LIMIT $2`, sql)
	require.NotContains(t, sql, "unused")
	_, idx := projection(tr)
	require.Equal(t, []int{0, 1, 3}, idx, "values land in their table positions")
}

func TestCursorSQL_AC6(t *testing.T) {
	tr := readOf("page_view", nil, c("url", "text"), c("at", "timestamptz"))
	require.Equal(t, `DECLARE odb_read NO SCROLL CURSOR FOR SELECT "url", "at" FROM "shop"."page_view"`, CursorSQL(tr))
	require.Equal(t, `FETCH FORWARD 500 FROM odb_read`, FetchSQL(500))
}

func TestRetrySQL_AC6_UnnestKeys(t *testing.T) {
	single := readOf("customer", []string{"customer_id"}, c("customer_id", "int4"), c("email", "text"))
	require.Equal(t,
		`SELECT "customer_id", "email" FROM "shop"."customer" WHERE ("customer_id") IN (SELECT * FROM unnest($1::"int4"[])) ORDER BY "customer_id"`,
		RetrySQL(single))
	comp := readOf("order_line", []string{"order_id", "line_no"}, c("order_id", "int8"), c("line_no", "int2"))
	require.Equal(t,
		`SELECT "order_id", "line_no" FROM "shop"."order_line" WHERE ("order_id", "line_no") IN (SELECT * FROM unnest($1::"int8"[], $2::"int2"[])) ORDER BY "order_id", "line_no"`,
		RetrySQL(comp))
}

func TestBoundsAndCountSQL(t *testing.T) {
	tr := readOf("big_events", []string{"id"}, c("id", "int8"))
	require.Equal(t, `SELECT min("id"), max("id") FROM "shop"."big_events"`, BoundsSQL(tr))
	require.Equal(t, `SELECT count(*) FROM "shop"."big_events"`, CountSQL(tr))
}
