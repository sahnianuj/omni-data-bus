package pipeline

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/jobs"
)

func TestPause_AC5_DrainAndCheckpoint(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 5000), 5000, opts(100))
	f.src.delay = time.Millisecond
	drain := make(chan struct{})
	var readsAtDrain int
	var once sync.Once
	f.w.onWrite = func(stored int) {
		if stored >= 1000 {
			once.Do(func() {
				f.src.mu.Lock()
				readsAtDrain = f.src.reads
				f.src.mu.Unlock()
				close(drain)
			})
		}
	}
	o := f.run(jobs.Control{Drain: drain})
	require.False(t, o.Finished)
	require.Nil(t, o.Fatal, "a drained run ends cleanly (→ paused)")
	require.LessOrEqual(t, f.src.reads, readsAtDrain+1, "readers stop at the next batch boundary")

	// Every batch read before the drain was written and acknowledged: the final checkpoint is
	// exactly the last contiguous ack, and it matches what was written.
	cp, err := ParseCheckpoint(o.Checkpoint)
	require.NoError(t, err)
	m := cp.Mappings[f.job.MappingIDs[0]]
	require.EqualValues(t, f.w.count(), m.Written)
	require.EqualValues(t, f.w.count()/100, m.Partitions[0].Seq)
	require.Equal(t, []any{int64(f.w.count())}, m.Partitions[0].LastKey)
	require.Equal(t, "in_progress", m.State)
}

func TestPause_AC5_DrainTimeout(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 5000), 5000, opts(100))
	f.runner.d.DrainTimeout = 150 * time.Millisecond
	// Batches reach the writers in order, and stuck writers never starve batch 5.
	f.runner.d.TransformWorkers, f.runner.d.WriterWorkers = 1, 8
	drain := make(chan struct{})
	// Batches after the 5th (rows > 500) get stuck until the run is cancelled; the pause is
	// requested when the first of them arrives.
	var once sync.Once
	f.w.block = func(ctx context.Context, docs []target.Doc) {
		id, _ := strconv.Atoi(strings.TrimPrefix(docs[0].Key, "person::"))
		if id > 500 {
			once.Do(func() { close(drain) })
			<-ctx.Done()
		}
	}
	start := time.Now()
	o := f.run(jobs.Control{Drain: drain})
	require.Less(t, time.Since(start), 5*time.Second, "cancelled after the drain timeout")
	require.False(t, o.Finished)
	require.Nil(t, o.Fatal)
	cp, _ := ParseCheckpoint(o.Checkpoint)
	m := cp.Mappings[f.job.MappingIDs[0]]
	require.EqualValues(t, 5, m.Partitions[0].Seq, "the stuck batches aren't in the checkpoint")
	require.EqualValues(t, 500, m.Written)
	require.Zero(t, m.Failed, "cancelled writes aren't recorded as failures")
	evs, _ := f.st.Jobs().ListEvents(context.Background(), ws, f.job.ID)
	require.True(t, hasEvent(evs, "warning", "were cancelled"))

	// Resume writes the rest with nothing missing.
	f.job.Checkpoint = o.Checkpoint
	f.w.block = nil
	o = f.run(jobs.Control{})
	require.True(t, o.Finished)
	require.Equal(t, 5000, f.w.count())
}
