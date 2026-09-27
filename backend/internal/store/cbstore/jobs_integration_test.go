//go:build integration

package cbstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/jobs"
	"omnidatabus/internal/store"
)

type holdRunner struct {
	mu      sync.Mutex
	started map[string]int
}

type holdExec struct{ ctl jobs.Control }

func (e holdExec) Checkpoint() json.RawMessage { return nil }
func (e holdExec) Wait() jobs.Outcome {
	select {
	case <-e.ctl.Drain:
	case <-e.ctl.Abort:
	}
	return jobs.Outcome{}
}

func (r *holdRunner) Start(_ context.Context, j store.Job, ctl jobs.Control) jobs.Execution {
	r.mu.Lock()
	r.started[j.ID]++
	r.mu.Unlock()
	return holdExec{ctl: ctl}
}

// TestClaimer_AC5_NoDoubleClaim: two workers race for three queued jobs on real Couchbase;
// each job is claimed exactly once and neither worker exceeds its capacity (OC-023 AC5).
func TestClaimer_AC5_NoDoubleClaim(t *testing.T) {
	ctx := context.Background()
	ws := store.WorkspaceID("ws-claim-" + uuid.NewString())
	var ids []string
	for i := 0; i < 3; i++ {
		p, err := shared.Projects().Create(ctx, ws, store.NewProject{Name: fmt.Sprintf("claim %d", i)})
		require.NoError(t, err)
		j, err := shared.Jobs().Create(ctx, ws, store.Job{ProjectID: p.ID, Options: json.RawMessage(`{}`)})
		require.NoError(t, err)
		ids = append(ids, j.ID)
	}
	// Claiming is cross-workspace and oldest first: queued jobs left by earlier tests in this
	// throwaway scope (the store contract) would take the workers' slots. Cancel them first.
	mine := map[string]bool{ids[0]: true, ids[1]: true, ids[2]: true}
	queued, err := shared.Jobs().ListByStatus(ctx, []string{store.JobQueued}, 1000)
	require.NoError(t, err)
	for _, j := range queued {
		if !mine[j.ID] {
			j.Status = store.JobCancelled
			_, _ = shared.Jobs().Update(ctx, j)
		}
	}
	runner := &holdRunner{started: map[string]int{}}
	cfg := func(id string) jobs.WorkerConfig {
		return jobs.WorkerConfig{ID: id, MaxJobs: 2, StatusPoll: 50 * time.Millisecond, HeartbeatInterval: time.Second, ReaperInterval: time.Hour, ShutdownBudget: 10 * time.Second}
	}
	a := jobs.NewWorker(cfg("a"), shared, runner, nil, nil)
	b := jobs.NewWorker(cfg("b"), shared, runner, nil, nil)
	var wg sync.WaitGroup
	for _, w := range []*jobs.Worker{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); w.ClaimOnce(ctx) }()
	}
	wg.Wait()
	// Other tests may have left queued jobs behind: claim until ours are all taken.
	require.Eventually(t, func() bool {
		a.ClaimOnce(ctx)
		b.ClaimOnce(ctx)
		for _, id := range ids {
			j, err := shared.Jobs().GetAny(ctx, id)
			if err != nil || j.Status != store.JobRunning {
				return false
			}
		}
		return true
	}, 20*time.Second, 100*time.Millisecond)
	runner.mu.Lock()
	for _, id := range ids {
		require.Equal(t, 1, runner.started[id], "job %s started once", id)
	}
	runner.mu.Unlock()
	require.LessOrEqual(t, a.Running(), 2)
	require.LessOrEqual(t, b.Running(), 2)
	a.Shutdown()
	b.Shutdown()
	for _, id := range ids {
		j, err := shared.Jobs().GetAny(ctx, id)
		require.NoError(t, err)
		require.Equal(t, store.JobQueued, j.Status, "graceful shutdown hands jobs back")
	}
}
