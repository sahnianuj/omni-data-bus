package jobs

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

// TestClaim_AC3_MinWorkerVersion (OC-057): during a rolling upgrade a 1.0.1 worker never claims
// a job that needs 2.0 — even when such jobs are the oldest queued — while 1.0-compatible jobs
// are claimed by either release.
func TestClaim_AC3_MinWorkerVersion(t *testing.T) {
	f := newFixture(t)
	var v2 []string
	for i := range 12 { // more than one claim page of 2.0-only jobs, all older than the 1.0 job
		p, err := f.st.Projects().Create(ctx, ws, store.NewProject{Name: fmt.Sprintf("v2-%d", i)})
		require.NoError(t, err)
		j, err := f.st.Jobs().Create(ctx, ws, store.Job{ProjectID: p.ID, Options: json.RawMessage(`{}`), MinWorkerVersion: "2.0.0", MinWorkerLevel: 2000000})
		require.NoError(t, err)
		v2 = append(v2, j.ID)
		f.clock.Advance(time.Millisecond)
	}
	v1 := queue(t, f, 1)[0]

	runner := newFakeRunner()
	old := fastConfig("old")
	old.Version, old.MaxJobs = "1.0.1", 20
	w1 := NewWorker(old, f.st, runner, f.clock.Now, nil)
	require.Equal(t, 1, w1.ClaimOnce(ctx), "only the 1.0 job")
	j, err := f.st.Jobs().GetAny(ctx, v1)
	require.NoError(t, err)
	require.Equal(t, "old", j.ClaimedBy)
	for _, id := range v2 {
		j, _ := f.st.Jobs().GetAny(ctx, id)
		require.Empty(t, j.ClaimedBy)
	}

	cur := fastConfig("new")
	cur.Version, cur.MaxJobs = "v2.0.0", 20
	w2 := NewWorker(cur, f.st, runner, f.clock.Now, nil)
	require.Equal(t, 10, w2.ClaimOnce(ctx), "the 2.0 worker takes the 2.0 jobs (10 per claim page)")
	w1.Shutdown()
	w2.Shutdown()
}

// TestCreate_SetsMinWorkerVersion: a job using a 2.0-only feature needs a 2.0 worker.
func TestCreate_SetsMinWorkerVersion(t *testing.T) {
	f := newFixture(t)
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	require.Empty(t, v.MinWorkerVersion, "a flat mapping to Couchbase runs on 1.0")

	m := f.maps["customer"]
	m.Spec = json.RawMessage(`{"specVersion":2,"mode":"composed","docSchema":{"root":{"id":"r","name":"$","type":"object"}},"key":{"template":"c::{customer_id}"},"relations":[],"fieldMappings":[]}`)
	_, err = f.st.Mappings().Update(ctx, ws, m)
	require.NoError(t, err)
	cur, _ := f.st.Jobs().Get(ctx, ws, v.ID)
	cur.Status = store.JobCompleted
	_, err = f.st.Jobs().Update(ctx, cur)
	require.NoError(t, err)
	v, _, err = f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, "2.0.0", v.MinWorkerVersion)
	require.Equal(t, 2000000, v.MinWorkerLevel)
}

func TestVersionLevel(t *testing.T) {
	for in, want := range map[string]int{"2.0.0": 2000000, "v1.0.1": 1000001, "2.1.0-rc.1": 2001000, "10.2.3": 10002003} {
		got, ok := store.VersionLevel(in)
		require.True(t, ok, in)
		require.Equal(t, want, got, in)
	}
	for _, dev := range []string{"dev", "2006732-dirty", "1.2", ""} {
		_, ok := store.VersionLevel(dev)
		require.False(t, ok, dev)
	}
	require.Greater(t, WorkerConfig{Version: "dev"}.level(), 99_000_000, "a development worker claims everything")
}
