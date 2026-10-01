//go:build integration

package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

// OC-063: a grouped read returns one row per group (the first by primary key), pages and
// resumes on the group key, and counts groups.
func TestGroupedRead_AC4_Integration(t *testing.T) {
	srv := pgServer(t)
	ctx := context.Background()
	tbl := fmt.Sprintf("it_od_%d", time.Now().UnixNano()%1_000_000_000)
	c := ownerConn(t, srv)
	_, err := c.Exec(ctx, fmt.Sprintf(`CREATE TABLE public.%[1]s (type text, order_id bigint, line_no int, quantity int, PRIMARY KEY (order_id, line_no));
		INSERT INTO public.%[1]s SELECT 'order_detail', o, l, l * 10 FROM generate_series(1, 5000) o, generate_series(1, 3) l;`, tbl))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = ownerConn(t, srv).Exec(context.Background(), "DROP TABLE public."+tbl) })

	cols := []schema.Column{{Name: "type", UDTName: "text"}, {Name: "order_id", UDTName: "int8"}, {Name: "line_no", UDTName: "int4"}, {Name: "quantity", UDTName: "int4"}}
	tr := source.TableRead{Schema: "public", Table: tbl, Columns: cols, Project: []bool{true, true, true, true}, Key: []string{"order_id"}, Distinct: true, Within: []string{"order_id", "line_no"}}
	rs, err := openReader(t, srv).BeginRead(ctx, "odb it", true)
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()

	n, err := rs.Count(ctx, tr)
	require.NoError(t, err)
	require.Equal(t, int64(5000), n, "groups, not rows")

	rows := readAll(t, rs, tr, 4, 15000)
	require.Len(t, rows, 5000)
	seen := map[int64]bool{}
	for _, r := range rows {
		id := r[1].(int64)
		require.False(t, seen[id], "group %d once", id)
		seen[id] = true
		require.Equal(t, int32(1), r[2], "the group's first line")
	}

	// Resume after group 4000: groups 4001..5000, each once.
	p, err := rs.OpenPartition(ctx, tr, nil, nil)
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	page, err := p.ReadAfter(ctx, []any{int64(4000)}, 2000)
	require.NoError(t, err)
	require.Len(t, page, 1000)
	require.Equal(t, int64(4001), page[0][1])

	// By key: one row per group.
	byKey, err := rs.ReadKeys(ctx, tr, [][]any{{int64(7)}, {int64(9)}})
	require.NoError(t, err)
	require.Len(t, byKey, 2)
	require.Equal(t, int32(1), byKey[0][2])
}
