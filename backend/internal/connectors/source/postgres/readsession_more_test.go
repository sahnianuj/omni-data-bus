package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
)

func TestReadSession_BoundsAndCursorOnSnapshot(t *testing.T) {
	d := &fakeDialer{reply: func(_ string, sql string, _ []any) ([][]any, error) {
		switch {
		case sql == "SELECT pg_export_snapshot()":
			return [][]any{{"snap-1"}}, nil
		case strings.HasPrefix(sql, "SELECT min("):
			return [][]any{{int64(1), int64(9)}}, nil
		case strings.HasPrefix(sql, "FETCH"):
			return [][]any{{int64(1), "a"}}, nil
		}
		return nil, nil
	}}
	rs, err := beginRead(context.Background(), d.dial, "odb job c", true)
	require.NoError(t, err)
	tr := readOf("log", nil, c("id", "int8"), c("msg", "text"))
	tr.Key = []string{"id"}
	lo, hi, err := rs.KeyBounds(context.Background(), tr)
	require.NoError(t, err)
	require.Equal(t, []any{int64(1), int64(9)}, []any{lo, hi})

	tr.App = "odb job c cursor"
	cur, err := rs.OpenCursor(context.Background(), tr)
	require.NoError(t, err)
	rows, err := cur.Fetch(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, [][]any{{int64(1), "a"}}, rows)
	require.NoError(t, cur.Close())
	st := d.statements("odb job c cursor")
	require.Equal(t, beginRR, st[0])
	require.Equal(t, `SET TRANSACTION SNAPSHOT 'snap-1'`, st[1])
	require.Contains(t, st, "ROLLBACK")
	require.NoError(t, rs.Close())
	require.NoError(t, rs.Close(), "closing twice is fine")
}

func TestReadSession_CursorOnCoordinatorWithoutSnapshot(t *testing.T) {
	d := &fakeDialer{reply: okReply("")}
	rs, err := beginRead(context.Background(), d.dial, "odb job n", true)
	require.NoError(t, err)
	tr := readOf("log", nil, c("id", "int8"))
	cur, err := rs.OpenCursor(context.Background(), tr)
	require.NoError(t, err)
	_, err = cur.Fetch(context.Background(), 5)
	require.NoError(t, err)
	require.NoError(t, cur.Close())
	coord := strings.Join(d.statements("odb job n coord"), "\n")
	require.Contains(t, coord, "DECLARE")
	require.Contains(t, coord, "CLOSE "+cursorName)
}

func TestReadRelated_LookupErrors(t *testing.T) {
	q := source.RelatedQuery{Table: readOf("orders", nil, c("order_id", "int8"), c("customer_id", "int4")), Join: []string{"customer_id"}}
	// A transient failure closes the lookup connection (the next lookup opens a new one).
	fail := true
	d := &fakeDialer{reply: func(_ string, sql string, _ []any) ([][]any, error) {
		if sql == "SELECT pg_export_snapshot()" {
			return [][]any{{"s"}}, nil
		}
		if strings.Contains(sql, "ANY(") && fail {
			fail = false
			return nil, &pgconn.PgError{Code: "57P01", Message: "terminating connection"}
		}
		return nil, nil
	}}
	rs, err := beginRead(context.Background(), d.dial, "odb job e", true)
	require.NoError(t, err)
	_, err = rs.ReadRelated(context.Background(), q, [][]any{{int32(1)}})
	require.True(t, source.IsTransient(err), "%v", err)
	_, err = rs.ReadRelated(context.Background(), q, [][]any{{int32(1)}})
	require.NoError(t, err)
	require.Equal(t, 3, d.n, "coordinator + a replaced lookup connection")
	rows, err := rs.ReadRelated(context.Background(), q, nil)
	require.NoError(t, err)
	require.Nil(t, rows)

	// The snapshot can't be imported: the lookup fails and releases its slot.
	d2 := &fakeDialer{reply: func(_ string, sql string, _ []any) ([][]any, error) {
		if sql == "SELECT pg_export_snapshot()" {
			return [][]any{{"s"}}, nil
		}
		if strings.HasPrefix(sql, "SET TRANSACTION SNAPSHOT") {
			return nil, errors.New("invalid snapshot identifier")
		}
		return nil, nil
	}}
	rs2, err := beginRead(context.Background(), d2.dial, "odb job f", true)
	require.NoError(t, err)
	for range MaxLookupConns + 1 {
		_, err = rs2.ReadRelated(context.Background(), q, [][]any{{int32(1)}})
		require.ErrorContains(t, err, "invalid snapshot")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range MaxLookupConns {
		rs2.lookupSlots <- struct{}{}
	}
	_, err = rs2.ReadRelated(ctx, q, [][]any{{int32(1)}})
	require.ErrorIs(t, err, context.Canceled)
}

func TestTruncateAppName(t *testing.T) {
	require.Equal(t, "abc", truncate("abcdef", 3))
	require.Equal(t, "ab", truncate("ab", 3))
}
