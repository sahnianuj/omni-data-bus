//go:build integration

package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/pipeline"
	"omnidatabus/internal/schema"
)

func ownerConn(t *testing.T, srv testServer) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), fmt.Sprintf("postgres://%s:%s@%s:%d/shop?sslmode=disable", srv.owner, srv.ownerPw, srv.host, srv.port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func openReader(t *testing.T, srv testServer) source.SnapshotReader {
	t.Helper()
	sess, err := New().Open(context.Background(), srv.cfg(srv.owner), connectors.Credentials{Password: srv.ownerPw})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	sr, ok := sess.(source.SnapshotReader)
	require.True(t, ok)
	return sr
}

// readAll streams every partition of t in parallel and returns the rows.
func readAll(t *testing.T, rs source.ReadSession, tr source.TableRead, k int, rowEstimate int64) [][]any {
	t.Helper()
	ctx := context.Background()
	lo, hi, err := rs.KeyBounds(ctx, tr)
	require.NoError(t, err)
	ranges := pipeline.PlanPartitions(pipeline.KeyInt, lo, hi, pipeline.PartitionCount(pipeline.KeyInt, rowEstimate, k))
	out := make(chan *source.Batch, pipeline.RowsChanCap)
	var wg sync.WaitGroup
	for i, r := range ranges {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr := tr
			tr.App = fmt.Sprintf("odb it p%d", i)
			p, err := rs.OpenPartition(ctx, tr, r.Lo, r.Hi)
			require.NoError(t, err)
			defer func() { _ = p.Close() }()
			require.NoError(t, pipeline.ReadPartition(ctx, p, i, tr.KeyIndexes(), nil, 0, pipeline.ReadConfig{BatchSize: 5000}, nil, out))
		}()
	}
	go func() { wg.Wait(); close(out) }()
	var rows [][]any
	for b := range out {
		rows = append(rows, b.Rows...)
	}
	return rows
}

func TestSnapshot_AC3_Integration_ConcurrentWritesInvisible(t *testing.T) {
	ctx := context.Background()
	srv := pgServer(t)
	owner := ownerConn(t, srv)
	table := fmt.Sprintf("it_snap_%d", time.Now().UnixNano())
	_, err := owner.Exec(ctx, fmt.Sprintf(`CREATE TABLE shop.%s (id bigint PRIMARY KEY, v text NOT NULL);
		INSERT INTO shop.%s SELECT g, 'before' FROM generate_series(1, 20000) g;`, table, table))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = owner.Exec(context.Background(), "DROP TABLE IF EXISTS shop."+table) })

	rs, err := openReader(t, srv).BeginRead(ctx, "odb it", true)
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()
	require.NotEmpty(t, rs.SnapshotID())

	// After the job's snapshot: new rows, an update and a delete.
	_, err = owner.Exec(ctx, fmt.Sprintf(`INSERT INTO shop.%s SELECT g, 'after' FROM generate_series(20001, 25000) g;
		UPDATE shop.%s SET v = 'after' WHERE id = 10; DELETE FROM shop.%s WHERE id = 11;`, table, table, table))
	require.NoError(t, err)

	tr := source.TableRead{
		Schema: "shop", Table: table, Key: []string{"id"},
		Columns: []schema.Column{{Name: "id", UDTName: "int8"}, {Name: "v", UDTName: "text"}}, Project: []bool{true, true},
	}
	rows := readAll(t, rs, tr, 4, 600_000) // force 4 partitions
	require.Len(t, rows, 20000, "rows inserted after the snapshot are invisible")
	for _, r := range rows {
		require.Equal(t, "before", r[1], "id %v", r[0])
	}
	n, err := rs.Count(ctx, tr)
	require.NoError(t, err)
	require.EqualValues(t, 20000, n, "count(*) runs in the same snapshot")
}

func TestReader_AC4_Integration_1MRows(t *testing.T) {
	ctx := context.Background()
	srv := pgServer(t)
	owner := ownerConn(t, srv)
	var exists bool
	require.NoError(t, owner.QueryRow(ctx, `SELECT to_regclass('shop.big_events') IS NOT NULL`).Scan(&exists))
	if !exists {
		t.Skip("shop.big_events is seeded by the dev stack (deploy/compose/seed/source/02-big-events.sql)")
	}
	rs, err := openReader(t, srv).BeginRead(ctx, "odb it", true)
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()
	tr := source.TableRead{Schema: "shop", Table: "big_events", Key: []string{"event_id"}, Columns: []schema.Column{
		{Name: "event_id", UDTName: "int8"}, {Name: "kind", UDTName: "text"}, {Name: "occurred_at", UDTName: "timestamptz"}, {Name: "payload", UDTName: "jsonb"},
	}, Project: []bool{true, true, false, true}}
	start := time.Now()
	rows := readAll(t, rs, tr, 0, 1_000_000)
	elapsed := time.Since(start)
	require.Len(t, rows, 1_000_000)
	seen := make([]bool, 1_000_001)
	for _, r := range rows {
		id := r[0].(int64)
		require.False(t, seen[id], "duplicate %d", id)
		seen[id] = true
		require.Nil(t, r[2], "occurred_at wasn't projected")
		require.IsType(t, "", r[3], "jsonb is read as text")
	}
	t.Logf("read 1,000,000 rows in %s (%.0f rows/s)", elapsed, 1e6/elapsed.Seconds())
}
