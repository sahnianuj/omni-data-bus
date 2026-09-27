package discovery

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
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/sourcetest"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

var ctx = context.Background()

const ws store.WorkspaceID = "ws-1"

type fixture struct {
	svc    *Service
	st     *memstore.Store
	fake   *sourcetest.Fake
	connID string
	opens  int
}

func shopSnapshot(tables ...string) *schema.Snapshot {
	s := schema.Schema{Name: "shop"}
	for _, t := range tables {
		s.Tables = append(s.Tables, schema.Table{
			Name: t, Kind: schema.KindTable, Readable: true,
			Columns:    []schema.Column{{Name: "id", Ordinal: 1, DataType: "integer", UDTName: "int4", JSONType: schema.TypeInteger, IsPrimaryKey: true}},
			PrimaryKey: []string{"id"},
		})
	}
	return &schema.Snapshot{Database: "shop", ServerVersion: "16.3", Schemas: []schema.Schema{s}}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := memstore.New(storetest.NewClock())
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	conn, err := st.Connections().Upsert(ctx, ws, p.ID, store.RoleSource, store.ConnectionInput{Type: "fake", Name: "shop", Config: json.RawMessage(`{}`), LastTestStatus: "ok"})
	require.NoError(t, err)
	f := &fixture{st: st, fake: &sourcetest.Fake{Snapshot: shopSnapshot("customer", "orders"), Relations: 2}, connID: conn.ID}
	open := func(ctx context.Context, w store.WorkspaceID, id string) (source.Session, store.Connection, error) {
		c, err := st.Connections().Get(ctx, w, id)
		if err != nil {
			return nil, c, err
		}
		f.opens++
		sess, err := f.fake.Open(ctx, c.Config, connectors.Credentials{})
		return sess, c, err
	}
	f.svc = NewService(st, open, storetest.NewClock())
	return f
}

func TestDiscoveryService_AC1_SyncStoresSnapshot(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	require.False(t, res.Async)
	require.Equal(t, StatusCompleted, res.Status.Status)
	require.Equal(t, 2, res.Status.TableCount)
	require.Nil(t, res.Status.Diff, "no previous snapshot, no diff")

	rec, snap, err := f.svc.Latest(ctx, ws, f.connID)
	require.NoError(t, err)
	require.Equal(t, res.Status.SnapshotID, rec.ID)
	require.NotNil(t, snap.Table("shop", "orders"))

	events, err := f.st.Audit().List(ctx, ws, 10)
	require.NoError(t, err)
	require.Equal(t, "connection.discover", events[0].Action)
}

func TestDiscoveryService_AC5_AsyncAboveThreshold(t *testing.T) {
	f := newFixture(t)
	f.fake.Relations = AsyncThreshold + 1
	res, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	require.True(t, res.Async)
	require.Equal(t, StatusRunning, res.Status.Status)
	require.NotEmpty(t, res.Status.ID)

	f.svc.Wait()
	st, err := f.svc.Status(ws, f.connID, res.Status.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, st.Status)
	require.NotEmpty(t, st.SnapshotID)
	require.NotNil(t, st.FinishedAt)

	_, err = f.svc.Status("other-ws", f.connID, res.Status.ID)
	require.ErrorIs(t, err, store.ErrNotFound, "runs are workspace-scoped")
	_, err = f.svc.Status(ws, "other-conn", res.Status.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestDiscoveryService_AC5_SingleFlight(t *testing.T) {
	f := newFixture(t)
	f.fake.DiscoverGate = make(chan struct{})
	first := make(chan Result, 1)
	go func() {
		res, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
		require.NoError(t, err)
		first <- res
	}()
	require.Eventually(t, func() bool { return f.fake.Discovers.Load() == 1 }, 2*time.Second, 5*time.Millisecond)

	second, err := f.svc.Discover(ctx, ws, "u2", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	require.True(t, second.Async, "joins the in-flight run")
	require.Equal(t, StatusRunning, second.Status.Status)

	close(f.fake.DiscoverGate)
	res := <-first
	require.Equal(t, res.Status.ID, second.Status.ID, "same discovery ID")
	require.Equal(t, int32(1), f.fake.Discovers.Load(), "one Discover call")
	require.Equal(t, 1, f.opens, "the joined request didn't open a session")

	// Once finished, the next request starts a new run.
	f.fake.DiscoverGate = nil
	third, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	require.NotEqual(t, res.Status.ID, third.Status.ID)
}

func TestDiscoveryService_AC6_LatestFlagAndRetention(t *testing.T) {
	f := newFixture(t)
	var last Status
	for range Retention + 2 {
		res, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
		require.NoError(t, err)
		last = res.Status
	}
	ids, err := f.st.Snapshots().ListIDs(ctx, ws, f.connID)
	require.NoError(t, err)
	require.Len(t, ids, Retention)
	require.Equal(t, last.SnapshotID, ids[0])
	rec, _, err := f.svc.Latest(ctx, ws, f.connID)
	require.NoError(t, err)
	require.True(t, rec.IsLatest)
	prev, err := f.st.Snapshots().Get(ctx, ws, ids[1])
	require.NoError(t, err)
	require.False(t, prev.IsLatest)
}

func TestDiscoveryService_AC7_RediscoveryReturnsDiff(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	f.fake.Snapshot = shopSnapshot("customer", "invoice")
	res, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	require.NotNil(t, res.Status.Diff)
	require.Equal(t, []string{"shop.invoice"}, res.Status.Diff.AddedTables)
	require.Equal(t, []string{"shop.orders"}, res.Status.Diff.RemovedTables)
}

func TestDiscoveryService_FailureIsReportedAndReleasesTheConnection(t *testing.T) {
	f := newFixture(t)
	f.fake.DiscoverErr = &connectors.ConnError{Role: connectors.RoleSource, Kind: connectors.KindPermission, Cause: errors.New("permission denied for schema billing")}
	_, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.Error(t, err)
	require.Equal(t, "SOURCE_PERMISSION_DENIED", problem.From(err).Code)

	// Async failures are recorded on the run.
	f.fake.Relations = AsyncThreshold + 1
	res, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	f.svc.Wait()
	st, err := f.svc.Status(ws, f.connID, res.Status.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, st.Status)
	require.Equal(t, "SOURCE_PERMISSION_DENIED", st.Error.Code)

	_, _, err = f.svc.Latest(ctx, ws, f.connID)
	require.True(t, problem.HasCode(err, problem.CodeSchemaNotDiscovered))
}

func TestDiscoveryService_OpenErrorsPropagate(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Discover(ctx, ws, "u1", "missing", source.DiscoverOptions{})
	require.ErrorIs(t, err, store.ErrNotFound)
	f.fake.OpenErr = errors.New("boom")
	_, err = f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.Error(t, err)
	_, _, err = f.svc.Latest(ctx, ws, "missing")
	require.ErrorIs(t, err, store.ErrNotFound)
}

// --- HTTP ---

func router(f *fixture, role store.MemberRole) http.Handler {
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

func call(h http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	h.ServeHTTP(rec, req)
	return rec
}

func TestDiscoverHandler_AC1_DiscoverThenReadSchema(t *testing.T) {
	f := newFixture(t)
	h := router(f, store.RoleEditor)
	base := "/api/v1/connections/" + f.connID

	rec := call(h, "GET", base+"/schema", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeSchemaNotDiscovered)

	rec = call(h, "POST", base+"/discover", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var st Status
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	require.Equal(t, StatusCompleted, st.Status)

	rec = call(h, "GET", base+"/discoveries/"+st.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, http.StatusNotFound, call(h, "GET", base+"/discoveries/nope", "").Code)

	rec = call(h, "POST", base+"/discover", `{"schemas":["shop"],"exclude":["*_archive"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, http.StatusBadRequest, call(h, "POST", base+"/discover", `{"bogus":1}`).Code)

	rec = call(h, "GET", base+"/schema/shop/orders", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var tb schema.Table
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tb))
	require.Equal(t, "orders", tb.Name)
	require.Len(t, tb.Columns, 1)
	rec = call(h, "GET", base+"/schema/shop/nope", "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "shop.nope")
}

func TestDiscoverHandler_AC5_AsyncReturns202(t *testing.T) {
	f := newFixture(t)
	f.fake.Relations = AsyncThreshold + 1
	rec := call(router(f, store.RoleEditor), "POST", "/api/v1/connections/"+f.connID+"/discover", "")
	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Contains(t, rec.Body.String(), `"status":"running"`)
	f.svc.Wait()
}

func TestDiscoverHandler_ViewerCanReadButNotDiscover(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	h := router(f, store.RoleViewer)
	require.Equal(t, http.StatusForbidden, call(h, "POST", "/api/v1/connections/"+f.connID+"/discover", "").Code)
	require.Equal(t, http.StatusOK, call(h, "GET", "/api/v1/connections/"+f.connID+"/schema", "").Code)
}

func TestSchemaHandler_AC6_ETag304(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	h := router(f, store.RoleViewer)
	url := "/api/v1/connections/" + f.connID + "/schema"

	rec := call(h, "GET", url, "")
	require.Equal(t, http.StatusOK, rec.Code)
	tag := rec.Header().Get("ETag")
	require.NotEmpty(t, tag)
	require.Equal(t, http.StatusNotModified, call(h, "GET", url, "", "If-None-Match", tag).Code)
	require.Equal(t, http.StatusOK, call(h, "GET", url, "", "If-None-Match", `"stale"`).Code)

	summaryTag := call(h, "GET", url+"?summary=true", "").Header().Get("ETag")
	require.NotEqual(t, tag, summaryTag, "the summary is a different representation")

	tableURL := url + "/shop/orders"
	tableTag := call(h, "GET", tableURL, "").Header().Get("ETag")
	require.Equal(t, http.StatusNotModified, call(h, "GET", tableURL, "", "If-None-Match", tableTag).Code)

	// A new snapshot invalidates the tag.
	_, err = f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, call(h, "GET", url, "", "If-None-Match", tag).Code)
}

func TestSchemaHandler_AC6_SummaryShape(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	rec := call(router(f, store.RoleViewer), "GET", "/api/v1/connections/"+f.connID+"/schema?summary=true", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		SnapshotID string `json:"snapshotId"`
		TableCount int    `json:"tableCount"`
		Database   string `json:"database"`
		Schemas    []struct {
			Name   string                       `json:"name"`
			Tables []map[string]json.RawMessage `json:"tables"`
		} `json:"schemas"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 2, body.TableCount)
	require.Equal(t, "shop", body.Database)
	tb := body.Schemas[0].Tables[0]
	require.NotContains(t, tb, "columns", "summary omits columns")
	require.NotContains(t, tb, "indexes")
	require.JSONEq(t, "1", string(tb["columnCount"]))
	require.JSONEq(t, "true", string(tb["hasPrimaryKey"]))

	full := call(router(f, store.RoleViewer), "GET", "/api/v1/connections/"+f.connID+"/schema", "")
	require.Contains(t, full.Body.String(), `"columns":[`)
}

func TestDiscoveryService_AtMostTwoConcurrentPerWorkspace(t *testing.T) {
	f := newFixture(t)
	r1, _, err := f.svc.start(ws, "c1")
	require.NoError(t, err)
	_, _, err = f.svc.start(ws, "c2")
	require.NoError(t, err)
	_, _, err = f.svc.start(ws, "c3")
	p := problem.From(err)
	require.Equal(t, http.StatusTooManyRequests, p.Status, "a 3rd concurrent discovery")
	require.Equal(t, problem.CodeTooManyConcurrentOps, p.Code)

	_, joined, err := f.svc.start(ws, "c1")
	require.NoError(t, err)
	require.True(t, joined, "joining an in-flight run doesn't need a slot")
	_, _, err = f.svc.start("other-ws", "c9")
	require.NoError(t, err, "workspaces have separate limits")

	f.svc.finish(r1, nil, errors.New("boom"))
	_, _, err = f.svc.start(ws, "c3")
	require.NoError(t, err, "a finished run frees its slot")
}
