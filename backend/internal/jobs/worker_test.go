package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

// queue creates n queued jobs in n projects of the fixture's workspace.
func queue(t *testing.T, f *fixture, n int) []string {
	t.Helper()
	var ids []string
	for i := 0; i < n; i++ {
		p, err := f.st.Projects().Create(ctx, ws, store.NewProject{Name: fmt.Sprintf("q%d", i)})
		require.NoError(t, err)
		j, err := f.st.Jobs().Create(ctx, ws, store.Job{ProjectID: p.ID, Options: json.RawMessage(`{}`)})
		require.NoError(t, err)
		f.clock.Advance(time.Millisecond)
		ids = append(ids, j.ID)
	}
	return ids
}

func TestClaimer_AC5_RespectsCapacity(t *testing.T) {
	f := newFixture(t)
	ids := queue(t, f, 3)
	runner := newFakeRunner()
	a := NewWorker(fastConfig("a"), f.st, runner, f.clock.Now, nil)
	b := NewWorker(fastConfig("b"), f.st, runner, f.clock.Now, nil)
	a.cfg.MaxJobs, b.cfg.MaxJobs = 1, 1

	require.Equal(t, 1, a.ClaimOnce(ctx))
	require.Equal(t, 0, a.ClaimOnce(ctx), "a is full")
	require.Equal(t, 1, b.ClaimOnce(ctx))
	require.Equal(t, 0, b.ClaimOnce(ctx))

	owners := map[string]string{}
	for _, id := range ids {
		j, _ := f.st.Jobs().GetAny(ctx, id)
		owners[id] = j.ClaimedBy
	}
	require.Equal(t, "a", owners[ids[0]], "oldest first")
	require.Equal(t, "b", owners[ids[1]])
	require.Empty(t, owners[ids[2]], "still queued")
	require.Len(t, runner.startedIDs(2), 2)

	// Finishing a's job frees its slot.
	runner.exec(ids[0]).finish <- Outcome{Finished: true}
	waitStatus(t, f.st, ids[0], store.JobCompleted)
	require.Eventually(t, func() bool { return a.Running() == 0 }, time.Second, 5*time.Millisecond)
	require.Equal(t, 1, a.ClaimOnce(ctx))
	j, _ := f.st.Jobs().GetAny(ctx, ids[2])
	require.Equal(t, "a", j.ClaimedBy)
	a.Shutdown()
	b.Shutdown()
}

func TestClaimer_AC5_ConcurrentWorkersNoDoubleClaim(t *testing.T) {
	f := newFixture(t)
	ids := queue(t, f, 3)
	runner := newFakeRunner()
	workers := []*Worker{
		NewWorker(fastConfig("a"), f.st, runner, f.clock.Now, nil),
		NewWorker(fastConfig("b"), f.st, runner, f.clock.Now, nil),
	}
	done := make(chan int, len(workers))
	for _, w := range workers {
		go func() { done <- w.ClaimOnce(ctx) }()
	}
	total := <-done + <-done
	require.Equal(t, 3, total)
	seen := map[string]int{}
	for _, id := range runner.startedIDs(3) {
		seen[id]++
	}
	for _, id := range ids {
		require.Equal(t, 1, seen[id], "job %s started exactly once", id)
	}
	for _, w := range workers {
		require.LessOrEqual(t, w.Running(), 2)
		w.Shutdown()
	}
}

func TestHeartbeat_AC6_LostOwnershipAborts(t *testing.T) {
	f := newFixture(t)
	ids := queue(t, f, 1)
	runner := newFakeRunner()
	w := NewWorker(fastConfig("a"), f.st, runner, f.clock.Now, nil)
	require.Equal(t, 1, w.ClaimOnce(ctx))
	// The reaper gave the job to another worker behind our back.
	j, _ := f.st.Jobs().GetAny(ctx, ids[0])
	j.ClaimedBy = "b"
	_, err := f.st.Jobs().Update(ctx, j)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return runner.exec(ids[0]).stopped() == "abort" }, time.Second, time.Millisecond,
		"aborted within one status poll")
	require.Eventually(t, func() bool { return w.Running() == 0 }, time.Second, time.Millisecond)
	j, _ = f.st.Jobs().GetAny(ctx, ids[0])
	require.Equal(t, store.JobRunning, j.Status, "the fenced-out worker records nothing")
	require.Equal(t, "b", j.ClaimedBy)
}

func TestHeartbeat_WritesHeartbeatAndCheckpoint(t *testing.T) {
	f := newFixture(t)
	ids := queue(t, f, 1)
	runner := newFakeRunner()
	w := NewWorker(fastConfig("a"), f.st, runner, time.Now, nil)
	require.Equal(t, 1, w.ClaimOnce(ctx))
	first, _ := f.st.Jobs().GetAny(ctx, ids[0])
	require.Eventually(t, func() bool {
		j, _ := f.st.Jobs().GetAny(ctx, ids[0])
		return j.HeartbeatAt != nil && j.HeartbeatAt.After(*first.HeartbeatAt) && string(j.Checkpoint) == `{"pos":1}`
	}, 2*time.Second, 5*time.Millisecond)
	runner.exec(ids[0]).finish <- Outcome{Finished: true}
	waitStatus(t, f.st, ids[0], store.JobCompleted)
}

func TestReaper_AC6_RequeuesStale(t *testing.T) {
	f := newFixture(t)
	ids := queue(t, f, 2)
	for i, id := range ids {
		j, _ := f.st.Jobs().GetAny(ctx, id)
		hb := f.clock.Now()
		j.Status, j.ClaimedBy, j.HeartbeatAt = store.JobRunning, "dead", &hb
		j.Checkpoint = json.RawMessage(`{"pos":42}`)
		if i == 1 {
			fresh := hb.Add(25 * time.Second)
			j.HeartbeatAt = &fresh
		}
		_, err := f.st.Jobs().Update(ctx, j)
		require.NoError(t, err)
	}
	f.clock.Advance(31 * time.Second)
	w := NewWorker(fastConfig("r"), f.st, newFakeRunner(), f.clock.Now, nil)
	n, err := w.ReapOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	j, _ := f.st.Jobs().GetAny(ctx, ids[0])
	require.Equal(t, store.JobQueued, j.Status)
	require.Empty(t, j.ClaimedBy)
	require.JSONEq(t, `{"pos":42}`, string(j.Checkpoint), "checkpoint preserved")
	j, _ = f.st.Jobs().GetAny(ctx, ids[1])
	require.Equal(t, store.JobRunning, j.Status, "fresh heartbeat untouched")

	// Only the lease holder reaps.
	other := NewWorker(fastConfig("r2"), f.st, newFakeRunner(), f.clock.Now, nil)
	n, err = other.ReapOnce(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestReaper_AC6_PausingBecomesPaused(t *testing.T) {
	f := newFixture(t)
	ids := queue(t, f, 2)
	for i, id := range ids {
		j, _ := f.st.Jobs().GetAny(ctx, id)
		hb := f.clock.Now()
		j.Status, j.ClaimedBy, j.HeartbeatAt = []string{store.JobPausing, store.JobCancelling}[i], "dead", &hb
		_, err := f.st.Jobs().Update(ctx, j)
		require.NoError(t, err)
	}
	f.clock.Advance(time.Minute)
	w := NewWorker(fastConfig("r"), f.st, newFakeRunner(), f.clock.Now, nil)
	n, err := w.ReapOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	j, _ := f.st.Jobs().GetAny(ctx, ids[0])
	require.Equal(t, store.JobPaused, j.Status)
	j, _ = f.st.Jobs().GetAny(ctx, ids[1])
	require.Equal(t, store.JobCancelled, j.Status)
	_, err = f.st.Jobs().ActiveForProject(ctx, ws, j.ProjectID)
	require.ErrorIs(t, err, store.ErrNotFound, "a cancelled job releases the project")
}

// startJob creates a job through the service and has worker "a" claim it.
func startJob(t *testing.T, f *fixture) (*Worker, *fakeRunner, string) {
	t.Helper()
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	runner := newFakeRunner()
	w := NewWorker(fastConfig("a"), f.st, runner, time.Now, nil)
	require.Equal(t, 1, w.ClaimOnce(ctx))
	return w, runner, v.ID
}

func TestControl_AC7_PauseDrainsAndCheckpoints(t *testing.T) {
	f := newFixture(t)
	w, runner, id := startJob(t, f)
	defer w.Shutdown()
	_, err := f.svc.Pause(ctx, ws, "u1", id)
	require.NoError(t, err)
	j := waitStatus(t, f.st, id, store.JobPaused)
	require.Equal(t, "drain", runner.exec(id).stopped(), "pause drains (graceful), not abort")
	require.JSONEq(t, `{"pos":"drained"}`, string(j.Checkpoint))
	require.Empty(t, j.ClaimedBy)
	v, err := f.svc.Get(ctx, ws, id)
	require.NoError(t, err)
	require.Equal(t, store.JobPaused, v.Progress.Status)
	require.True(t, v.Resumable)
}

func TestControl_AC7_ResumeRequeues(t *testing.T) {
	f := newFixture(t)
	w, _, id := startJob(t, f)
	_, err := f.svc.Pause(ctx, ws, "u1", id)
	require.NoError(t, err)
	waitStatus(t, f.st, id, store.JobPaused)
	w.Shutdown()

	r, err := f.svc.Resume(ctx, ws, "u1", id, "")
	require.NoError(t, err)
	require.Equal(t, store.JobQueued, r.Status)
	runner := newFakeRunner()
	w2 := NewWorker(fastConfig("b"), f.st, runner, time.Now, nil)
	defer w2.Shutdown()
	require.Equal(t, 1, w2.ClaimOnce(ctx))
	require.JSONEq(t, `{"pos":"drained"}`, string(runner.exec(id).job.Checkpoint), "the run continues from the checkpoint")
}

func TestControl_AC7_CancelFast(t *testing.T) {
	f := newFixture(t)
	w, runner, id := startJob(t, f)
	defer w.Shutdown()
	start := time.Now()
	c, err := f.svc.Cancel(ctx, ws, "u1", id)
	require.NoError(t, err)
	require.Equal(t, store.JobCancelling, c.Status)
	j := waitStatus(t, f.st, id, store.JobCancelled)
	require.Less(t, time.Since(start), 5*time.Second)
	require.Equal(t, "abort", runner.exec(id).stopped())
	require.Equal(t, ReasonCancelled, j.Reason)
	_, err = f.st.Jobs().ActiveForProject(ctx, ws, f.project.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestControl_AC7_StatusPollDetectsPause(t *testing.T) {
	f := newFixture(t)
	w, runner, id := startJob(t, f)
	defer w.Shutdown()
	// A status change written by any API node (here: straight to the store) is picked up.
	j, _ := f.st.Jobs().GetAny(ctx, id)
	j.Status = store.JobPausing
	_, err := f.st.Jobs().Update(ctx, j)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return runner.exec(id).stopped() == "drain" }, time.Second, time.Millisecond)
	waitStatus(t, f.st, id, store.JobPaused)
}

func TestControl_FatalFailsWithReason(t *testing.T) {
	f := newFixture(t)
	w, runner, id := startJob(t, f)
	defer w.Shutdown()
	runner.exec(id).finish <- Outcome{Failed: 3, Fatal: &Fatal{Code: ReasonErrorThreshold, Message: "3 of 3 rows failed"}, Checkpoint: json.RawMessage(`{"pos":3}`)}
	j := waitStatus(t, f.st, id, store.JobFailed)
	require.Equal(t, ReasonErrorThreshold, j.Reason)
	require.Equal(t, "3 of 3 rows failed", j.ReasonDetail)
	evs, _ := f.st.Jobs().ListEvents(ctx, ws, id)
	require.Equal(t, "Failed: 3 of 3 rows failed", evs[len(evs)-1].Message)
}

func TestWorker_AC8_ShutdownRequeues(t *testing.T) {
	f := newFixture(t)
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	runner := newFakeRunner()
	w := NewWorker(fastConfig("a"), f.st, runner, time.Now, nil)
	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() { _ = w.Run(runCtx); close(stopped) }()
	waitStatus(t, f.st, v.ID, store.JobRunning)

	cancel() // SIGTERM
	<-stopped
	j, _ := f.st.Jobs().GetAny(ctx, v.ID)
	require.Equal(t, store.JobQueued, j.Status, "handed back, not failed")
	require.Empty(t, j.ClaimedBy)
	require.JSONEq(t, `{"pos":"drained"}`, string(j.Checkpoint))
	require.Equal(t, "drain", runner.exec(v.ID).stopped())
	require.Zero(t, w.ClaimOnce(ctx), "a stopped worker claims nothing")
}
