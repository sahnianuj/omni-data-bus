package pipeline

import (
	"context"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/jobs"
	"omnidatabus/internal/race"
)

// TestCrashRecovery_AC4_NoLossProperty kills a run at random points (abort: no drain, no final
// checkpoint) and resumes from what was persisted: every source row ends up written, and the
// only duplicates are rows written after the last persisted checkpoint.
func TestCrashRecovery_AC4_NoLossProperty(t *testing.T) {
	rows, crashes := 100_000, 50
	if race.Enabled || testing.Short() {
		rows, crashes = 10_000, 20
	}
	all := peopleRows(1, int32(rows))
	rng := rand.New(rand.NewPCG(4, 29))
	for c := 0; c < crashes; c++ {
		crashAt := 1 + rng.IntN(rows-1)
		// Partitioned reads make acks arrive out of order across readers too.
		f := newRunFixture(t, all, 1_500_000, opts(1000))
		f.runner.d.PersistBatches = 1 + rng.IntN(8)
		f.runner.d.WriterWorkers = 4

		abort := make(chan struct{})
		var once sync.Once
		f.w.onWrite = func(stored int) {
			if stored >= crashAt {
				once.Do(func() { close(abort) })
			}
		}
		o := f.run(jobs.Control{Abort: abort})
		require.False(t, o.Finished, "crash %d at %d", c, crashAt)
		firstRun := f.w.count()

		// The crashed worker wrote nothing more: resume from the persisted checkpoint only.
		stored, err := f.st.Jobs().GetAny(context.Background(), f.job.ID)
		require.NoError(t, err)
		var covered int64
		if len(stored.Checkpoint) > 0 {
			cp, err := ParseCheckpoint(stored.Checkpoint)
			require.NoError(t, err)
			for _, m := range cp.Mappings {
				covered += m.Processed()
			}
		}
		f.job.Checkpoint = stored.Checkpoint
		f.w.onWrite = nil
		o = f.run(jobs.Control{})
		require.True(t, o.Finished, "crash %d: resumed run finishes", c)

		require.Equal(t, rows, f.w.count(), "crash %d at %d: no row lost", c, crashAt)
		dups := 0
		for _, n := range f.w.writes {
			dups += n - 1
		}
		require.LessOrEqual(t, int64(dups), int64(firstRun)-covered,
			"crash %d: duplicates only between the persisted checkpoint (%d rows) and the crash (%d written)", c, covered, firstRun)
	}
}
