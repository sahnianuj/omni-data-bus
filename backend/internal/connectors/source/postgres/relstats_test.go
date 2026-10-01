package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
)

func TestRelStatsSQL_CountPerKey(t *testing.T) {
	q := source.RelatedQuery{
		Table:  readOf("order_items", nil, c("order_id", "int8"), c("line_no", "int4"), c("status", "text")),
		Join:   []string{"order_id"},
		Filter: []source.FilterCond{{Column: "status", Op: "eq", Args: []string{"open"}}},
	}
	require.Equal(t,
		`SELECT (SELECT count(*) FROM "shop"."order_items" WHERE "order_id" = k.odb_k0 AND "status" = $2::"text") `+
			`FROM unnest($1::"int8"[]) WITH ORDINALITY AS k(odb_k0, odb_i) ORDER BY k.odb_i`,
		RelatedCountSQL(q), "one count per key, in the keys' order; filters follow the key arrays")
	require.Equal(t, []any{[]string{"1001", "1002"}, "open"}, relatedArgs(q, [][]any{{int64(1001)}, {int64(1002)}}))
}

func TestRelStatsSQL_ManyToManyAndTop(t *testing.T) {
	q := m2mQuery()
	require.Equal(t,
		`SELECT (SELECT count(*) FROM "shop"."order_items" j LEFT JOIN "shop"."products" t ON t."product_id" = j."product_id" WHERE j."order_id" = k.odb_k0) `+
			`FROM unnest($1::"int8"[]) WITH ORDINALITY AS k(odb_k0, odb_i) ORDER BY k.odb_i`,
		RelatedCountSQL(q), "many-to-many counts junction rows")
	require.Equal(t,
		`SELECT j."order_id"::text, count(*) AS odb_n FROM "shop"."order_items" j LEFT JOIN "shop"."products" t ON t."product_id" = j."product_id" `+
			`WHERE j."order_id" IS NOT NULL GROUP BY j."order_id" ORDER BY odb_n DESC LIMIT 1`,
		RelatedTopSQL(q), "the worst case: the key with the most rows")
	q.Filter = []source.FilterCond{{Column: "sku", Op: "like", Args: []string{"A%"}}}
	require.Contains(t, RelatedTopSQL(q), `WHERE t."sku" LIKE $1::text AND j."order_id" IS NOT NULL`)
}
