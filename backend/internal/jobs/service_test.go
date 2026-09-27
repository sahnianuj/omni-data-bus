package jobs

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/store"
)

func TestJobsService_AC4_SingleActivePerProject(t *testing.T) {
	f := newFixture(t)
	v, pre, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	require.True(t, pre.OK)
	require.Equal(t, store.JobQueued, v.Status)
	require.Equal(t, []string{f.maps["customer"].ID}, v.MappingIDs)
	require.Equal(t, DefaultOptions().BatchSize, v.Options.BatchSize, "defaults persisted")

	_, _, err = f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.True(t, problem.HasCode(err, problem.CodeProjectJobRunning))

	// Once the first is cancelled (terminal), a new job may start.
	_, err = f.svc.Cancel(ctx, ws, "u1", v.ID)
	require.NoError(t, err)
	_, _, err = f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
}

func TestJobsService_ControlsAndProgressStatus(t *testing.T) {
	f := newFixture(t)
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, int64(2000), v.Progress.Total, "total starts as the row estimate")
	require.Len(t, v.Progress.Mappings, 1)
	require.Equal(t, progress.PhaseQueued, v.Progress.Mappings[0].Phase)

	// queued: pause is invalid (409), cancel is immediate.
	_, err = f.svc.Pause(ctx, ws, "u1", v.ID)
	require.True(t, problem.HasCode(err, problem.CodeJobInvalidTransition))
	c, err := f.svc.Cancel(ctx, ws, "u1", v.ID)
	require.NoError(t, err)
	require.Equal(t, store.JobCancelled, c.Status)
	require.Equal(t, store.JobCancelled, c.Progress.Status, "the progress document follows")
	require.NotNil(t, c.FinishedAt)

	// running → pausing → (worker) paused → resume → queued.
	v, _, err = f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	j, _ := f.st.Jobs().Get(ctx, ws, v.ID)
	j.Status = store.JobRunning
	j, err = f.st.Jobs().Update(ctx, j)
	require.NoError(t, err)
	p, err := f.svc.Pause(ctx, ws, "u1", v.ID)
	require.NoError(t, err)
	require.Equal(t, store.JobPausing, p.Status)
	j, _ = f.st.Jobs().Get(ctx, ws, v.ID)
	j.Status = store.JobPaused
	_, err = f.st.Jobs().Update(ctx, j)
	require.NoError(t, err)
	r, err := f.svc.Resume(ctx, ws, "u1", v.ID, "")
	require.NoError(t, err)
	require.Equal(t, store.JobQueued, r.Status)

	_, err = f.svc.Get(ctx, "other-ws", v.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestJobsService_ResumeFailedNeedsCheckpointAndFreeProject(t *testing.T) {
	f := newFixture(t)
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	j, _ := f.st.Jobs().Get(ctx, ws, v.ID)
	j.Status = store.JobFailed
	j, err = f.st.Jobs().Update(ctx, j)
	require.NoError(t, err)
	_, err = f.svc.Resume(ctx, ws, "u1", v.ID, "")
	require.True(t, problem.HasCode(err, problem.CodeJobInvalidTransition), "nothing to resume from")

	j.Checkpoint = []byte(`{"m":1}`)
	_, err = f.st.Jobs().Update(ctx, j)
	require.NoError(t, err)
	other, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	_, err = f.svc.Resume(ctx, ws, "u1", v.ID, "")
	require.True(t, problem.HasCode(err, problem.CodeProjectJobRunning))
	_, err = f.svc.Cancel(ctx, ws, "u1", other.ID)
	require.NoError(t, err)
	r, err := f.svc.Resume(ctx, ws, "u1", v.ID, "")
	require.NoError(t, err)
	require.Equal(t, store.JobQueued, r.Status)
}
