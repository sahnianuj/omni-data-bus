package projects

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

var ctx = context.Background()

const ws store.WorkspaceID = "ws-1"

type fixture struct {
	svc    *Service
	st     *memstore.Store
	sealer crypto.Sealer
	jobs   *fakeJobs
}

type fakeJobs struct{ running bool }

func (f *fakeJobs) HasActiveJob(context.Context, store.WorkspaceID, string) (bool, error) {
	return f.running, nil
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	kr, err := crypto.ParseKeyring(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), "", "")
	require.NoError(t, err)
	st := memstore.New(storetest.NewClock())
	jobs := &fakeJobs{}
	sealer := crypto.NewLocalSealer(kr)
	return fixture{svc: NewService(st, sealer, jobs), st: st, sealer: sealer, jobs: jobs}
}

func (f fixture) saveRepo(t *testing.T, projectID string, role store.Role, typ, status, password string) {
	t.Helper()
	id := store.ConnectionID(projectID, role)
	sec := connections.Secrets{Password: password}
	sealed, err := connections.Seal(f.sealer, ws, projectID, id, sec)
	require.NoError(t, err)
	_, err = f.st.Connections().Upsert(ctx, ws, projectID, role, store.ConnectionInput{
		Type: typ, Name: typ + " repo", Config: json.RawMessage(`{"host":"h"}`), Secret: sealed,
		SecretMeta: connections.Meta(sec), LastTestStatus: status,
	})
	require.NoError(t, err)
}

func TestProjectsService_AC1_Create(t *testing.T) {
	f := newFixture(t)
	sum, err := f.svc.Create(ctx, ws, "u1", "  Inventory sync ", " stock levels ")
	require.NoError(t, err)
	require.Equal(t, "Inventory sync", sum.Project.Name)
	require.Equal(t, "stock levels", sum.Project.Description)
	require.Equal(t, StatusDraft, sum.Status)
	require.Nil(t, sum.Source)
	require.Nil(t, sum.Target)
	events, _ := f.st.Audit().List(ctx, ws, 5)
	require.Equal(t, "project.create", events[0].Action)
}

func TestProjectsService_AC1_DuplicateName(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create(ctx, ws, "u1", "Orders", "")
	require.NoError(t, err)
	_, err = f.svc.Create(ctx, ws, "u1", "orders", "")
	p := problem.From(err)
	require.Equal(t, 409, p.Status)
	require.Equal(t, problem.CodeProjectNameTaken, p.Code)
}

func TestProjectStatus_AC4_Table(t *testing.T) {
	ok := &store.Connection{LastTestStatus: "ok"}
	failed := &store.Connection{LastTestStatus: "failed"}
	untested := &store.Connection{}
	require.Equal(t, StatusDraft, DeriveStatus(nil, nil))
	require.Equal(t, StatusDraft, DeriveStatus(ok, nil))
	require.Equal(t, StatusDraft, DeriveStatus(ok, untested))
	require.Equal(t, StatusDraft, DeriveStatus(failed, ok))
	require.Equal(t, StatusConfigured, DeriveStatus(ok, ok))
	warn := &store.Connection{LastTestStatus: "warn"}
	require.Equal(t, StatusConfigured, DeriveStatus(warn, ok), "passed with warnings (e.g. unreadable billing tables) counts as passed")
}

func TestProjectStatus_AC2_DraftUntilBothTested(t *testing.T) {
	f := newFixture(t)
	sum, err := f.svc.Create(ctx, ws, "u1", "P", "")
	require.NoError(t, err)
	f.saveRepo(t, sum.Project.ID, store.RoleSource, "postgres", "ok", "pg-pass")
	got, err := f.svc.Get(ctx, ws, sum.Project.ID)
	require.NoError(t, err)
	require.Equal(t, StatusDraft, got.Status)
	f.saveRepo(t, sum.Project.ID, store.RoleTarget, "couchbase", "ok", "cb-pass")
	got, err = f.svc.Get(ctx, ws, sum.Project.ID)
	require.NoError(t, err)
	require.Equal(t, StatusConfigured, got.Status)
	require.Equal(t, "postgres", got.Source.Type)
}

func TestProjectContext_AC3_ResolvesStoredConnections(t *testing.T) {
	f := newFixture(t)
	sum, err := f.svc.Create(ctx, ws, "u1", "P", "")
	require.NoError(t, err)
	f.saveRepo(t, sum.Project.ID, store.RoleSource, "postgres", "ok", "pg-secret")
	f.saveRepo(t, sum.Project.ID, store.RoleTarget, "couchbase", "ok", "cb-secret")

	pc, err := f.svc.Resolve(ctx, ws, sum.Project.ID)
	require.NoError(t, err)
	require.Equal(t, "pg-secret", pc.Source.Secrets.Password, "stored credentials are reused, no prompt")
	require.Equal(t, "cb-secret", pc.Target.Secrets.Password)

	_, err = f.svc.Resolve(ctx, "other-ws", sum.Project.ID)
	require.ErrorIs(t, err, store.ErrNotFound, "never across workspaces")
}

func TestProjectsService_AC6_UpdateWithVersion(t *testing.T) {
	f := newFixture(t)
	sum, err := f.svc.Create(ctx, ws, "u1", "Before", "")
	require.NoError(t, err)
	name := "After"
	got, err := f.svc.Update(ctx, ws, "u1", sum.Project.ID, &name, nil, sum.Project.Version)
	require.NoError(t, err)
	require.Equal(t, "After", got.Project.Name)
	_, err = f.svc.Update(ctx, ws, "u1", sum.Project.ID, &name, nil, sum.Project.Version)
	require.Equal(t, 412, problem.From(err).Status, "stale version")
}

func TestProjectsService_AC7_DeleteBlockedWhenRunning(t *testing.T) {
	f := newFixture(t)
	sum, err := f.svc.Create(ctx, ws, "u1", "P", "")
	require.NoError(t, err)
	f.jobs.running = true
	err = f.svc.Delete(ctx, ws, "u1", sum.Project.ID)
	require.Equal(t, problem.CodeProjectJobRunning, problem.From(err).Code)
	_, err = f.svc.Get(ctx, ws, sum.Project.ID)
	require.NoError(t, err, "still there")
}

func TestProjectsService_AC7_DeleteCascadesAndAudits(t *testing.T) {
	f := newFixture(t)
	sum, err := f.svc.Create(ctx, ws, "u1", "Doomed", "")
	require.NoError(t, err)
	f.saveRepo(t, sum.Project.ID, store.RoleSource, "postgres", "ok", "x")
	require.NoError(t, f.svc.Delete(ctx, ws, "u1", sum.Project.ID))
	_, err = f.st.Connections().Get(ctx, ws, store.ConnectionID(sum.Project.ID, store.RoleSource))
	require.ErrorIs(t, err, store.ErrNotFound, "credentials deleted with the project")
	events, _ := f.st.Audit().List(ctx, ws, 5)
	require.Equal(t, "project.delete", events[0].Action)
}

func TestDashboardStats_AC8_Aggregates(t *testing.T) {
	f := newFixture(t)
	a, _ := f.svc.Create(ctx, ws, "u", "A", "")
	b, _ := f.svc.Create(ctx, ws, "u", "B", "")
	_, _ = f.svc.Create(ctx, ws, "u", "C", "")
	_, _ = f.svc.Create(ctx, ws, "u", "D", "")
	f.saveRepo(t, a.Project.ID, store.RoleSource, "postgres", "ok", "x")
	f.saveRepo(t, a.Project.ID, store.RoleTarget, "couchbase", "ok", "x")
	f.saveRepo(t, b.Project.ID, store.RoleSource, "postgres", "failed", "x")
	stats, recent, err := f.svc.Dashboard(ctx, ws)
	require.NoError(t, err)
	require.Equal(t, Stats{Projects: 4, RepositoriesOK: 2, RepositoriesTotal: 3, ConfiguredProjects: 1}, stats)
	require.Len(t, recent, 3)
	require.Equal(t, "D", recent[0].Project.Name, "most recent first")
}

// --- HTTP ---

func router(f fixture, role store.MemberRole) http.Handler {
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u1", WorkspaceID: ws, Role: role})))
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

func TestProjectsHandler_AC1_CreateGetListPatchDelete(t *testing.T) {
	f := newFixture(t)
	h := router(f, store.RoleEditor)
	rec := call(h, "POST", "/api/v1/projects", `{"name":"Inventory sync","description":"d"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var p DTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	require.Equal(t, StatusDraft, p.Status)
	require.Nil(t, p.Source)

	require.Equal(t, http.StatusOK, call(h, "GET", "/api/v1/projects/"+p.ID, "").Code)
	list := call(h, "GET", "/api/v1/projects?limit=10", "")
	require.Contains(t, list.Body.String(), `"nextCursor":""`)
	require.Contains(t, list.Body.String(), "Inventory sync")

	patch := call(h, "PATCH", "/api/v1/projects/"+p.ID, `{"name":"Renamed","version":1}`)
	require.Equal(t, http.StatusOK, patch.Code, patch.Body.String())
	require.Equal(t, http.StatusPreconditionFailed, call(h, "PATCH", "/api/v1/projects/"+p.ID, `{"name":"x","version":1}`).Code)

	conns := call(h, "GET", "/api/v1/projects/"+p.ID+"/connections", "")
	require.JSONEq(t, `{"source":null,"target":null}`, conns.Body.String())

	require.Equal(t, http.StatusNoContent, call(h, "DELETE", "/api/v1/projects/"+p.ID, "").Code)
	require.Equal(t, http.StatusNotFound, call(h, "GET", "/api/v1/projects/"+p.ID, "").Code)
}

func TestProjectsHandler_AC1_Validation(t *testing.T) {
	h := router(newFixture(t), store.RoleOwner)
	require.Equal(t, http.StatusUnprocessableEntity, call(h, "POST", "/api/v1/projects", `{"name":""}`).Code)
	require.Equal(t, http.StatusUnprocessableEntity, call(h, "POST", "/api/v1/projects", `{"name":"`+strings.Repeat("x", 101)+`"}`).Code)
}

func TestProjectsHandler_AC1_ViewerIsReadOnly(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create(ctx, ws, "u1", "Existing", "")
	require.NoError(t, err)
	h := router(f, store.RoleViewer)
	rec := call(h, "POST", "/api/v1/projects", `{"name":"New"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeForbiddenRole)
	require.Equal(t, http.StatusOK, call(h, "GET", "/api/v1/projects", "").Code)
}

func TestProjectsHandler_AC3_ConnectionsNeverExposeSecrets(t *testing.T) {
	f := newFixture(t)
	sum, err := f.svc.Create(ctx, ws, "u1", "P", "")
	require.NoError(t, err)
	f.saveRepo(t, sum.Project.ID, store.RoleSource, "postgres", "ok", "super-secret-pw")
	rec := call(router(f, store.RoleViewer), "GET", "/api/v1/projects/"+sum.Project.ID+"/connections", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "super-secret-pw")
	require.Contains(t, rec.Body.String(), `"passwordSet":true`)
	require.Contains(t, rec.Body.String(), `"target":null`)
}

func TestDashboardHandler_AC8(t *testing.T) {
	f := newFixture(t)
	_, _ = f.svc.Create(ctx, ws, "u1", "P", "")
	rec := call(router(f, store.RoleViewer), "GET", "/api/v1/dashboard", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"projects":1`)
}
