package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
)

func TestEmbedSQL_AC3_SingleKeyAny(t *testing.T) {
	q := source.RelatedQuery{
		Table: readOf("Order Details", nil, c("order_id", "int8"), c("customer_id", "int4"), c("order_date", "date")),
		Join:  []string{"customer_id"}, OrderBy: []source.OrderColumn{{Column: "order_date", Desc: true}},
	}
	require.Equal(t,
		`SELECT "order_id", "customer_id", "order_date" FROM "shop"."Order Details" WHERE "customer_id" = ANY($1::"int4"[]) ORDER BY "customer_id", "order_date" DESC`,
		RelatedSQL(q), "identifiers quoted, one array parameter")

	// 1,000 parent rows with 800 distinct customers: one array of 800 values.
	keys := make([][]any, 800)
	for i := range keys {
		keys[i] = []any{int32(i + 1)}
	}
	args := relatedArgs(q, keys)
	require.Len(t, args, 1)
	require.Len(t, args[0], 800)
	require.Equal(t, "1", args[0].([]string)[0])
}

func TestEmbedSQL_AC3_CompositeUnnest(t *testing.T) {
	q := source.RelatedQuery{
		Table: readOf("shipment", nil, c("shipment_id", "int8"), c("order_id", "int8"), c("line_no", "int2"), c("carrier", "text")),
		Join:  []string{"order_id", "line_no"},
	}
	require.Equal(t,
		`SELECT "shipment_id", "order_id", "line_no", "carrier" FROM "shop"."shipment" WHERE ("order_id", "line_no") IN (SELECT * FROM unnest($1::"int8"[], $2::"int2"[])) ORDER BY "order_id", "line_no"`,
		RelatedSQL(q))
	args := relatedArgs(q, [][]any{{int64(5), int16(1)}, {int64(5), int16(2)}})
	require.Equal(t, []any{[]string{"5", "5"}, []string{"1", "2"}}, args)
}

func TestEmbedSQL_AC3_SmallLimitInSQL(t *testing.T) {
	q := source.RelatedQuery{
		Table: readOf("orders", nil, c("order_id", "int8"), c("customer_id", "int4")),
		Join:  []string{"customer_id"}, OrderBy: []source.OrderColumn{{Column: "order_id", Desc: true}}, PerKeyLimit: 11,
	}
	q.Table.Project = []bool{true, false} // the join column isn't projected by the caller here
	sql := RelatedSQL(q)
	require.Contains(t, sql, `row_number() OVER (PARTITION BY "customer_id" ORDER BY "order_id" DESC) AS odb_rn`)
	require.Contains(t, sql, `WHERE odb_rn <= 11 ORDER BY odb_j0, odb_o0 DESC, odb_rn`)
	require.True(t, strings.HasPrefix(sql, `SELECT "order_id" FROM (SELECT "order_id" AS "order_id", "customer_id" AS odb_j0, "order_id" AS odb_o0, row_number()`), sql)
}

func TestPgText_KeyValues(t *testing.T) {
	u := [16]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 1, 2, 3, 4, 5, 6, 7, 8}
	require.Equal(t, "12345678-9abc-def0-0102-030405060708", pgText(u))
	require.Equal(t, `\x0102`, pgText([]byte{1, 2}))
	require.Equal(t, "2026-09-01T12:00:00Z", pgText(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)))
	require.Equal(t, "42", pgText(int32(42)))
	require.Equal(t, "ab", pgText("ab"))
}

func TestEmbedResolver_AC5_LookupsShareTheSnapshot(t *testing.T) {
	d := &fakeDialer{reply: func(_ string, sql string, _ []any) ([][]any, error) {
		if sql == "SELECT pg_export_snapshot()" {
			return [][]any{{"00000003-0000001B-1"}}, nil
		}
		if strings.Contains(sql, "ANY(") {
			return [][]any{{int64(1), int32(7)}}, nil
		}
		return nil, nil
	}}
	rs, err := beginRead(context.Background(), d.dial, "odb job j1", true)
	require.NoError(t, err)
	q := source.RelatedQuery{Table: readOf("orders", nil, c("order_id", "int8"), c("customer_id", "int4")), Join: []string{"customer_id"}}
	for range 3 {
		rows, err := rs.ReadRelated(context.Background(), q, [][]any{{int32(7)}})
		require.NoError(t, err)
		require.Equal(t, [][]any{{int64(1), int32(7)}}, rows)
	}
	st := d.statements("odb job j1 embed")
	require.Equal(t, beginRR, st[0])
	require.Equal(t, `SET TRANSACTION SNAPSHOT '00000003-0000001B-1'`, st[1], "the embed lookups see the parents' snapshot")
	require.Len(t, st, 3+3, "one connection, reused for every lookup")
	require.NoError(t, rs.Close())
	require.Contains(t, d.statements("odb job j1 embed"), "ROLLBACK")

	// Without an exported snapshot the lookups run on the coordinator's transaction.
	d2 := &fakeDialer{reply: okReply("")}
	rs2, err := beginRead(context.Background(), d2.dial, "odb job j2", true)
	require.NoError(t, err)
	_, err = rs2.ReadRelated(context.Background(), q, [][]any{{int32(7)}})
	require.NoError(t, err)
	require.Empty(t, d2.statements("odb job j2 embed"))
}
