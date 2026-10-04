package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

// RunExports covers OC-055: export history newest first, paging, lookup by bundle, isolation,
// cascade on project delete, and external jobs that hold no lock.
func RunExports(t *testing.T, newStore Factory) {
	ctx := context.Background()
	t.Run("ListAndFind", func(t *testing.T) {
		clock := NewClock()
		s := newStore(t, clock)
		wsA, wsB := workspaces()
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "Exports " + string(wsA)})
		require.NoError(t, err)
		var ids []string
		for i, sha := range []string{"aa", "bb", "cc"} {
			clock.Advance(time.Minute)
			e, err := s.Exports().Create(ctx, wsA, store.ExportRecord{
				ProjectID: p.ID, CreatedBy: "u", CreatedByEmail: "u@example.com", MappingIDs: []string{"m1"},
				PlanHashes: map[string]string{"m1": "h" + sha}, TargetType: "directory", CredentialMode: []string{"sealed", "external", "plaintext"}[i],
				Runtime: "script", OdbVersion: "2.0.0", BundleSHA256: sha, PlanHash: "p" + sha,
			})
			require.NoError(t, err)
			require.NotEmpty(t, e.ID)
			ids = append(ids, e.ID)
		}
		page, next, err := s.Exports().List(ctx, wsA, p.ID, store.Page{Limit: 2})
		require.NoError(t, err)
		require.Equal(t, []string{ids[2], ids[1]}, []string{page[0].ID, page[1].ID}, "newest first")
		require.Equal(t, "plaintext", page[0].CredentialMode)
		require.Equal(t, map[string]string{"m1": "hcc"}, page[0].PlanHashes)
		require.NotEmpty(t, next)
		rest, next, err := s.Exports().List(ctx, wsA, p.ID, store.Page{Limit: 2, Cursor: next})
		require.NoError(t, err)
		require.Len(t, rest, 1)
		require.Equal(t, ids[0], rest[0].ID)
		require.Empty(t, next)

		got, err := s.Exports().FindByBundle(ctx, wsA, p.ID, "bb")
		require.NoError(t, err)
		require.Equal(t, ids[1], got.ID)
		_, err = s.Exports().FindByBundle(ctx, wsA, p.ID, "zz")
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = s.Exports().FindByBundle(ctx, wsB, p.ID, "bb")
		require.ErrorIs(t, err, store.ErrNotFound, "another workspace sees nothing")
		other, _, err := s.Exports().List(ctx, wsB, p.ID, store.Page{})
		require.NoError(t, err)
		require.Empty(t, other)

		require.NoError(t, s.Projects().Delete(ctx, wsA, p.ID))
		_, err = s.Exports().FindByBundle(ctx, wsA, p.ID, "bb")
		require.ErrorIs(t, err, store.ErrNotFound, "deleting the project deletes its history")
	})

	t.Run("ExternalJobHoldsNoLock", func(t *testing.T) {
		s := newStore(t, NewClock())
		wsA, _ := workspaces()
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "Ext " + string(wsA)})
		require.NoError(t, err)
		active, err := s.Jobs().Create(ctx, wsA, store.Job{ProjectID: p.ID, Options: []byte(`{}`), MappingIDs: []string{"m"}})
		require.NoError(t, err)
		ext, err := s.Jobs().Create(ctx, wsA, store.Job{
			ProjectID: p.ID, Status: store.JobCompleted, Options: []byte(`{}`), MappingIDs: []string{"m"},
			Origin: "external", External: []byte(`{"runId":"r1"}`),
		})
		require.NoError(t, err, "an imported run doesn't need the active-job lock")
		got, err := s.Jobs().Get(ctx, wsA, ext.ID)
		require.NoError(t, err)
		require.Equal(t, "external", got.Origin)
		require.JSONEq(t, `{"runId":"r1"}`, string(got.External))
		cur, err := s.Jobs().ActiveForProject(ctx, wsA, p.ID)
		require.NoError(t, err)
		require.Equal(t, active.ID, cur.ID, "the running job keeps the lock")
	})
}

// RunClaimable covers OC-057's version-aware claim query: queued jobs a worker's release can run.
func RunClaimable(t *testing.T, newStore Factory) {
	ctx := context.Background()
	clock := NewClock()
	s := newStore(t, clock)
	wsA, _ := workspaces()
	mk := func(level int, status string) string {
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "Claim " + string(wsA) + time.Now().Format("150405.000000000")})
		require.NoError(t, err)
		j, err := s.Jobs().Create(ctx, wsA, store.Job{ProjectID: p.ID, Options: []byte(`{}`), MinWorkerLevel: level, Status: status})
		require.NoError(t, err)
		clock.Advance(time.Millisecond)
		return j.ID
	}
	legacy, v2, done := mk(0, ""), mk(2000000, ""), mk(0, store.JobCompleted)
	ids := func(js []store.Job) map[string]bool {
		out := map[string]bool{}
		for _, j := range js {
			out[j.ID] = true
		}
		return out
	}
	old, err := s.Jobs().ListClaimable(ctx, 1000001, 1000)
	require.NoError(t, err)
	got := ids(old)
	require.True(t, got[legacy])
	require.False(t, got[v2], "a 1.0.1 worker doesn't see 2.0 jobs")
	require.False(t, got[done], "only queued jobs")
	cur, err := s.Jobs().ListClaimable(ctx, 2000000, 1000)
	require.NoError(t, err)
	require.True(t, ids(cur)[v2])
	require.True(t, ids(cur)[legacy])
	j, err := s.Jobs().GetAny(ctx, v2)
	require.NoError(t, err)
	require.Equal(t, 2000000, j.MinWorkerLevel)
}
