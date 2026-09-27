package pipeline

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/pipeline/flowctl"
)

// memTable is a fake source table of int keys 1..n with rows [id, "row-<id>"].
type memTable struct{ n int64 }

type memPartition struct {
	mu     sync.Mutex
	t      memTable
	lo, hi any
	calls  []any       // the `after` argument of every ReadAfter
	failAt map[int]int // call index → how many times it still fails
	reads  int
	errFn  func() error
}

func (p *memPartition) ReadAfter(_ context.Context, after []any, limit int) ([][]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var a any
	if after != nil {
		a = after[0]
	}
	p.calls = append(p.calls, a)
	if n := p.failAt[p.reads]; n > 0 {
		p.failAt[p.reads] = n - 1
		return nil, &source.TransientError{Err: &pgconn.PgError{Code: "08006", Message: "connection reset"}}
	}
	if p.errFn != nil {
		if err := p.errFn(); err != nil {
			return nil, err
		}
	}
	p.reads++
	start := int64(1)
	if lo, ok := p.lo.(int64); ok {
		start = lo
	}
	if after != nil {
		start = after[0].(int64) + 1
	}
	end := p.t.n + 1
	if hi, ok := p.hi.(int64); ok && hi < end {
		end = hi
	}
	var rows [][]any
	for id := start; id < end && len(rows) < limit; id++ {
		rows = append(rows, []any{id, "row"})
	}
	return rows, nil
}

func (p *memPartition) Close() error { return nil }

func drainAll(ch <-chan *source.Batch) []*source.Batch {
	var out []*source.Batch
	for b := range ch {
		out = append(out, b)
	}
	return out
}

func noSleep() ReadConfig {
	return ReadConfig{BatchSize: 1000, Clock: flowctl.NewFakeClock()}
}

func runPartitions(t *testing.T, table memTable, k int) []*source.Batch {
	t.Helper()
	ranges := PlanPartitions(KeyInt, int64(1), table.n, k)
	out := make(chan *source.Batch, RowsChanCap)
	var wg sync.WaitGroup
	for i, r := range ranges {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := &memPartition{t: table, lo: r.Lo, hi: r.Hi, failAt: map[int]int{}}
			require.NoError(t, ReadPartition(context.Background(), p, i, []int{0}, nil, 0, noSleep(), nil, out))
		}()
	}
	go func() { wg.Wait(); close(out) }()
	return drainAll(out)
}

func TestReaders_AC4_CompleteNoDuplicates(t *testing.T) {
	batches := runPartitions(t, memTable{n: 25_003}, 3)
	seen := map[int64]bool{}
	for _, b := range batches {
		for _, r := range b.Rows {
			id := r[0].(int64)
			require.False(t, seen[id], "duplicate %d", id)
			seen[id] = true
		}
	}
	require.Len(t, seen, 25_003)
}

func TestReaders_AC4_SeqContiguousPerPartition(t *testing.T) {
	batches := runPartitions(t, memTable{n: 25_003}, 3)
	byPart := map[int][]*source.Batch{}
	for _, b := range batches {
		byPart[b.Partition] = append(byPart[b.Partition], b)
	}
	require.Len(t, byPart, 3)
	for part, bs := range byPart {
		sort.Slice(bs, func(i, j int) bool { return bs[i].Seq < bs[j].Seq })
		var last int64
		for i, b := range bs {
			require.EqualValues(t, i+1, b.Seq, "partition %d seq contiguous from 1", part)
			k := b.LastKey[0].(int64)
			require.Greater(t, k, last, "LastKey increases")
			require.Equal(t, b.Rows[len(b.Rows)-1][0], b.LastKey[0])
			last = k
		}
	}
}

func TestReader_AC7_RetriesSameKeyset(t *testing.T) {
	// The 3rd read (batch 3) fails twice with a connection reset, then succeeds.
	p := &memPartition{t: memTable{n: 5000}, failAt: map[int]int{2: 2}}
	out := make(chan *source.Batch, 100)
	cfg := noSleep()
	var retries []int
	cfg.OnRetry = func(_, attempt int, _ error) { retries = append(retries, attempt) }
	require.NoError(t, ReadPartition(context.Background(), p, 0, []int{0}, nil, 0, cfg, nil, out))
	close(out)
	batches := drainAll(out)
	require.Len(t, batches, 5)
	require.Equal(t, []int{1, 2}, retries)
	require.Equal(t, []any{nil, int64(1000), int64(2000), int64(2000), int64(2000), int64(3000), int64(4000), int64(5000)}, p.calls,
		"the failed keyset query is retried with the same $1")
	total := 0
	for i, b := range batches {
		require.EqualValues(t, i+1, b.Seq)
		require.EqualValues(t, i*1000+1, b.Rows[0][0], "no gap")
		total += len(b.Rows)
	}
	require.Equal(t, 5000, total)
	require.Len(t, cfg.Clock.(*flowctl.FakeClock).Slept, 2, "backed off between attempts")
}

func TestReader_AC7_GivesUpAfterMaxRetries(t *testing.T) {
	p := &memPartition{t: memTable{n: 5000}, failAt: map[int]int{1: 6}}
	out := make(chan *source.Batch, 100)
	err := ReadPartition(context.Background(), p, 2, []int{0}, nil, 0, noSleep(), nil, out)
	var re *ReadError
	require.ErrorAs(t, err, &re)
	require.Equal(t, 2, re.Partition)
	require.Contains(t, err.Error(), CodeSourceReadFailed)
	require.Len(t, p.calls, 1+6, "1 success, then 6 failed attempts")

	// A non-transient error fails at once.
	perm := &memPartition{t: memTable{n: 10}, failAt: map[int]int{}, errFn: func() error {
		return &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}
	}}
	err = ReadPartition(context.Background(), perm, 0, []int{0}, nil, 0, noSleep(), nil, out)
	require.ErrorAs(t, err, &re)
	require.Len(t, perm.calls, 1)
}

func TestReader_AC8_RateLimited(t *testing.T) {
	clock := flowctl.NewFakeClock()
	cfg := ReadConfig{BatchSize: 100, Clock: clock, Limiter: flowctl.NewTokenBucket(1000, 100, clock)}
	p := &memPartition{t: memTable{n: 1_000_000}, failAt: map[int]int{}}
	out := make(chan *source.Batch, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ReadPartition(ctx, p, 0, []int{0}, nil, 0, cfg, nil, out) }()
	start := clock.Now()
	rows := 0
	for b := range out {
		if clock.Now().Sub(start) > 5*time.Second {
			break
		}
		rows += len(b.Rows)
	}
	cancel()
	require.LessOrEqual(t, rows, 5100)
	require.GreaterOrEqual(t, rows, 4000)
}

func TestReader_ResumesAfterCheckpointAndDrains(t *testing.T) {
	p := &memPartition{t: memTable{n: 3000}, failAt: map[int]int{}}
	out := make(chan *source.Batch, 10)
	require.NoError(t, ReadPartition(context.Background(), p, 0, []int{0}, []any{int64(1000)}, 1, noSleep(), nil, out))
	close(out)
	bs := drainAll(out)
	require.Len(t, bs, 2)
	require.EqualValues(t, 2, bs[0].Seq, "seq continues after the checkpointed one")
	require.EqualValues(t, 1001, bs[0].Rows[0][0])

	stop := make(chan struct{})
	close(stop)
	err := ReadPartition(context.Background(), &memPartition{t: memTable{n: 10}}, 0, []int{0}, nil, 0, noSleep(), stop, make(chan *source.Batch, 1))
	require.ErrorIs(t, err, ErrDrained)
}

type memCursor struct {
	rows [][]any
	pos  int
}

func (c *memCursor) Fetch(_ context.Context, limit int) ([][]any, error) {
	end := min(c.pos+limit, len(c.rows))
	out := c.rows[c.pos:end]
	c.pos = end
	return out, nil
}
func (c *memCursor) Close() error { return nil }

func TestCursorReader_AC6_ReadsAllNoCheckpoint(t *testing.T) {
	c := &memCursor{}
	for i := range 2500 {
		c.rows = append(c.rows, []any{"url", i})
	}
	out := make(chan *source.Batch, 10)
	require.NoError(t, ReadCursor(context.Background(), c, noSleep(), nil, out))
	close(out)
	bs := drainAll(out)
	require.Len(t, bs, 3)
	n := 0
	for i, b := range bs {
		require.EqualValues(t, i+1, b.Seq)
		require.Nil(t, b.LastKey, "no checkpoint position")
		n += len(b.Rows)
	}
	require.Equal(t, 2500, n)
}

func TestMemoryBudget_AC5_AdjustsBatchSize(t *testing.T) {
	require.Equal(t, 1000, BatchSizeFor(1000, 200, 1, 256<<20), "small rows fit")
	// 64 KB rows: (4+8+8+4) buffered batches × 1000 × 64 KB ≈ 1.5 GB > 256 MB.
	got := BatchSizeFor(1000, 64<<10, 8, 256<<20)
	require.Less(t, got, 1000)
	require.LessOrEqual(t, int64(got)*24*(64<<10), int64(256<<20))
	require.Equal(t, MinBatch, BatchSizeFor(1000, 10<<20, 8, 256<<20), "never below the minimum")
	require.Equal(t, 1000, BatchSizeFor(1000, 0, 1, 256<<20), "unknown row size")
}
