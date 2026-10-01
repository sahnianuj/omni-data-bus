package postgres

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"omnidatabus/internal/connectors/source"
)

// readConn is the part of a PostgreSQL connection the readers use (faked in tests).
type readConn interface {
	Exec(ctx context.Context, sql string, args ...any) error
	// Query returns every row as driver values.
	Query(ctx context.Context, sql string, args ...any) ([][]any, error)
	Close(ctx context.Context) error
}

type pgxReadConn struct{ c *pgx.Conn }

func (p pgxReadConn) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := p.c.Exec(ctx, sql, args...)
	return err
}

func (p pgxReadConn) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := p.c.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

// Stream calls fn with each row as it arrives (sorted-merge, OC-046).
func (p pgxReadConn) Stream(ctx context.Context, sql string, fn func([]any) error, args ...any) error {
	rows, err := p.c.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return err
		}
		if err := fn(vals); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (p pgxReadConn) Close(ctx context.Context) error { return p.c.Close(ctx) }

// streamConn is a readConn that can stream rows; others are read whole and then replayed.
type streamConn interface {
	Stream(ctx context.Context, sql string, fn func([]any) error, args ...any) error
}

func streamOn(ctx context.Context, c readConn, sql string, fn func([]any) error, args ...any) error {
	if sc, ok := c.(streamConn); ok {
		return sc.Stream(ctx, sql, fn, args...)
	}
	rows, err := c.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

// dialFunc opens a new connection with the given application_name.
type dialFunc func(ctx context.Context, app string) (readConn, error)

func pgxDialer(cc *pgx.ConnConfig) dialFunc {
	return func(ctx context.Context, app string) (readConn, error) {
		c := cc.Copy()
		if c.RuntimeParams == nil {
			c.RuntimeParams = map[string]string{}
		}
		if app != "" {
			c.RuntimeParams["application_name"] = truncate(app, 63)
		}
		conn, err := pgx.ConnectConfig(ctx, c)
		if err != nil {
			return nil, err
		}
		return pgxReadConn{conn}, nil
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

const beginRR = "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY"

// BeginRead implements source.SnapshotReader.
func (s *pgSession) BeginRead(ctx context.Context, app string, exportSnapshot bool) (source.ReadSession, error) {
	if s.cc == nil {
		return nil, fmt.Errorf("postgres: session can't open reader connections")
	}
	return beginRead(ctx, pgxDialer(s.cc), app, exportSnapshot)
}

// ProbeSnapshotExport implements source.SnapshotProber: it exports (and discards) a snapshot.
func (s *pgSession) ProbeSnapshotExport(ctx context.Context) error {
	tx, err := s.conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var id string
	return tx.QueryRow(ctx, "SELECT pg_export_snapshot()").Scan(&id)
}

type readSession struct {
	dial     dialFunc
	app      string
	mu       sync.Mutex // guards coord (shared by KeyBounds, Count, ReadKeys and a no-snapshot reader)
	coord    readConn
	snapshot string
	// coordBroken is set when the coordinator's connection failed: its snapshot is gone.
	coordBroken bool
	// lookups are idle connections for related-row lookups (embeds), each in the snapshot;
	// lookupSlots bounds how many exist at once.
	lookupMu    sync.Mutex
	lookups     []readConn
	lookupSlots chan struct{}
}

// MaxLookupConns bounds the connections used for embed lookups per read session (OC-021).
const MaxLookupConns = 4

// beginRead opens the coordinator: a REPEATABLE READ READ ONLY transaction that exports its
// snapshot and stays open while the mapping is read (OC-024). When export fails (e.g. a hot
// standby), the transaction is restarted without it and SnapshotID is "".
func beginRead(ctx context.Context, dial dialFunc, app string, export bool) (*readSession, error) {
	coord, err := dial(ctx, app+" coord")
	if err != nil {
		return nil, err
	}
	rs := &readSession{dial: dial, app: app, coord: coord, lookupSlots: make(chan struct{}, MaxLookupConns)}
	for _, stmt := range []string{"SET statement_timeout = 0", "SET idle_in_transaction_session_timeout = 0", beginRR} {
		if err := coord.Exec(ctx, stmt); err != nil {
			_ = coord.Close(context.WithoutCancel(ctx))
			return nil, err
		}
	}
	if !export {
		return rs, nil
	}
	rows, err := coord.Query(ctx, "SELECT pg_export_snapshot()")
	if err == nil && len(rows) == 1 && len(rows[0]) == 1 {
		rs.snapshot, _ = rows[0][0].(string)
		return rs, nil
	}
	// The failed statement aborted the transaction: start a plain one.
	if err := coord.Exec(ctx, "ROLLBACK"); err == nil {
		err = coord.Exec(ctx, beginRR)
		if err == nil {
			return rs, nil
		}
	}
	_ = coord.Close(context.WithoutCancel(ctx))
	return nil, fmt.Errorf("postgres: restart read transaction: %w", err)
}

func (r *readSession) SnapshotID() string { return r.snapshot }

func setSnapshotSQL(id string) string {
	return "SET TRANSACTION SNAPSHOT '" + strings.ReplaceAll(id, "'", "''") + "'"
}

func (r *readSession) onCoord(_ context.Context, fn func(readConn) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	err := fn(r.coord)
	if err != nil && ClassifyPgTransient(err) {
		r.coordBroken = true
		return &source.TransientError{Err: err}
	}
	return err
}

func (r *readSession) KeyBounds(ctx context.Context, t source.TableRead) (lo, hi any, err error) {
	err = r.onCoord(ctx, func(c readConn) error {
		rows, err := c.Query(ctx, BoundsSQL(t))
		if err == nil && len(rows) == 1 && len(rows[0]) == 2 {
			lo, hi = rows[0][0], rows[0][1]
		}
		return err
	})
	return lo, hi, err
}

func (r *readSession) Count(ctx context.Context, t source.TableRead) (int64, error) {
	var n int64
	err := r.onCoord(ctx, func(c readConn) error {
		rows, err := c.Query(ctx, CountSQL(t))
		if err == nil && len(rows) == 1 && len(rows[0]) == 1 {
			n, _ = rows[0][0].(int64)
		}
		return err
	})
	return n, err
}

// widen places the projected values into a row of len(Columns).
func widen(vals []any, idx []int, width int) []any {
	row := make([]any, width)
	for i, j := range idx {
		if i < len(vals) {
			row[j] = vals[i]
		}
	}
	return row
}

func (r *readSession) ReadKeys(ctx context.Context, t source.TableRead, keys [][]any) ([][]any, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	_, idx := projection(t)
	args := make([]any, len(t.Key))
	for k := range t.Key {
		col := make([]string, len(keys))
		for i, key := range keys {
			if k < len(key) {
				col[i] = keyText(key[k])
			}
		}
		args[k] = col
	}
	var out [][]any
	err := r.onCoord(ctx, func(c readConn) error {
		rows, err := c.Query(ctx, RetrySQL(t), args...)
		for _, v := range rows {
			out = append(out, widen(v, idx, len(t.Columns)))
		}
		return err
	})
	return out, err
}

// keyText renders a stored key component (decoded from JSON) as PostgreSQL input text.
func keyText(v any) string {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return x
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// partition reads [lo, hi) on its own connection that shares the coordinator's snapshot. It
// reconnects after a transient error: with the snapshot while the coordinator lives, else
// with a fresh snapshot (the pipeline records the warning).
type partition struct {
	rs     *readSession
	t      source.TableRead
	lo, hi any
	idx    []int
	conn   readConn
	shared bool // reading on the coordinator (no exported snapshot)
	resnap bool // a reconnect had to take a new snapshot
}

// Resnapshotted implements source.Resnapshotter.
func (p *partition) Resnapshotted() bool { return p.resnap }

func (r *readSession) OpenPartition(ctx context.Context, t source.TableRead, lo, hi any) (source.PartitionReader, error) {
	_, idx := projection(t)
	p := &partition{rs: r, t: t, lo: lo, hi: hi, idx: idx}
	if r.snapshot == "" {
		p.shared = true
		return p, nil
	}
	if err := p.connect(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *partition) connect(ctx context.Context) error {
	conn, err := p.rs.dial(ctx, p.t.App)
	if err != nil {
		return err
	}
	stmts := []string{beginRR}
	if p.rs.snapshot != "" && !p.rs.coordBroken {
		stmts = append(stmts, setSnapshotSQL(p.rs.snapshot))
	} else {
		p.resnap = true
	}
	stmts = append(stmts, "SET LOCAL statement_timeout = '5min'")
	for _, s := range stmts {
		if err := conn.Exec(ctx, s); err != nil {
			_ = conn.Close(context.WithoutCancel(ctx))
			return err
		}
	}
	p.conn = conn
	return nil
}

func (p *partition) ReadAfter(ctx context.Context, after []any, limit int) ([][]any, error) {
	q := KeysetQuery{After: after != nil, Lo: after == nil && p.lo != nil, Hi: p.hi != nil}
	args := make([]any, 0, len(after)+3)
	args = append(args, after...)
	if q.Lo {
		args = append(args, p.lo)
	}
	if q.Hi {
		args = append(args, p.hi)
	}
	args = append(args, limit)
	sql := KeysetSQL(p.t, q)
	var rows [][]any
	var err error
	if p.shared {
		err = p.rs.onCoord(ctx, func(c readConn) error {
			rows, err = c.Query(ctx, sql, args...)
			return err
		})
	} else {
		if p.conn == nil {
			if err := p.connect(ctx); err != nil {
				return nil, wrapRead(err)
			}
		}
		rows, err = p.conn.Query(ctx, sql, args...)
		if err != nil && ClassifyPgTransient(err) {
			_ = p.conn.Close(context.WithoutCancel(ctx))
			p.conn = nil
		}
	}
	if err != nil {
		return nil, wrapRead(err)
	}
	out := make([][]any, len(rows))
	for i, v := range rows {
		out[i] = widen(v, p.idx, len(p.t.Columns))
	}
	return out, nil
}

func wrapRead(err error) error {
	if ClassifyPgTransient(err) && !source.IsTransient(err) {
		return &source.TransientError{Err: err}
	}
	return err
}

func (p *partition) Close() error {
	if p.conn != nil {
		err := p.conn.Exec(context.Background(), "ROLLBACK")
		_ = p.conn.Close(context.Background())
		p.conn = nil
		return err
	}
	return nil
}

// cursor reads a keyless table with FETCH FORWARD (OC-024 AC6).
type cursor struct {
	rs     *readSession
	conn   readConn
	shared bool
	idx    []int
	width  int
}

func (r *readSession) OpenCursor(ctx context.Context, t source.TableRead) (source.CursorReader, error) {
	_, idx := projection(t)
	c := &cursor{rs: r, idx: idx, width: len(t.Columns)}
	if r.snapshot == "" {
		c.shared = true
		return c, r.onCoord(ctx, func(conn readConn) error { return conn.Exec(ctx, CursorSQL(t)) })
	}
	conn, err := r.dial(ctx, t.App)
	if err != nil {
		return nil, err
	}
	for _, s := range []string{beginRR, setSnapshotSQL(r.snapshot), CursorSQL(t)} {
		if err := conn.Exec(ctx, s); err != nil {
			_ = conn.Close(context.WithoutCancel(ctx))
			return nil, err
		}
	}
	c.conn = conn
	return c, nil
}

func (c *cursor) Fetch(ctx context.Context, limit int) ([][]any, error) {
	var rows [][]any
	var err error
	if c.shared {
		err = c.rs.onCoord(ctx, func(conn readConn) error {
			rows, err = conn.Query(ctx, FetchSQL(limit))
			return err
		})
	} else {
		rows, err = c.conn.Query(ctx, FetchSQL(limit))
	}
	if err != nil {
		return nil, err
	}
	out := make([][]any, len(rows))
	for i, v := range rows {
		out[i] = widen(v, c.idx, c.width)
	}
	return out, nil
}

func (c *cursor) Close() error {
	if c.shared {
		return c.rs.onCoord(context.Background(), func(conn readConn) error { return conn.Exec(context.Background(), "CLOSE "+cursorName) })
	}
	_ = c.conn.Exec(context.Background(), "ROLLBACK")
	return c.conn.Close(context.Background())
}

// ReadRelated implements source.RelatedReader: one query for a batch of join keys, on a
// connection that shares the coordinator's exported snapshot (OC-021 AC5).
func (r *readSession) ReadRelated(ctx context.Context, q source.RelatedQuery, keys [][]any) ([][]any, error) {
	var out [][]any
	err := r.StreamRelated(ctx, q, keys, func(row []any) error {
		out = append(out, row)
		return nil
	})
	return out, err
}

// StreamRelated implements source.RelatedStreamer: ReadRelated, one row at a time.
func (r *readSession) StreamRelated(ctx context.Context, q source.RelatedQuery, keys [][]any, fn func(row []any) error) error {
	if len(keys) == 0 && q.Range == nil {
		return nil
	}
	sql := RelatedSQL(q)
	args := relatedArgs(q, keys)
	_, idx := projection(q.Table)
	width := len(q.Table.Columns)
	emit := func(v []any) error { return fn(relatedRow(v, idx, width)) }
	var err error
	if r.snapshot == "" {
		err = r.onCoord(ctx, func(c readConn) error { return streamOn(ctx, c, sql, emit, args...) })
	} else {
		var conn readConn
		if conn, err = r.lookupConn(ctx); err != nil {
			return wrapRead(err)
		}
		err = streamOn(ctx, conn, sql, emit, args...)
		r.releaseLookup(ctx, conn, err != nil && ClassifyPgTransient(err))
	}
	if err != nil {
		return wrapRead(err)
	}
	return nil
}

// relatedRow aligns a related row with its table's columns; values past the projection (a
// many-to-many row's junction columns) follow in order.
func relatedRow(vals []any, idx []int, width int) []any {
	row := widen(vals, idx, width)
	if len(vals) > len(idx) {
		row = append(row, vals[len(idx):]...)
	}
	return row
}

// relatedArgs are one text array per join column (the SQL casts them to the column's type), or
// a range's two bounds, followed by the filters' values (OC-045).
func relatedArgs(q source.RelatedQuery, keys [][]any) []any {
	var args []any
	if q.Range != nil {
		args = append(args, pgText(q.Range.From), pgText(q.Range.To))
	} else {
		for k := range q.Join {
			col := make([]string, len(keys))
			for i, key := range keys {
				if k < len(key) {
					col[i] = pgText(key[k])
				}
			}
			args = append(args, col)
		}
	}
	for _, f := range q.Filter {
		switch f.Op {
		case "isNull", "notNull":
		case "in", "notIn":
			args = append(args, append([]string{}, f.Args...))
		default:
			if len(f.Args) > 0 {
				args = append(args, f.Args[0])
			} else {
				args = append(args, "")
			}
		}
	}
	return args
}

// pgText renders a driver value as PostgreSQL input text.
func pgText(v any) string {
	switch x := v.(type) {
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
	case []byte:
		return fmt.Sprintf("\\x%x", x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case fmt.Stringer:
		return x.String()
	}
	return keyText(v)
}

// lookupConn takes an idle lookup connection, or opens one inside the snapshot (at most
// MaxLookupConns at once).
func (r *readSession) lookupConn(ctx context.Context) (readConn, error) {
	select {
	case r.lookupSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	r.lookupMu.Lock()
	if n := len(r.lookups); n > 0 {
		c := r.lookups[n-1]
		r.lookups = r.lookups[:n-1]
		r.lookupMu.Unlock()
		return c, nil
	}
	r.lookupMu.Unlock()
	conn, err := r.dial(ctx, r.app+" embed")
	if err != nil {
		<-r.lookupSlots
		return nil, err
	}
	for _, stmt := range []string{beginRR, setSnapshotSQL(r.snapshot), "SET LOCAL statement_timeout = '5min'"} {
		if err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(context.WithoutCancel(ctx))
			<-r.lookupSlots
			return nil, err
		}
	}
	return conn, nil
}

// releaseLookup returns a connection to the pool, or closes it when it broke.
func (r *readSession) releaseLookup(ctx context.Context, c readConn, broken bool) {
	if broken {
		_ = c.Close(context.WithoutCancel(ctx))
	} else {
		r.lookupMu.Lock()
		r.lookups = append(r.lookups, c)
		r.lookupMu.Unlock()
	}
	<-r.lookupSlots
}

// Close ends the coordinator transaction (its snapshot goes with it).
func (r *readSession) Close() error {
	r.lookupMu.Lock()
	for _, c := range r.lookups {
		_ = c.Exec(context.Background(), "ROLLBACK")
		_ = c.Close(context.Background())
	}
	r.lookups = nil
	r.lookupMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.coord == nil {
		return nil
	}
	_ = r.coord.Exec(context.Background(), "ROLLBACK")
	err := r.coord.Close(context.Background())
	r.coord = nil
	return err
}
