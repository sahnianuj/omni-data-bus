package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/jobs"
	"omnidatabus/internal/pipeline/flowctl"
	"omnidatabus/internal/store"
)

func TestCheckpoint_TypedKeysRoundTrip(t *testing.T) {
	ts := time.Date(2026, 9, 1, 12, 0, 0, 123, time.UTC)
	cp := Checkpoint{ErrorSeq: 7, Mappings: map[string]*MappingCheckpoint{"m1": {Partitions: []PartitionState{
		{Lo: nil, Hi: int64(500), Seq: 3, LastKey: []any{int64(299)}},
		{Lo: int64(500), Hi: nil, Seq: 0},
		{Lo: [16]byte{0x40}, Hi: [16]byte{0x80}, Seq: 2, LastKey: []any{[16]byte{0x41, 1}, "text", ts, 1.5}, Done: true},
	}, Written: 10}}}
	raw, err := json.Marshal(cp)
	require.NoError(t, err)
	back, err := ParseCheckpoint(raw)
	require.NoError(t, err)
	require.Equal(t, cp, back)
	empty, err := ParseCheckpoint(nil)
	require.NoError(t, err)
	require.Empty(t, empty.Mappings)
}

func TestCheckpointer_AdvancesOnlyOverContiguousAcks(t *testing.T) {
	m := &MappingCheckpoint{Partitions: []PartitionState{{}, {}}}
	c := newCheckpointer(m)
	c.ack(0, 2, []any{int64(200)}, 100, 0, 0) // batch 2 finished before batch 1
	require.EqualValues(t, 0, c.snapshot().Partitions[0].Seq, "can't skip batch 1")
	require.Zero(t, c.snapshot().Written, "counts follow the checkpoint")
	c.ack(0, 1, []any{int64(100)}, 99, 0, 1)
	require.EqualValues(t, 2, c.snapshot().Partitions[0].Seq)
	require.Equal(t, []any{int64(200)}, c.snapshot().Partitions[0].LastKey)
	c.ack(1, 1, []any{int64(900)}, 5, 2, 0)
	c.partitionDone(1)
	s := c.snapshot()
	require.True(t, s.Partitions[1].Done)
	require.EqualValues(t, 1, s.Partitions[1].Seq)
	require.EqualValues(t, 204, s.Written)
	require.EqualValues(t, 2, s.Skipped)
	require.EqualValues(t, 1, s.Failed)
}

func TestCheckpointer_AC1_ContiguousAdvance(t *testing.T) {
	c := newCheckpointer(&MappingCheckpoint{Partitions: []PartitionState{{}}})
	key := func(seq uint64) []any { return []any{int64(seq * 100)} }
	for _, seq := range []uint64{1, 2, 4, 5} {
		c.ack(0, seq, key(seq), 100, 0, 0)
	}
	require.Equal(t, key(2), c.snapshot().Partitions[0].LastKey, "batch 3 is still in flight")
	require.Equal(t, 3, c.ack(0, 3, key(3), 100, 0, 0), "3 closes the gap and advances over 3, 4 and 5")
	require.Equal(t, key(5), c.snapshot().Partitions[0].LastKey)
	require.EqualValues(t, 500, c.snapshot().Written)
}

func TestCheckpointer_AC1_PropertyRandomAckOrder(t *testing.T) {
	rng := rand.New(rand.NewPCG(29, 1))
	for run := 0; run < 200; run++ {
		n := 1 + rng.IntN(60)
		c := newCheckpointer(&MappingCheckpoint{Partitions: []PartitionState{{}}})
		acked := map[uint64]bool{}
		for _, i := range rng.Perm(n) {
			seq := uint64(i + 1)
			c.ack(0, seq, []any{int64(seq)}, 1, 0, 0)
			acked[seq] = true
			// Invariant: every batch up to the position is acknowledged, and the position is the
			// longest acknowledged prefix.
			s := c.snapshot().Partitions[0]
			for k := uint64(1); k <= s.Seq; k++ {
				require.True(t, acked[k], "run %d: position %d covers unacked batch %d", run, s.Seq, k)
			}
			require.False(t, acked[s.Seq+1], "run %d: the position stops short of an acked batch", run)
			if s.Seq > 0 {
				require.Equal(t, []any{int64(s.Seq)}, s.LastKey)
			}
			require.EqualValues(t, s.Seq, c.snapshot().Written, "counts follow the position")
		}
		require.EqualValues(t, n, c.snapshot().Partitions[0].Seq)
	}
}

// persistLog records the order of flushes and saves.
type persistLog struct {
	mu    sync.Mutex
	calls []string
	saves []time.Time
	err   error
}

func (l *persistLog) persister(clock *flowctl.FakeClock) *persister {
	return newPersister(clock.Now,
		func(context.Context) { l.mu.Lock(); l.calls = append(l.calls, "flush"); l.mu.Unlock() },
		func(context.Context, json.RawMessage) error {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.calls = append(l.calls, "save")
			l.saves = append(l.saves, clock.Now())
			return l.err
		},
		func() json.RawMessage { return json.RawMessage(`{}`) })
}

func TestCheckpointer_AC2_Cadence(t *testing.T) {
	clock := flowctl.NewFakeClock()
	start := clock.Now()
	var log persistLog
	p := log.persister(clock)
	// One batch every 500 ms for 12 s: well under 50 batches per 5 s, so time drives it.
	for i := 0; i < 24; i++ {
		clock.Advance(500 * time.Millisecond)
		require.NoError(t, p.advanced(context.Background(), 1, false))
	}
	require.Len(t, log.saves, 2)
	require.Equal(t, 5*time.Second, log.saves[0].Sub(start))
	require.Equal(t, 10*time.Second, log.saves[1].Sub(start))
	// A finished partition persists at once.
	clock.Advance(100 * time.Millisecond)
	require.NoError(t, p.advanced(context.Background(), 0, true))
	require.Len(t, log.saves, 3)
	// 50 contiguous batches persist without waiting for the clock.
	require.NoError(t, p.advanced(context.Background(), 49, false))
	require.Len(t, log.saves, 3)
	require.NoError(t, p.advanced(context.Background(), 1, false))
	require.Len(t, log.saves, 4)
	require.JSONEq(t, `{}`, string(p.durable()))
}

func TestCheckpointer_AC2_Fenced(t *testing.T) {
	var log persistLog
	log.err = ErrLostOwnership
	p := log.persister(flowctl.NewFakeClock())
	require.ErrorIs(t, p.persist(context.Background()), ErrLostOwnership)
	require.Nil(t, p.durable(), "nothing counts as persisted")

	// In the pipeline: another worker claims the job mid-run, so the next persist fails and
	// the run aborts with LOST_OWNERSHIP.
	f := newRunFixture(t, peopleRows(1, 20000), 20000, opts(100))
	f.runner.d.PersistBatches = 5
	f.src.delay = time.Millisecond
	var once sync.Once
	f.w.onWrite = func(stored int) {
		if stored >= 1000 {
			once.Do(func() {
				j, _ := f.st.Jobs().GetAny(context.Background(), f.job.ID)
				j.ClaimedBy = "other-worker"
				_, _ = f.st.Jobs().Update(context.Background(), j)
			})
		}
	}
	o := f.run(jobs.Control{})
	require.NotNil(t, o.Fatal)
	require.Equal(t, CodeLostOwnership, o.Fatal.Code)
	require.Less(t, f.w.count(), 20000, "stopped early")
}

func TestCheckpointer_AC2_ErrorsFlushedBeforePersist(t *testing.T) {
	var log persistLog
	p := log.persister(flowctl.NewFakeClock())
	require.NoError(t, p.persist(context.Background()))
	require.NoError(t, p.advanced(context.Background(), 50, false))
	require.Equal(t, []string{"flush", "save", "flush", "save"}, log.calls)

	// In the pipeline: every failed row the stored checkpoint covers is in job_errors.
	bad := []int32{}
	for i := int32(5); i <= 3000; i += 37 {
		bad = append(bad, i)
	}
	f := newRunFixture(t, peopleRows(1, 3000, bad...), 3000, opts(100))
	f.runner.d.PersistBatches = 2
	checked := 0
	var mu sync.Mutex
	check := func() {
		mu.Lock()
		defer mu.Unlock()
		j, _ := f.st.Jobs().GetAny(context.Background(), f.job.ID)
		cp, err := ParseCheckpoint(j.Checkpoint)
		if err != nil || len(cp.Mappings) == 0 {
			return
		}
		errs, _, _ := f.st.Jobs().ListErrors(context.Background(), ws, f.job.ID, store.JobErrorFilter{}, store.Page{Limit: 1000})
		require.GreaterOrEqual(t, int64(len(errs)), cp.Mappings[f.job.MappingIDs[0]].Failed, "stored checkpoint ahead of stored errors")
		require.GreaterOrEqual(t, int64(len(errs)), cp.ErrorSeq, "every numbered error the checkpoint covers is stored")
		checked++
	}
	f.w.onWrite = func(int) { check() }
	o := f.run(jobs.Control{})
	require.True(t, o.Finished)
	// Under load every write can finish before a checkpoint is stored; the final one is checked too.
	check()
	require.Positive(t, checked)
}

func TestCheckpoint_AC2_StoredFormat(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 500), 500, opts(100))
	o := f.run(jobs.Control{})
	var doc map[string]any
	require.NoError(t, json.Unmarshal(o.Checkpoint, &doc))
	require.EqualValues(t, 1, doc["version"])
	require.NotEmpty(t, doc["updatedAt"])
	m := doc["mappings"].(map[string]any)[f.job.MappingIDs[0]].(map[string]any)
	require.Equal(t, "done", m["state"])
	require.Equal(t, "keyset", m["strategy"])
	j, err := f.st.Jobs().GetAny(context.Background(), f.job.ID)
	require.NoError(t, err)
	require.NotEmpty(t, j.Checkpoint, "persisted during the run, not only at the end")
	require.False(t, errors.Is(nil, ErrLostOwnership))
}
