package keyspaces

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
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

const ws store.WorkspaceID = "ws-1"

// fakeKeyspaces records calls and returns canned results.
type fakeKeyspaces struct {
	createErr error
	calls     []string
	gotBucket target.BucketSpec
	gotColl   target.CollectionSpec
}

func (f *fakeKeyspaces) ListBuckets(context.Context) (target.BucketList, error) {
	return target.BucketList{Buckets: []target.BucketInfo{{Name: "travel-sample", CanManage: true}}, Capabilities: []byte(`{"canCreateBucket":false}`)}, nil
}

func (f *fakeKeyspaces) ListScopes(_ context.Context, b string) ([]target.ScopeInfo, error) {
	if b == "missing" {
		return nil, problem.New(http.StatusNotFound, problem.CodeNotFound, "Bucket not found", "")
	}
	return []target.ScopeInfo{{Name: "_default", Collections: []target.CollectionInfo{{Name: "_default"}}}}, nil
}

func (f *fakeKeyspaces) Collection(_ context.Context, _, _, c string) (target.CollectionInfo, error) {
	n := int64(187)
	return target.CollectionInfo{Name: c, DocCountEstimate: &n}, nil
}

func (f *fakeKeyspaces) SampleDocuments(context.Context, string, string, string, int) ([]json.RawMessage, string, error) {
	return []json.RawMessage{json.RawMessage(`{"a":1}`)}, "query", nil
}

func (f *fakeKeyspaces) ExistingKeys(context.Context, string, string, string, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (f *fakeKeyspaces) CreateBucket(_ context.Context, s target.BucketSpec) error {
	f.calls = append(f.calls, "bucket")
	f.gotBucket = s
	return f.createErr
}

func (f *fakeKeyspaces) CreateScope(_ context.Context, b, s string) error {
	f.calls = append(f.calls, "scope:"+b+"."+s)
	return f.createErr
}

func (f *fakeKeyspaces) CreateCollection(_ context.Context, b, s string, c target.CollectionSpec) error {
	f.calls = append(f.calls, "collection:"+b+"."+s+"."+c.Name)
	f.gotColl = c
	return f.createErr
}

type fixture struct {
	fake    *fakeKeyspaces
	st      *memstore.Store
	openErr error
}

func (f *fixture) router(role store.MemberRole) http.Handler {
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u1", WorkspaceID: ws, Role: role})))
		})
	}
	open := func(_ context.Context, _ store.WorkspaceID, id string) (target.Keyspaces, store.Connection, error) {
		if f.openErr != nil {
			return nil, store.Connection{}, f.openErr
		}
		return f.fake, store.Connection{ID: id, ProjectID: "p1"}, nil
	}
	return httpapi.NewRouter(httpapi.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Features:   []httpapi.Registrar{Handlers{Open: open, Audit: f.st.Audit()}},
		Middleware: []func(http.Handler) http.Handler{withPrincipal},
	})
}

func newFixture() *fixture {
	return &fixture{fake: &fakeKeyspaces{}, st: memstore.New(storetest.NewClock())}
}

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

const base = "/api/v1/connections/p1%3A%3Atarget"

func TestKeyspaceHandlers_AC1_Browse(t *testing.T) {
	h := newFixture().router(store.RoleViewer)
	rec := call(h, "GET", base+"/buckets", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"buckets":[{"name":"travel-sample","numReplicas":0,"canManage":true}],"partial":false,"capabilities":{"canCreateBucket":false}}`, rec.Body.String())

	rec = call(h, "GET", base+"/buckets/travel-sample/scopes", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"scopes":[{"name":"_default","collections":[{"name":"_default","maxExpirySec":0}]}]}`, rec.Body.String())
	require.Equal(t, http.StatusNotFound, call(h, "GET", base+"/buckets/missing/scopes", "").Code)

	rec = call(h, "GET", base+"/buckets/travel-sample/scopes/inventory/collections/airline", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"name":"airline","maxExpirySec":0,"docCountEstimate":187}`, rec.Body.String())
}

func TestCreateHandler_AC2_CreatedAndPending(t *testing.T) {
	f := newFixture()
	h := f.router(store.RoleEditor)
	rec := call(h, "POST", base+"/buckets/travel-sample/scopes/inventory/collections", `{"name":"orders","maxExpirySec":3600}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"status":"ready","bucket":"travel-sample","scope":"inventory","collection":"orders"}`, rec.Body.String())
	require.Equal(t, int64(3600), f.fake.gotColl.MaxExpirySec)

	f.fake.createErr = target.ErrPending
	rec = call(h, "POST", base+"/buckets/travel-sample/scopes", `{"name":"tenant"}`)
	require.Equal(t, http.StatusAccepted, rec.Code)
	require.JSONEq(t, `{"status":"pending","bucket":"travel-sample","scope":"tenant"}`, rec.Body.String())

	f.fake.createErr = nil
	rec = call(h, "POST", base+"/buckets", `{"name":"orders","ramQuotaMB":256,"bucketType":"couchbase","numReplicas":1,"storageBackend":"couchstore"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, target.BucketSpec{Name: "orders", RAMQuotaMB: 256, BucketType: "couchbase", NumReplicas: 1, StorageBackend: "couchstore"}, f.fake.gotBucket)

	events, err := f.st.Audit().List(context.Background(), ws, 10)
	require.NoError(t, err)
	require.Len(t, events, 3)
	for _, e := range events {
		require.Equal(t, "keyspace.create", e.Action)
	}
	require.Contains(t, string(events[0].Metadata), `"bucket":"orders"`)
}

func TestCreate_AC3_ExistsIs409(t *testing.T) {
	f := newFixture()
	f.fake.createErr = problem.New(http.StatusConflict, problem.CodeKeyspaceExists, "Collection already exists", "")
	rec := call(f.router(store.RoleEditor), "POST", base+"/buckets/b/scopes/s/collections", `{"name":"route"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeKeyspaceExists)
}

func TestCreateHandler_AC4_PermissionDenied(t *testing.T) {
	f := newFixture()
	// Viewers never reach the connector.
	rec := call(f.router(store.RoleViewer), "POST", base+"/buckets/b/scopes", `{"name":"s"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Empty(t, f.fake.calls, "SDK fake not called")

	f.fake.createErr = &connectors.ConnError{Role: connectors.RoleTarget, Kind: connectors.KindPermission, Cause: errors.New("this credential can't create scopes")}
	rec = call(f.router(store.RoleEditor), "POST", base+"/buckets/b/scopes", `{"name":"s"}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), "TARGET_PERMISSION_DENIED")
}

func TestCreateHandler_AC4_CapellaBucket(t *testing.T) {
	f := newFixture()
	f.fake.createErr = problem.New(http.StatusUnprocessableEntity, problem.CodeCapellaBucketCreate, "Create buckets in Capella", "")
	rec := call(f.router(store.RoleEditor), "POST", base+"/buckets", `{"name":"b","ramQuotaMB":256,"bucketType":"couchbase"}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeCapellaBucketCreate)
}

func TestKeyspaceHandlers_BadRequestsAndOpenErrors(t *testing.T) {
	f := newFixture()
	h := f.router(store.RoleEditor)
	require.Equal(t, http.StatusUnprocessableEntity, call(h, "POST", base+"/buckets/b/scopes", `{}`).Code)
	require.Equal(t, http.StatusBadRequest, call(h, "POST", base+"/buckets/b/scopes/s/collections", `{"name":"c","bogus":1}`).Code)
	require.Equal(t, http.StatusBadRequest, call(h, "POST", base+"/buckets", `nope`).Code)

	f.openErr = store.ErrNotFound
	for _, req := range [][3]string{
		{"GET", base + "/buckets", ""},
		{"GET", base + "/buckets/b/scopes", ""},
		{"GET", base + "/buckets/b/scopes/s/collections/c", ""},
		{"POST", base + "/buckets", `{"name":"b","ramQuotaMB":256,"bucketType":"couchbase"}`},
		{"POST", base + "/buckets/b/scopes", `{"name":"s"}`},
		{"POST", base + "/buckets/b/scopes/s/collections", `{"name":"c"}`},
	} {
		require.Equal(t, http.StatusNotFound, call(h, req[0], req[1], req[2]).Code, req[1])
	}
	require.Empty(t, f.fake.calls)
}
