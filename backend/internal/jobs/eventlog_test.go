package jobs

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

func TestJobEventLog_AC7_AppendAndList(t *testing.T) {
	f := newFixture(t)
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	w, runner, id := func() (*Worker, *fakeRunner, string) {
		runner := newFakeRunner()
		w := NewWorker(fastConfig("a"), f.st, runner, f.clock.Now, nil) // one clock for service and worker
		require.Equal(t, 1, w.ClaimOnce(ctx))
		return w, runner, v.ID
	}()
	defer w.Shutdown()
	_, err = f.svc.Pause(ctx, ws, "u1", id)
	require.NoError(t, err)
	waitStatus(t, f.st, id, store.JobPaused)
	require.NotNil(t, runner.exec(id))
	_, err = f.svc.Resume(ctx, ws, "u1", id, "")
	require.NoError(t, err)

	evs, err := f.svc.Events(ctx, ws, id)
	require.NoError(t, err)
	var kinds []string
	for i, e := range evs {
		kinds = append(kinds, e.Kind)
		if i > 0 {
			require.False(t, e.At.Before(evs[i-1].At), "chronological")
		}
	}
	require.Equal(t, []string{"created", "claimed", "pause", "drained", "resume"}, kinds)
	require.Equal(t, "Paused; progress saved", evs[3].Message)
	_, err = f.svc.Events(ctx, "ws-other", id)
	require.ErrorIs(t, err, store.ErrNotFound)
}
