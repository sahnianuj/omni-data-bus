package jobs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/audit"
	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/store"
)

func auditedRouter(f *fixture) http.Handler {
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u1", WorkspaceID: ws, Role: store.RoleEditor})))
		})
	}
	return httpapi.NewRouter(httpapi.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Features:   []httpapi.Registrar{Handlers{Svc: f.svc, Audit: &audit.Recorder{Store: f.st.Audit()}}},
		Middleware: []func(http.Handler) http.Handler{withPrincipal},
	})
}

func setJobStatus(t *testing.T, f *fixture, id, status string) {
	t.Helper()
	j, err := f.st.Jobs().Get(context.Background(), ws, id)
	require.NoError(t, err)
	j.Status = status
	_, err = f.st.Jobs().Update(context.Background(), j)
	require.NoError(t, err)
}

// Each job action is audited exactly once, with the actor and the job (OC-033 AC7).
func TestJobsHandler_AuditsEveryAction(t *testing.T) {
	f := newFixture(t)
	h := auditedRouter(f)
	rec := call(h, "POST", "/api/v1/projects/"+f.project.ID+"/jobs", "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created struct {
		Job View `json:"job"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	id := created.Job.ID

	setJobStatus(t, f, id, store.JobRunning)
	require.Equal(t, http.StatusOK, call(h, "POST", "/api/v1/jobs/"+id+"/pause", "").Code)
	setJobStatus(t, f, id, store.JobPaused)
	require.Equal(t, http.StatusOK, call(h, "POST", "/api/v1/jobs/"+id+"/resume", "").Code)
	require.Equal(t, http.StatusOK, call(h, "POST", "/api/v1/jobs/"+id+"/cancel", "").Code)
	require.Equal(t, http.StatusConflict, call(h, "POST", "/api/v1/jobs/"+id+"/cancel", "").Code, "failed actions aren't audited")
	require.Equal(t, http.StatusOK, call(h, "GET", "/api/v1/jobs/"+id+"/report", "").Code, "viewing isn't a download")
	require.Equal(t, http.StatusOK, call(h, "GET", "/api/v1/jobs/"+id+"/report.csv?download=1", "").Code)

	events, _, err := f.st.Audit().Search(context.Background(), ws, store.AuditFilter{}, store.Page{Limit: 50})
	require.NoError(t, err)
	var got []string
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		got = append(got, e.Action)
		require.Equal(t, "u1", e.UserID)
		require.Equal(t, "job", e.EntityType)
	}
	require.Equal(t, []string{"job.create", "job.pause", "job.resume", "job.cancel", "report.download"}, got)
	require.JSONEq(t, `{"format":"csv"}`, string(events[0].Metadata))
}
