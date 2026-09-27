package jobs

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/store"
)

func router(f *fixture, role store.MemberRole, workspace store.WorkspaceID) http.Handler {
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u1", WorkspaceID: workspace, Role: role})))
		})
	}
	return httpapi.NewRouter(httpapi.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Features: []httpapi.Registrar{Handlers{Svc: f.svc}},
		Middleware: []func(http.Handler) http.Handler{withPrincipal},
	})
}

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestJobsHandler_AC1_InvalidTransition409(t *testing.T) {
	f := newFixture(t)
	h := router(f, store.RoleEditor, ws)
	base := "/api/v1/projects/" + f.project.ID + "/jobs"

	rec := call(h, "POST", base+"/preflight", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"ok":true`)

	rec = call(h, "POST", base, `{"batchSize":2000}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created struct {
		Job View `json:"job"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.Equal(t, 2000, created.Job.Options.BatchSize)
	id := created.Job.ID

	rec = call(h, "POST", "/api/v1/jobs/"+id+"/pause", "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"JOB_INVALID_TRANSITION"`)

	require.Equal(t, http.StatusConflict, call(h, "POST", base, "").Code, "one active job per project")
	require.Equal(t, http.StatusOK, call(h, "POST", "/api/v1/jobs/"+id+"/cancel", "").Code)
	require.Equal(t, http.StatusConflict, call(h, "POST", "/api/v1/jobs/"+id+"/resume", "").Code)

	rec = call(h, "GET", "/api/v1/jobs", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), id)
	rec = call(h, "GET", "/api/v1/jobs/"+id+"/events/log", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"kind":"cancel"`)

	require.Equal(t, http.StatusUnprocessableEntity, call(h, "POST", base, `{"batchSize":50}`).Code)
	require.Equal(t, http.StatusNotFound, call(router(f, store.RoleEditor, "ws-other"), "GET", "/api/v1/jobs/"+id, "").Code)
	require.Equal(t, http.StatusForbidden, call(router(f, store.RoleViewer, ws), "POST", base, "").Code)
}

func TestJobsHandler_AC2_PreflightFailureListsChecks(t *testing.T) {
	f := newFixture(t)
	f.issues[f.maps["customer"].ID] = []mapping.Issue{{Severity: mapping.SeverityError, Code: "X", Message: "Broken"}}
	rec := call(router(f, store.RoleEditor, ws), "POST", "/api/v1/projects/"+f.project.ID+"/jobs", "")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var body struct {
		Code      string          `json:"code"`
		Preflight PreflightResult `json:"preflight"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "PREFLIGHT_FAILED", body.Code)
	require.False(t, body.Preflight.OK)
	require.NotEmpty(t, body.Preflight.Checks)
}

func TestErrorsHandler_AC5_SummaryAndFilter(t *testing.T) {
	f := newFixture(t)
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	finishWithErrors(t, f, v.ID, f.maps["customer"].ID, 7) // 5 PARSE_FAILED, 2 DOC_TOO_LARGE
	h := router(f, store.RoleViewer, ws)
	base := "/api/v1/jobs/" + v.ID + "/errors"

	rec := call(h, "GET", base+"/summary", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var sum struct {
		Items     []store.JobErrorCount `json:"items"`
		Truncated bool                  `json:"truncated"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sum))
	require.Len(t, sum.Items, 2)
	require.Equal(t, "PARSE_FAILED", sum.Items[0].Code)
	require.EqualValues(t, 5, sum.Items[0].Count)
	require.NotZero(t, sum.Items[0].Example.Seq)

	rec = call(h, "GET", base+"?code=DOC_TOO_LARGE&limit=1", "")
	var page struct {
		Items      []store.JobError `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	require.Equal(t, "DOC_TOO_LARGE", page.Items[0].Code)
	require.NotEmpty(t, page.NextCursor)
	rec = call(h, "GET", base+"?code=DOC_TOO_LARGE&cursor="+page.NextCursor, "")
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	require.Empty(t, page.NextCursor)

	rec = call(h, "GET", base+"/export", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	require.Equal(t, 8, strings.Count(rec.Body.String(), "\n"), "header + 7 rows")
	rec = call(h, "GET", base+"/export?format=ndjson", "")
	require.Equal(t, 7, strings.Count(rec.Body.String(), "\n"))

	require.Equal(t, http.StatusNotFound, call(router(f, store.RoleViewer, "ws-x"), "GET", base+"/summary", "").Code)
	rec = call(router(f, store.RoleEditor, ws), "POST", "/api/v1/jobs/"+v.ID+"/retry", `{"onlyCodes":["PARSE_FAILED"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}
