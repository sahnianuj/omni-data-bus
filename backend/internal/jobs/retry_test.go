package jobs

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// finishWithErrors marks the job completed_with_errors with n stored failures on mappingID.
func finishWithErrors(t *testing.T, f *fixture, jobID, mappingID string, n int) {
	t.Helper()
	j, err := f.st.Jobs().Get(ctx, ws, jobID)
	require.NoError(t, err)
	j.Status = store.JobCompletedWithErrors
	_, err = f.st.Jobs().Update(ctx, j)
	require.NoError(t, err)
	var errs []store.JobError
	for i := 1; i <= n; i++ {
		code := "PARSE_FAILED"
		if i%3 == 0 {
			code = "DOC_TOO_LARGE"
		}
		errs = append(errs, store.JobError{
			JobID: jobID, WorkspaceID: ws, Seq: int64(i), MappingID: mappingID, Stage: "transform", Code: code,
			Message: "bad", SourceKey: json.RawMessage(fmt.Sprintf("[%d]", i*10)), At: time.Now(),
		})
	}
	require.NoError(t, f.st.Jobs().AddErrors(ctx, errs, time.Hour))
	raw, _ := json.Marshal(map[string]any{"failed": n})
	require.NoError(t, f.st.Jobs().PutProgress(ctx, jobID, raw))
}

func TestRetry_AC6_CreatesKeyFilteredJob(t *testing.T) {
	f := newFixture(t)
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	finishWithErrors(t, f, v.ID, f.maps["customer"].ID, 7)
	require.True(t, func() bool { g, _ := f.svc.Get(ctx, ws, v.ID); return g.Retryable }())

	r, _, err := f.svc.Retry(ctx, ws, "u1", v.ID, []string{"DOC_TOO_LARGE"})
	require.NoError(t, err)
	require.Equal(t, store.JobQueued, r.Status)
	require.Equal(t, v.ID, r.RetryOf)
	require.Equal(t, v.ID, r.Options.RetryOf)
	require.Equal(t, "failed", r.Options.KeyFilter)
	require.Equal(t, []string{"DOC_TOO_LARGE"}, r.Options.OnlyCodes)
	require.Equal(t, []string{f.maps["customer"].ID}, r.MappingIDs)
	evs, _ := f.st.Jobs().ListEvents(ctx, ws, v.ID)
	require.Equal(t, "retry", evs[len(evs)-1].Kind)
}

func TestRetry_AC6_NotPossible(t *testing.T) {
	f := newFixture(t)
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	_, _, err = f.svc.Retry(ctx, ws, "u1", v.ID, nil)
	require.True(t, problem.HasCode(err, problem.CodeRetryNotPossible), "a queued job has nothing to retry")

	finishWithErrors(t, f, v.ID, f.maps["customer"].ID, 3)
	j, _ := f.st.Jobs().Get(ctx, ws, v.ID)
	j.ErrorsTruncated = true
	_, err = f.st.Jobs().Update(ctx, j)
	require.NoError(t, err)
	_, _, err = f.svc.Retry(ctx, ws, "u1", v.ID, nil)
	require.True(t, problem.HasCode(err, problem.CodeRetryNotPossible), "truncated errors")

	// A table without a primary key can't be read back by key.
	g := newFixture(t)
	pv := g.addMapping(t, "page_view", "ready", true)
	opts := DefaultOptions()
	opts.MappingIDs = []string{pv.ID}
	v2, _, err := g.svc.Create(ctx, ws, "u1", g.project.ID, opts)
	require.NoError(t, err)
	finishWithErrors(t, g, v2.ID, pv.ID, 2)
	_, _, err = g.svc.Retry(ctx, ws, "u1", v2.ID, nil)
	require.True(t, problem.HasCode(err, problem.CodeRetryNotPossible))
	require.Contains(t, problem.From(err).Detail, "no primary key")
}
