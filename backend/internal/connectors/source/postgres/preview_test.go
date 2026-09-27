package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

func TestPreviewSQL_AC5_QuotesIdentifiers(t *testing.T) {
	sql := PreviewSQL(source.PreviewRequest{
		Schema: "shop", Table: "Order Details",
		Columns: []schema.Column{{Name: "order_id", UDTName: "int8"}, {Name: `we"ird`, UDTName: "text"}},
		OrderBy: []string{"order_id", "line no"},
	})
	require.Equal(t, `SELECT "order_id", "we""ird" FROM "shop"."Order Details" ORDER BY "order_id", "line no" LIMIT $1`, sql)

	sql = PreviewSQL(source.PreviewRequest{Schema: `s"; DROP TABLE x; --`, Table: "t", Columns: []schema.Column{{Name: "a", UDTName: "int4"}}})
	require.Equal(t, `SELECT "a" FROM "s""; DROP TABLE x; --"."t" LIMIT $1`, sql, "injection attempts stay inside quotes")
}

func TestPreviewSQL_AC5_CastsNonNativeTypes(t *testing.T) {
	cols := []schema.Column{
		{Name: "price", UDTName: "money"},
		{Name: "status", UDTName: "order_status"},
		{Name: "email", UDTName: "citext"},
		{Name: "search", UDTName: "tsvector"},
		{Name: "until", UDTName: "timetz"},
		{Name: "statuses", UDTName: "_order_status", IsArray: true},
		{Name: "sizes", UDTName: "_int4", IsArray: true},
		{Name: "created", UDTName: "timestamptz"},
	}
	sql := PreviewSQL(source.PreviewRequest{Schema: "s", Table: "t", Columns: cols})
	for _, want := range []string{
		`"price"::numeric AS "price"`, `"status"::text AS "status"`, `"email"::text AS "email"`,
		`"search"::text AS "search"`, `"until"::text AS "until"`, `"statuses"::text[] AS "statuses"`,
		`"sizes", "created"`,
	} {
		require.Contains(t, sql, want)
	}
}

func TestPreview_AC5_LimitCapped(t *testing.T) {
	require.Equal(t, source.MaxPreviewLimit, source.ClampPreviewLimit(1000))
	require.Equal(t, source.DefaultPreviewLimit, source.ClampPreviewLimit(0))
	require.Equal(t, 10, source.ClampPreviewLimit(10))
}

func TestSampleSQL_AC4_Modes(t *testing.T) {
	cols := []schema.Column{{Name: "id", UDTName: "int4"}}
	big := PreviewSQL(source.PreviewRequest{Schema: "s", Table: "t", Columns: cols, Limit: 10, Random: true, RowEstimate: 10_000_000})
	require.Equal(t, `SELECT "id" FROM "s"."t" TABLESAMPLE SYSTEM (0.0010) LIMIT $1`, big)
	small := PreviewSQL(source.PreviewRequest{Schema: "s", Table: "t", Columns: cols, Limit: 10, Random: true, RowEstimate: 5_000})
	require.Equal(t, `SELECT "id" FROM "s"."t" ORDER BY random() LIMIT $1`, small)
	tiny := PreviewSQL(source.PreviewRequest{Schema: "s", Table: "t", Columns: cols, Limit: 50, Random: true, RowEstimate: 100_000})
	require.Contains(t, tiny, "TABLESAMPLE SYSTEM (0.5000)")
}
