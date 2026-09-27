package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

// RunJobs covers OC-023/OC-026/OC-027 storage: the active-job lock, CAS updates and claims,
// progress documents, dead-lettered errors, timelines and leases.
func RunJobs(t *testing.T, newStore Factory) {
	ctx := context.Background()
	setup := func(t *testing.T) (store.Store, *Clock, store.WorkspaceID, store.Project) {
		clock := NewClock()
		s := newStore(t, clock)
		ws, _ := workspaces()
		p, err := s.Projects().Create(ctx, ws, store.NewProject{Name: "jobs " + string(ws), CreatedBy: "u1"})
		require.NoError(t, err)
		return s, clock, ws, p
	}
	newJob := func(p store.Project) store.Job {
		return store.Job{ProjectID: p.ID, Options: json.RawMessage(`{"batchSize":1000}`), MappingIDs: []string{"m1"}, CreatedBy: "u1"}
	}

	t.Run("AC4_OneActiveJobPerProject", func(t *testing.T) {
		s, _, ws, p := setup(t)
		j, err := s.Jobs().Create(ctx, ws, newJob(p))
		require.NoError(t, err)
		require.Equal(t, store.JobQueued, j.Status)
		require.EqualValues(t, 1, j.Version)
		_, err = s.Jobs().Create(ctx, ws, newJob(p))
		var conflict *store.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, "activeJob", conflict.Field)
		active, err := s.Jobs().ActiveForProject(ctx, ws, p.ID)
		require.NoError(t, err)
		require.Equal(t, j.ID, active.ID)

		// Terminal releases the lock; a second job may start.
		j.Status = store.JobFailed
		j, err = s.Jobs().Update(ctx, j)
		require.NoError(t, err)
		_, err = s.Jobs().ActiveForProject(ctx, ws, p.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		j2, err := s.Jobs().Create(ctx, ws, newJob(p))
		require.NoError(t, err)

		// Resuming the failed job while j2 is active is refused; after j2 ends it's allowed.
		j.Status = store.JobQueued
		_, err = s.Jobs().Update(ctx, j)
		require.ErrorAs(t, err, &conflict)
		j2.Status = store.JobCancelled
		_, err = s.Jobs().Update(ctx, j2)
		require.NoError(t, err)
		j, err = s.Jobs().Get(ctx, ws, j.ID)
		require.NoError(t, err)
		j.Status = store.JobQueued
		_, err = s.Jobs().Update(ctx, j)
		require.NoError(t, err)
		active, err = s.Jobs().ActiveForProject(ctx, ws, p.ID)
		require.NoError(t, err)
		require.Equal(t, j.ID, active.ID)
	})

	t.Run("CASUpdateAndTenantIsolation", func(t *testing.T) {
		s, clock, ws, p := setup(t)
		j, err := s.Jobs().Create(ctx, ws, newJob(p))
		require.NoError(t, err)
		clock.Advance(time.Second)
		stale := j
		j.Status, j.ClaimedBy = store.JobRunning, "w1"
		j.Checkpoint = json.RawMessage(`{"m1":{"lastKey":[42]}}`)
		j, err = s.Jobs().Update(ctx, j)
		require.NoError(t, err)
		require.EqualValues(t, 2, j.Version)
		require.True(t, j.UpdatedAt.After(j.CreatedAt))
		_, err = s.Jobs().Update(ctx, stale)
		require.ErrorIs(t, err, store.ErrVersionConflict)

		got, err := s.Jobs().GetAny(ctx, j.ID)
		require.NoError(t, err)
		require.Equal(t, "w1", got.ClaimedBy)
		require.JSONEq(t, `{"m1":{"lastKey":[42]}}`, string(got.Checkpoint))
		_, other := workspaces()
		_, err = s.Jobs().Get(ctx, other, j.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		items, _, err := s.Jobs().List(ctx, other, "", store.Page{})
		require.NoError(t, err)
		require.Empty(t, items)
		items, _, err = s.Jobs().List(ctx, ws, p.ID, store.Page{})
		require.NoError(t, err)
		require.Len(t, items, 1)
	})

	t.Run("AC5_ConcurrentClaimsHaveOneWinner", func(t *testing.T) {
		s, _, ws, p := setup(t)
		j, err := s.Jobs().Create(ctx, ws, newJob(p))
		require.NoError(t, err)
		var wins atomic.Int32
		var wg sync.WaitGroup
		for w := 0; w < 8; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := j
				c.Status, c.ClaimedBy = store.JobRunning, fmt.Sprintf("w%d", w)
				if _, err := s.Jobs().Update(ctx, c); err == nil {
					wins.Add(1)
				} else {
					require.ErrorIs(t, err, store.ErrVersionConflict)
				}
			}()
		}
		wg.Wait()
		require.EqualValues(t, 1, wins.Load())
	})

	t.Run("ListByStatusOldestFirst", func(t *testing.T) {
		s, clock, ws, p := setup(t)
		p2, err := s.Projects().Create(ctx, ws, store.NewProject{Name: "jobs 2 " + string(ws), CreatedBy: "u1"})
		require.NoError(t, err)
		a, err := s.Jobs().Create(ctx, ws, newJob(p))
		require.NoError(t, err)
		clock.Advance(time.Second)
		b, err := s.Jobs().Create(ctx, ws, newJob(p2))
		require.NoError(t, err)
		got, err := s.Jobs().ListByStatus(ctx, []string{store.JobQueued}, 1000)
		require.NoError(t, err)
		var ids []string
		for _, j := range got {
			if j.ID == a.ID || j.ID == b.ID {
				ids = append(ids, j.ID)
			}
		}
		require.Equal(t, []string{a.ID, b.ID}, ids)
	})

	t.Run("ProgressRevisionChanges", func(t *testing.T) {
		s, _, ws, p := setup(t)
		j, err := s.Jobs().Create(ctx, ws, newJob(p))
		require.NoError(t, err)
		_, _, err = s.Jobs().GetProgress(ctx, j.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		require.NoError(t, s.Jobs().PutProgress(ctx, j.ID, json.RawMessage(`{"seq":1}`)))
		doc, rev1, err := s.Jobs().GetProgress(ctx, j.ID)
		require.NoError(t, err)
		require.JSONEq(t, `{"seq":1}`, string(doc))
		require.NoError(t, s.Jobs().PutProgress(ctx, j.ID, json.RawMessage(`{"seq":2}`)))
		_, rev2, err := s.Jobs().GetProgress(ctx, j.ID)
		require.NoError(t, err)
		require.NotEqual(t, rev1, rev2)
	})

	t.Run("ReportRoundTrip", func(t *testing.T) {
		s, _, ws, p := setup(t)
		j, err := s.Jobs().Create(ctx, ws, newJob(p))
		require.NoError(t, err)
		_, err = s.Jobs().GetReport(ctx, j.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		require.NoError(t, s.Jobs().PutReport(ctx, j.ID, json.RawMessage(`{"mappings":{"m1":{"result":"passed"}}}`)))
		require.NoError(t, s.Jobs().PutReport(ctx, j.ID, json.RawMessage(`{"mappings":{"m1":{"result":"failed"}}}`)))
		doc, err := s.Jobs().GetReport(ctx, j.ID)
		require.NoError(t, err)
		require.JSONEq(t, `{"mappings":{"m1":{"result":"failed"}}}`, string(doc), "the latest put wins")
	})

	t.Run("AC5_ErrorsListFilterSummary", func(t *testing.T) {
		s, clock, ws, p := setup(t)
		j, err := s.Jobs().Create(ctx, ws, newJob(p))
		require.NoError(t, err)
		var errs []store.JobError
		for i := int64(1); i <= 7; i++ {
			code := "PARSE_FAILED"
			if i%3 == 0 {
				code = "DOC_TOO_LARGE"
			}
			errs = append(errs, store.JobError{
				JobID: j.ID, WorkspaceID: ws, Seq: i, MappingID: "m1", Stage: "transform", Code: code,
				Message: "bad", SourceKey: json.RawMessage(`[` + string(rune('0'+i)) + `]`), At: clock.Now(),
			})
		}
		require.NoError(t, s.Jobs().AddErrors(ctx, errs, time.Hour))
		page1, next, err := s.Jobs().ListErrors(ctx, ws, j.ID, store.JobErrorFilter{}, store.Page{Limit: 4})
		require.NoError(t, err)
		require.Len(t, page1, 4)
		require.NotEmpty(t, next)
		page2, next, err := s.Jobs().ListErrors(ctx, ws, j.ID, store.JobErrorFilter{}, store.Page{Limit: 4, Cursor: next})
		require.NoError(t, err)
		require.Len(t, page2, 3)
		require.Empty(t, next)
		require.EqualValues(t, 5, page2[0].Seq)
		big, _, err := s.Jobs().ListErrors(ctx, ws, j.ID, store.JobErrorFilter{Code: "DOC_TOO_LARGE"}, store.Page{})
		require.NoError(t, err)
		require.Len(t, big, 2)
		require.JSONEq(t, `[3]`, string(big[0].SourceKey))

		sum, err := s.Jobs().SummarizeErrors(ctx, ws, j.ID)
		require.NoError(t, err)
		require.Len(t, sum, 2)
		require.Equal(t, "PARSE_FAILED", sum[0].Code)
		require.EqualValues(t, 5, sum[0].Count)
		require.EqualValues(t, 1, sum[0].Example.Seq)

		_, other := workspaces()
		leak, _, err := s.Jobs().ListErrors(ctx, other, j.ID, store.JobErrorFilter{}, store.Page{})
		if err == nil {
			require.Empty(t, leak)
		} else {
			require.ErrorIs(t, err, store.ErrNotFound)
		}
	})

	t.Run("AC7_EventsAppendInOrder", func(t *testing.T) {
		s, clock, ws, p := setup(t)
		j, err := s.Jobs().Create(ctx, ws, newJob(p))
		require.NoError(t, err)
		for _, k := range []string{"created", "claimed", "aimd"} {
			clock.Advance(time.Second)
			require.NoError(t, s.Jobs().AppendEvent(ctx, j.ID, store.JobEvent{At: clock.Now(), Kind: k, Message: k}))
		}
		evs, err := s.Jobs().ListEvents(ctx, ws, j.ID)
		require.NoError(t, err)
		require.Len(t, evs, 3)
		require.Equal(t, "aimd", evs[2].Kind)
		require.True(t, evs[0].At.Before(evs[2].At))
		_, other := workspaces()
		_, err = s.Jobs().ListEvents(ctx, other, j.ID)
		require.True(t, errors.Is(err, store.ErrNotFound))
	})

	t.Run("AC6_LeaseExclusiveUntilExpiry", func(t *testing.T) {
		s, clock, _, _ := setup(t)
		name := "reaper-test-" + time.Now().Format("150405.000000000")
		ok, err := s.Jobs().AcquireLease(ctx, name, "a", 30*time.Second)
		require.NoError(t, err)
		require.True(t, ok)
		ok, err = s.Jobs().AcquireLease(ctx, name, "b", 30*time.Second)
		require.NoError(t, err)
		require.False(t, ok, "held by a")
		ok, err = s.Jobs().AcquireLease(ctx, name, "a", 30*time.Second)
		require.NoError(t, err)
		require.True(t, ok, "a renews")
		clock.Advance(31 * time.Second)
		ok, err = s.Jobs().AcquireLease(ctx, name, "b", 30*time.Second)
		require.NoError(t, err)
		require.True(t, ok, "a's lease expired")
	})
}
