package connections

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/source/sourcetest"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

var ctx = context.Background()

const ws store.WorkspaceID = "ws-1"

// recorder is a PostgreSQL-shaped connector that records the credentials it was tested with.
type recorder struct {
	*postgres.Connector
	creds  []connectors.Credentials
	result connectors.TestResult
}

func (r *recorder) Test(_ context.Context, _ json.RawMessage, c connectors.Credentials) connectors.TestResult {
	r.creds = append(r.creds, c)
	return r.result
}

type fakeJobs struct{ running bool }

func (f *fakeJobs) HasActiveJob(context.Context, store.WorkspaceID, string) (bool, error) {
	return f.running, nil
}

type fixture struct {
	svc     *Service
	st      *memstore.Store
	pg      *recorder
	jobs    *fakeJobs
	project string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	st := memstore.New(storetest.NewClock())
	pg := &recorder{Connector: postgres.New(), result: connectors.TestResult{OK: true, Steps: []connectors.TestStep{{ID: "auth", Status: connectors.StepOK}}}}
	sources := source.NewRegistry()
	sources.Register(pg)
	jobs := &fakeJobs{}
	svc := NewService(st, sealer(t), sources, target.NewRegistry(), jobs, storetest.NewClock())
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	return fixture{svc: svc, st: st, pg: pg, jobs: jobs, project: p.ID}
}

var pgCfg = json.RawMessage(`{"host":"db.example.com","database":"shop","username":"omni_ro"}`)

func save(t *testing.T, f fixture, cfg json.RawMessage, pw *string) store.Connection {
	t.Helper()
	c, _, err := f.svc.Save(ctx, ws, "u1", f.project, store.RoleSource, SaveInput{Type: "postgres", Config: cfg, Secrets: SecretsPatch{Password: pw}})
	require.NoError(t, err)
	return c
}

func TestConnectionsService_AC5_UpsertCreatesForProject(t *testing.T) {
	f := newFixture(t)
	c, created, err := f.svc.Save(ctx, ws, "u1", f.project, store.RoleSource, SaveInput{Type: "postgres", Config: pgCfg, Secrets: SecretsPatch{Password: ptr("pw-1")}})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, store.ConnectionID(f.project, store.RoleSource), c.ID)
	require.Equal(t, "shop@db.example.com", c.Name, "default name")
	require.Equal(t, "untested", c.LastTestStatus)
	require.True(t, c.SecretMeta.PasswordSet)
	require.Contains(t, string(c.Config), `"port":5432`, "defaults stored")
	dto, _ := json.Marshal(ToDTO(c))
	require.NotContains(t, string(dto), "pw-1")
}

func TestConnectionsService_AC5_UpsertKeepsIDAndSecret(t *testing.T) {
	f := newFixture(t)
	first := save(t, f, pgCfg, ptr("pw-1"))
	second := save(t, f, pgCfg, nil) // no password: keep the stored one
	require.Equal(t, first.ID, second.ID)
	sec, err := Open(f.svc.sealer, second)
	require.NoError(t, err)
	require.Equal(t, "pw-1", sec.Password)
}

func TestConnectionsService_AC5_PasswordRequiredOnCreate(t *testing.T) {
	f := newFixture(t)
	_, _, err := f.svc.Save(ctx, ws, "u1", f.project, store.RoleSource, SaveInput{Type: "postgres", Config: pgCfg})
	require.Equal(t, "secrets.password", problem.From(err).FieldErrors[0].Field)
}

func TestConnectionsService_AC5_ConfigChangeResetsTestStatus(t *testing.T) {
	f := newFixture(t)
	c := save(t, f, pgCfg, ptr("pw"))
	_, err := f.svc.TestStored(ctx, ws, "u1", c.ID)
	require.NoError(t, err)
	got, _ := f.svc.Get(ctx, ws, c.ID)
	require.Equal(t, "ok", got.LastTestStatus)

	same := save(t, f, pgCfg, nil)
	require.Equal(t, "ok", same.LastTestStatus, "nothing changed: keep the result")

	changed := save(t, f, json.RawMessage(`{"host":"db2.example.com","database":"shop","username":"omni_ro"}`), nil)
	require.Equal(t, "untested", changed.LastTestStatus)
	_ = save(t, f, json.RawMessage(`{"host":"db2.example.com","database":"shop","username":"omni_ro"}`), nil)
	newPw := save(t, f, json.RawMessage(`{"host":"db2.example.com","database":"shop","username":"omni_ro"}`), ptr("new"))
	require.Equal(t, "untested", newPw.LastTestStatus, "a new password needs a new test")
}

func TestConnectionsService_AC5_TestStoredUsesSealedSecret(t *testing.T) {
	f := newFixture(t)
	c := save(t, f, pgCfg, ptr("stored-pw"))
	f.pg.result = connectors.TestResult{OK: true, Steps: []connectors.TestStep{{ID: "privileges", Status: connectors.StepWarn}}}
	res, err := f.svc.TestStored(ctx, ws, "u1", c.ID)
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Equal(t, "stored-pw", f.pg.creds[len(f.pg.creds)-1].Password, "decrypted from the store; the request had no password")
	got, _ := f.svc.Get(ctx, ws, c.ID)
	require.Equal(t, "warn", got.LastTestStatus)
	require.NotNil(t, got.LastTestedAt)

	f.pg.result = connectors.TestResult{OK: false}
	_, err = f.svc.TestStored(ctx, ws, "u1", c.ID)
	require.NoError(t, err)
	got, _ = f.svc.Get(ctx, ws, c.ID)
	require.Equal(t, "failed", got.LastTestStatus)
}

func TestConnectionsService_AC5_BlockedWhileJobRunning(t *testing.T) {
	f := newFixture(t)
	f.jobs.running = true
	_, _, err := f.svc.Save(ctx, ws, "u1", f.project, store.RoleSource, SaveInput{Type: "postgres", Config: pgCfg, Secrets: SecretsPatch{Password: ptr("x")}})
	require.Equal(t, problem.CodeProjectJobRunning, problem.From(err).Code)
	require.Equal(t, problem.CodeProjectJobRunning, problem.From(f.svc.Delete(ctx, ws, "u1", f.project, store.RoleSource)).Code)
}

func TestConnectionsService_AC5_CrossProjectAccessNotFound(t *testing.T) {
	f := newFixture(t)
	c := save(t, f, pgCfg, ptr("pw"))
	_, err := f.svc.Get(ctx, "other-ws", c.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = f.svc.TestStored(ctx, "other-ws", "u1", c.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, _, err = f.svc.Save(ctx, "other-ws", "u1", f.project, store.RoleSource, SaveInput{Type: "postgres", Config: pgCfg, Secrets: SecretsPatch{Password: ptr("x")}})
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestConnectionsService_AC6_TestUnsavedPersistsNothing(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.TestUnsaved(ctx, store.RoleSource, "postgres", pgCfg, SecretsPatch{Password: ptr("typed-pw")})
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Equal(t, "typed-pw", f.pg.creds[0].Password)
	_, err = f.svc.Get(ctx, ws, store.ConnectionID(f.project, store.RoleSource))
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = f.svc.TestUnsaved(ctx, store.RoleSource, "mysql", pgCfg, SecretsPatch{})
	require.Equal(t, problem.CodeUnsupportedSource, problem.From(err).Code)
}

func TestConnectionsService_AC1_InvalidConfigRejected(t *testing.T) {
	f := newFixture(t)
	_, _, err := f.svc.Save(ctx, ws, "u1", f.project, store.RoleSource, SaveInput{
		Type:   "postgres",
		Config: json.RawMessage(`{"host":"","database":"shop","username":"u","port":70000}`), Secrets: SecretsPatch{Password: ptr("x")},
	})
	p := problem.From(err)
	require.Equal(t, 422, p.Status)
	require.True(t, strings.HasPrefix(p.FieldErrors[0].Field, "config."))
}

func TestConnectionsService_AC7_AuditEvents(t *testing.T) {
	f := newFixture(t)
	c := save(t, f, pgCfg, ptr("audit-secret"))
	_ = save(t, f, pgCfg, nil)
	_, _ = f.svc.TestStored(ctx, ws, "u1", c.ID)
	require.NoError(t, f.svc.Delete(ctx, ws, "u1", f.project, store.RoleSource))
	events, err := f.st.Audit().List(ctx, ws, 10)
	require.NoError(t, err)
	var actions []string
	for _, e := range events {
		actions = append(actions, e.Action)
		require.Contains(t, string(e.Metadata), f.project)
		require.NotContains(t, string(e.Metadata), "audit-secret")
	}
	require.Equal(t, []string{"connection.delete", "connection.test", "connection.update", "connection.create"}, actions)
}

func TestOutcome(t *testing.T) {
	require.Equal(t, "ok", Outcome(connectors.TestResult{OK: true}))
	require.Equal(t, "warn", Outcome(connectors.TestResult{OK: true, Steps: []connectors.TestStep{{Status: connectors.StepWarn}}}))
	require.Equal(t, "failed", Outcome(connectors.TestResult{OK: false}))
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

func TestConnectionsHandler_AC5_SaveTestGetDelete(t *testing.T) {
	f := newFixture(t)
	h := router(f, store.RoleEditor)
	body := `{"type":"postgres","config":` + string(pgCfg) + `,"secrets":{"password":"http-secret"}}`
	rec := call(h, "PUT", "/api/v1/projects/"+f.project+"/connections/source", body)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "http-secret")
	require.Contains(t, rec.Body.String(), `"passwordSet":true`)
	require.Equal(t, http.StatusOK, call(h, "PUT", "/api/v1/projects/"+f.project+"/connections/source", `{"type":"postgres","config":`+string(pgCfg)+`}`).Code)

	id := store.ConnectionID(f.project, store.RoleSource)
	test := call(h, "POST", "/api/v1/connections/"+id+"/test", "")
	require.Equal(t, http.StatusOK, test.Code)
	require.Contains(t, test.Body.String(), `"ok":true`)
	require.Contains(t, call(h, "GET", "/api/v1/connections/"+id, "").Body.String(), `"lastTestStatus":"ok"`)

	require.Equal(t, http.StatusNotFound, call(h, "PUT", "/api/v1/projects/"+f.project+"/connections/sideways", body).Code)
	require.Equal(t, http.StatusNoContent, call(h, "DELETE", "/api/v1/projects/"+f.project+"/connections/source", "").Code)
}

func TestConnectionsHandler_AC6_TestUnsaved(t *testing.T) {
	f := newFixture(t)
	rec := call(router(f, store.RoleEditor), "POST", "/api/v1/connections/test",
		`{"role":"source","type":"postgres","config":`+string(pgCfg)+`,"secrets":{"password":"p"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"steps"`)
	require.Equal(t, http.StatusUnprocessableEntity, call(router(f, store.RoleEditor), "POST", "/api/v1/connections/test", `{"role":"middle","type":"x","config":{}}`).Code)
}

func TestConnectionsHandler_ViewerCannotSaveOrTest(t *testing.T) {
	f := newFixture(t)
	h := router(f, store.RoleViewer)
	require.Equal(t, http.StatusForbidden, call(h, "PUT", "/api/v1/projects/"+f.project+"/connections/source", `{"type":"postgres","config":{}}`).Code)
	require.Equal(t, http.StatusForbidden, call(h, "POST", "/api/v1/connections/test", `{}`).Code)
}

func TestConnectionsHandler_DecodesEncodedIDs(t *testing.T) {
	f := newFixture(t)
	h := router(f, store.RoleEditor)
	body := `{"type":"postgres","config":` + string(pgCfg) + `,"secrets":{"password":"p"}}`
	require.Equal(t, http.StatusCreated, call(h, "PUT", "/api/v1/projects/"+f.project+"/connections/source", body).Code)
	// Browsers' URL encoders escape ':' — "p::source" arrives as "p%3A%3Asource".
	encoded := strings.ReplaceAll(store.ConnectionID(f.project, store.RoleSource), ":", "%3A")
	require.Equal(t, http.StatusOK, call(h, "GET", "/api/v1/connections/"+encoded, "").Code)
	require.Equal(t, http.StatusOK, call(h, "POST", "/api/v1/connections/"+encoded+"/test", "").Code)
}

func TestConnectionsService_OpenSourceUsesStoredCredentials(t *testing.T) {
	st := memstore.New(storetest.NewClock())
	fake := &sourcetest.Fake{Type: "fake"}
	sources := source.NewRegistry()
	sources.Register(fake)
	svc := NewService(st, sealer(t), sources, target.NewRegistry(), nil, nil)
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	conn, _, err := svc.Save(ctx, ws, "u1", p.ID, store.RoleSource, SaveInput{Type: "fake", Config: json.RawMessage(`{}`), Secrets: SecretsPatch{Password: ptr("pw")}})
	require.NoError(t, err)

	sess, got, err := svc.OpenSource(ctx, ws, conn.ID)
	require.NoError(t, err)
	require.Equal(t, conn.ID, got.ID)
	require.NoError(t, sess.Close())

	_, _, err = svc.OpenSource(ctx, "other-ws", conn.ID)
	require.ErrorIs(t, err, store.ErrNotFound)

	fake.OpenErr = errors.New("unreachable")
	_, _, err = svc.OpenSource(ctx, ws, conn.ID)
	require.Error(t, err)

	// A target isn't a source.
	_, err = st.Connections().Upsert(ctx, ws, p.ID, store.RoleTarget, store.ConnectionInput{Type: "couchbase", Config: json.RawMessage(`{}`)})
	require.NoError(t, err)
	_, _, err = svc.OpenSource(ctx, ws, store.ConnectionID(p.ID, store.RoleTarget))
	require.True(t, problem.HasCode(err, problem.CodeValidation))
}

// fakeTarget is a target connector with keyspaces (browsable) or without.
type fakeTarget struct {
	typ      string
	gotID    string
	gotVer   string
	gotCreds connectors.Credentials
}

func (f *fakeTarget) Descriptor() connectors.Descriptor {
	return connectors.Descriptor{Type: f.typ, Role: connectors.RoleTarget, DisplayName: f.typ}
}
func (f *fakeTarget) Validate(json.RawMessage) error { return nil }
func (f *fakeTarget) Test(context.Context, json.RawMessage, connectors.Credentials) connectors.TestResult {
	return connectors.TestResult{OK: true}
}
func (f *fakeTarget) Close(context.Context) error { return nil }
func (f *fakeTarget) Capabilities() target.Capabilities {
	return target.Capabilities{RequiresCredentials: true, SupportsCreateLocation: true}
}

type browsableTarget struct{ *fakeTarget }

func (b browsableTarget) Keyspaces(_ context.Context, id, version string, _ json.RawMessage, creds connectors.Credentials) (target.Keyspaces, error) {
	b.gotID, b.gotVer, b.gotCreds = id, version, creds
	return nil, nil
}

func TestConnectionsService_OpenKeyspacesUsesStoredCredentials(t *testing.T) {
	st := memstore.New(storetest.NewClock())
	cb := &fakeTarget{typ: "couchbase"}
	plain := &fakeTarget{typ: "plain"}
	targets := target.NewRegistry()
	targets.Register(browsableTarget{cb})
	targets.Register(plain)
	svc := NewService(st, sealer(t), source.NewRegistry(), targets, nil, nil)
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	conn, _, err := svc.Save(ctx, ws, "u1", p.ID, store.RoleTarget, SaveInput{Type: "couchbase", Config: json.RawMessage(`{}`), Secrets: SecretsPatch{Password: ptr("cb-pw")}})
	require.NoError(t, err)

	_, got, err := svc.OpenKeyspaces(ctx, ws, conn.ID)
	require.NoError(t, err)
	require.Equal(t, conn.ID, got.ID)
	require.Equal(t, conn.ID, cb.gotID)
	require.Equal(t, "1", cb.gotVer, "pooled by connection version")
	require.Equal(t, "cb-pw", cb.gotCreds.Password)

	_, _, err = svc.OpenKeyspaces(ctx, "other", conn.ID)
	require.ErrorIs(t, err, store.ErrNotFound)

	_, err = st.Connections().Upsert(ctx, ws, p.ID, store.RoleSource, store.ConnectionInput{Type: "postgres", Config: json.RawMessage(`{}`)})
	require.NoError(t, err)
	_, _, err = svc.OpenKeyspaces(ctx, ws, store.ConnectionID(p.ID, store.RoleSource))
	require.True(t, problem.HasCode(err, problem.CodeValidation), "a source has no keyspaces")

	_, _, err = svc.Save(ctx, ws, "u1", p.ID, store.RoleTarget, SaveInput{Type: "plain", Config: json.RawMessage(`{}`), Secrets: SecretsPatch{Password: ptr("x")}})
	require.NoError(t, err)
	_, _, err = svc.OpenKeyspaces(ctx, ws, conn.ID)
	require.True(t, problem.HasCode(err, problem.CodeUnsupportedTarget))
}
