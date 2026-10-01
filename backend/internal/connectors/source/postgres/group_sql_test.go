package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

// OC-063: the grouped read's statements.
func TestGroupedSQL(t *testing.T) {
	tr := source.TableRead{Schema: "s", Table: "od", Columns: []schema.Column{{Name: "order_id", UDTName: "int8"}, {Name: "line_no", UDTName: "int4"}}, Project: []bool{true, true}, Key: []string{"order_id"}, Distinct: true, Within: []string{"order_id", "line_no"}}
	require.Equal(t, `SELECT DISTINCT ON ("order_id") "order_id", "line_no" FROM "s"."od" WHERE ("order_id") > ($1) ORDER BY "order_id", "line_no" LIMIT $2`, KeysetSQL(tr, KeysetQuery{After: true}))
	require.Equal(t, `SELECT count(*) FROM (SELECT DISTINCT "order_id" FROM "s"."od") g`, CountSQL(tr))
	require.Equal(t, `SELECT DISTINCT ON ("order_id") "order_id", "line_no" FROM "s"."od" ORDER BY "order_id", "line_no" LIMIT $1`,
		PreviewSQL(source.PreviewRequest{Schema: "s", Table: "od", Columns: tr.Columns, OrderBy: []string{"order_id", "line_no"}, DistinctOn: []string{"order_id"}}))
}
