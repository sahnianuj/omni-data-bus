package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
)

// recConn records statements; query answers come from reply.
type recConn struct {
	name   string
	log    *[]string
	mu     *sync.Mutex
	reply  func(sql string, args []any) ([][]any, error)
	closed bool
}

func (f *recConn) rec(s string) {
	f.mu.Lock()
	*f.log = append(*f.log, f.name+": "+s)
	f.mu.Unlock()
}

func (f *recConn) Exec(_ context.Context, sql string, _ ...any) error {
	f.rec(sql)
	if f.reply != nil {
		_, err := f.reply(sql, nil)
		return err
	}
	return nil
}

func (f *recConn) Query(_ context.Context, sql string, args ...any) ([][]any, error) {
	f.rec(sql)
	return f.reply(sql, args)
}

func (f *recConn) Close(context.Context) error { f.closed = true; return nil }

type fakeDialer struct {
	mu    sync.Mutex
	log   []string
	n     int
	reply func(conn string, sql string, args []any) ([][]any, error)
}

func (d *fakeDialer) dial(_ context.Context, app string) (readConn, error) {
	d.mu.Lock()
	d.n++
	name := app
	d.mu.Unlock()
	return &recConn{name: name, log: &d.log, mu: &d.mu, reply: func(sql string, args []any) ([][]any, error) { return d.reply(name, sql, args) }}, nil
}

func (d *fakeDialer) statements(conn string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, l := range d.log {
		if strings.HasPrefix(l, conn+": ") {
			out = append(out, strings.TrimPrefix(l, conn+": "))
		}
	}
	return out
}

func okReply(snapshot string) func(string, string, []any) ([][]any, error) {
	return func(_ string, sql string, _ []any) ([][]any, error) {
		switch {
		case sql == "SELECT pg_export_snapshot()":
			if snapshot == "" {
				return nil, &pgconn.PgError{Code: "25006", Message: "cannot export a snapshot from a subtransaction"}
			}
			return [][]any{{snapshot}}, nil
		case strings.HasPrefix(sql, "SELECT count(*)"):
			return [][]any{{int64(42)}}, nil
		case strings.HasPrefix(sql, "SELECT min("):
			return [][]any{{int64(1), int64(100)}}, nil
		}
		return nil, nil
	}
}

func TestSnapshotCoordinator_AC3_SharesSnapshot(t *testing.T) {
	d := &fakeDialer{reply: okReply("00000003-0000001B-1")}
	tr := readOf("big_events", []string{"id"}, c("id", "int8"), c("kind", "text"))
	rs, err := beginRead(context.Background(), d.dial, "odb job j1", true)
	require.NoError(t, err)
	require.Equal(t, "00000003-0000001B-1", rs.SnapshotID())
	require.Equal(t, []string{"SET statement_timeout = 0", "SET idle_in_transaction_session_timeout = 0", beginRR, "SELECT pg_export_snapshot()"},
		d.statements("odb job j1 coord"))

	for k := 0; k < 4; k++ {
		tr.App = "odb job j1 p" + string(rune('0'+k))
		p, err := rs.OpenPartition(context.Background(), tr, int64(k*25), int64(k*25+25))
		require.NoError(t, err)
		_, err = p.ReadAfter(context.Background(), nil, 100)
		require.NoError(t, err)
		st := d.statements(tr.App)
		require.Equal(t, beginRR, st[0])
		require.Equal(t, `SET TRANSACTION SNAPSHOT '00000003-0000001B-1'`, st[1], "before the first query")
		require.True(t, strings.HasPrefix(st[3], "SELECT "), st[3])
		require.NoError(t, p.Close())
	}
	n, err := rs.Count(context.Background(), tr)
	require.NoError(t, err)
	require.EqualValues(t, 42, n)
	require.NoError(t, rs.Close())
	require.Contains(t, d.statements("odb job j1 coord"), "ROLLBACK")
}

func TestSnapshotCoordinator_AC3_FallbackWhenExportUnavailable(t *testing.T) {
	d := &fakeDialer{reply: okReply("")}
	tr := readOf("big_events", []string{"id"}, c("id", "int8"))
	rs, err := beginRead(context.Background(), d.dial, "odb job j2", true)
	require.NoError(t, err)
	require.Empty(t, rs.SnapshotID(), "the pipeline then uses one reader and warns")
	require.Equal(t, []string{"SET statement_timeout = 0", "SET idle_in_transaction_session_timeout = 0", beginRR, "SELECT pg_export_snapshot()", "ROLLBACK", beginRR},
		d.statements("odb job j2 coord"))
	tr.App = "odb job j2 p0"
	p, err := rs.OpenPartition(context.Background(), tr, nil, nil)
	require.NoError(t, err)
	_, err = p.ReadAfter(context.Background(), nil, 10)
	require.NoError(t, err)
	require.Equal(t, 1, d.n, "the only reader shares the coordinator's transaction")
}

func TestPartition_TransientReconnectsWithSnapshot(t *testing.T) {
	fails := 1
	d := &fakeDialer{}
	d.reply = func(conn, sql string, args []any) ([][]any, error) {
		if strings.HasSuffix(conn, "p0") && strings.HasPrefix(sql, "SELECT \"id\"") && fails > 0 {
			fails--
			return nil, &pgconn.PgError{Code: "57P01", Message: "terminating connection"}
		}
		if strings.HasPrefix(sql, "SELECT \"id\"") {
			return [][]any{{int64(7)}}, nil
		}
		return okReply("snap-1")(conn, sql, args)
	}
	rs, err := beginRead(context.Background(), d.dial, "j", true)
	require.NoError(t, err)
	tr := readOf("big_events", []string{"id"}, c("id", "int8"), c("kind", "text"))
	tr.App = "j p0"
	p, err := rs.OpenPartition(context.Background(), tr, nil, nil)
	require.NoError(t, err)
	_, err = p.ReadAfter(context.Background(), []any{int64(6)}, 10)
	require.True(t, source.IsTransient(err))
	rows, err := p.ReadAfter(context.Background(), []any{int64(6)}, 10)
	require.NoError(t, err)
	require.Equal(t, [][]any{{int64(7), nil}}, rows, "widened to the table layout")
	st := d.statements("j p0")
	require.Equal(t, 2, strings.Count(strings.Join(st, "\n"), "SET TRANSACTION SNAPSHOT 'snap-1'"), "the reconnect rejoins the snapshot")
	require.False(t, p.(source.Resnapshotter).Resnapshotted())
}

func TestReadKeys_TextArraysPerKeyColumn(t *testing.T) {
	var got []any
	d := &fakeDialer{reply: func(conn, sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "unnest(") {
			got = args
			return [][]any{{int64(1), int64(2)}}, nil
		}
		return okReply("s")(conn, sql, args)
	}}
	rs, err := beginRead(context.Background(), d.dial, "j", true)
	require.NoError(t, err)
	tr := readOf("order_line", []string{"order_id", "line_no"}, c("order_id", "int8"), c("line_no", "int2"))
	rows, err := rs.ReadKeys(context.Background(), tr, [][]any{{float64(1), float64(2)}, {float64(10), float64(3)}})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, []any{[]string{"1", "10"}, []string{"2", "3"}}, got)
	require.Equal(t, "12345678901", keyText(float64(12345678901)))
	require.Equal(t, "1.5", keyText(1.5))
	require.Equal(t, "a-b", keyText("a-b"))
}

// TestTotals_AC3_CountUsesSnapshot: count(*) runs on the coordinator, inside the transaction
// that exported the snapshot the readers share, so the total matches what is read (OC-027).
func TestTotals_AC3_CountUsesSnapshot(t *testing.T) {
	d := &fakeDialer{reply: okReply("snap-9")}
	rs, err := beginRead(context.Background(), d.dial, "job", true)
	require.NoError(t, err)
	n, err := rs.Count(context.Background(), readOf("orders", []string{"id"}, c("id", "int8")))
	require.NoError(t, err)
	require.EqualValues(t, 42, n)
	st := d.statements("job coord")
	require.Equal(t, []string{
		"SET statement_timeout = 0", "SET idle_in_transaction_session_timeout = 0", beginRR,
		"SELECT pg_export_snapshot()", `SELECT count(*) FROM "shop"."orders"`,
	}, st, "same transaction, after the export")
}
