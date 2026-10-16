package relations

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

// fixedSchema serves one snapshot for every connection.
type fixedSchema struct{ snap *schema.Snapshot }

func (f fixedSchema) Latest(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
	return store.DiscoverySnapshot{}, f.snap, nil
}

// counterSession answers cardinality detection.
type counterSession struct {
	source.Session
	perKey  map[string]int64 // table → most rows per key
	timeout bool
}

func (c counterSession) MaxRowsPerKey(_ context.Context, _, table string, _ []string, _ int, _ time.Duration) (int64, int64, error) {
	if c.timeout {
		return 0, 0, source.ErrQueryTimeout
	}
	return c.perKey[table], 100, nil
}

func (c counterSession) Close() error { return nil }

type apiFixture struct {
	st      *memstore.Store
	h       http.Handler
	project string
	sess    *counterSession
}

const ws = store.WorkspaceID("ws1")

func newAPI(t *testing.T, principalWS store.WorkspaceID) *apiFixture {
	t.Helper()
	ctx := context.Background()
	st := memstore.New(storetest.NewClock())
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	_, err = st.Connections().Upsert(ctx, ws, p.ID, store.RoleSource, store.ConnectionInput{Type: "postgres", Name: "src"})
	require.NoError(t, err)
	f := &apiFixture{st: st, project: p.ID, sess: &counterSession{perKey: map[string]int64{"customers": 1, "orders": 40}}}
	svc := NewService(st, fixedSchema{snap: shopSnap()}, func(context.Context, store.WorkspaceID, string) (source.Session, store.Connection, error) {
		return *f.sess, store.Connection{}, nil
	})
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u1", WorkspaceID: principalWS, Role: store.RoleOwner})))
		})
	}
	f.h = httpapi.NewRouter(httpapi.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Features: []httpapi.Registrar{Handlers{Svc: svc}},
		Middleware: []func(http.Handler) http.Handler{withPrincipal},
	})
	return f
}

func (f *apiFixture) do(method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	f.h.ServeHTTP(rec, req)
	return rec
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p problem.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p), rec.Body.String())
	return p.Code
}

const custCode = `{"from":{"schema":"public","table":"orders","columns":["cust_code"]},"to":{"schema":"public","table":"customers","columns":["code"]}}`

func TestRelationshipsAPI_AC3_CRUD(t *testing.T) {
	f := newAPI(t, ws)
	base := "/api/v1/projects/" + f.project + "/relationships"
	rec := f.do(http.MethodPost, base, custCode)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var rel store.Relationship
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rel))
	require.Equal(t, "orders.cust_code → customers.code", rel.Name)
	require.Equal(t, CardinalityManyToOne, rel.Cardinality)

	rec = f.do(http.MethodGet, base, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), rel.ID)

	// It appears in the graph as a virtual hop.
	rec = f.do(http.MethodGet, "/api/v1/connections/"+store.ConnectionID(f.project, store.RoleSource)+"/relations?root=public.orders&depth=2", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var g GraphResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &g))
	virtual := 0
	for _, p := range g.Paths {
		if p.Hops[0].RelationshipID == rel.ID {
			virtual++
		}
	}
	require.Positive(t, virtual)
	require.NotEmpty(t, g.Junctions, "orders ↔ products via order_items")

	rec = f.do(http.MethodPut, base+"/"+rel.ID, strings.Replace(custCode, `"to"`, `"cardinality":"one-to-one","to"`, 1))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"cardinality":"one-to-one"`)
	require.Equal(t, http.StatusNoContent, f.do(http.MethodDelete, base+"/"+rel.ID, "").Code)
	require.Equal(t, http.StatusNotFound, f.do(http.MethodGet, base+"/"+rel.ID, "").Code)
}

func TestRelationshipsAPI_RejectsSelfAndDuplicates(t *testing.T) {
	f := newAPI(t, ws)
	base := "/api/v1/projects/" + f.project + "/relationships"
	self := `{"from":{"schema":"public","table":"orders","columns":["order_id"]},"to":{"schema":"public","table":"orders","columns":["order_id"]}}`
	rec := f.do(http.MethodPost, base, self)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "joins each row of orders to itself")

	require.Equal(t, http.StatusCreated, f.do(http.MethodPost, base, custCode).Code)
	rec = f.do(http.MethodPost, base, custCode)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "already exists")
	reversed := `{"from":{"schema":"public","table":"customers","columns":["code"]},"to":{"schema":"public","table":"orders","columns":["cust_code"]}}`
	require.Equal(t, http.StatusConflict, f.do(http.MethodPost, base, reversed).Code, "the same pair the other way round")
}

func TestRelationshipsAPI_AC3_TypeMismatch(t *testing.T) {
	f := newAPI(t, ws)
	snap := shopSnap()
	snap.Table("public", "customers").Columns = append(snap.Table("public", "customers").Columns, schema.Column{Name: "meta", UDTName: "jsonb"})
	svc := NewService(f.st, fixedSchema{snap: snap}, nil)
	_, err := svc.Create(context.Background(), ws, "u1", f.project, RelationshipInput{
		From: store.RelationshipEnd{Schema: "public", Table: "orders", Columns: []string{"customer_id"}},
		To:   store.RelationshipEnd{Schema: "public", Table: "customers", Columns: []string{"meta"}},
	})
	p := problem.From(err)
	require.Equal(t, http.StatusUnprocessableEntity, p.Status)
	require.Equal(t, CodeRelationTypeMismatch, p.Code)
	require.Contains(t, p.Detail, "int4")

	// Another workspace doesn't see the project.
	other := newAPI(t, "ws-other")
	other.project = f.project
	rec := other.do(http.MethodPost, "/api/v1/projects/"+f.project+"/relationships", custCode)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Equal(t, problem.CodeNotFound, problemCode(t, rec))
}

func TestDetectCardinality_AC4_Timeout(t *testing.T) {
	f := newAPI(t, ws)
	base := "/api/v1/projects/" + f.project + "/relationships/detect"
	rec := f.do(http.MethodPost, base, custCode)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var d Detection
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &d))
	require.Equal(t, CardinalityManyToOne, d.Suggested, "customers.code is unique in the sample")

	f.sess.perKey["customers"] = 3
	rec = f.do(http.MethodPost, base, strings.Replace(custCode, `"to"`, `"cardinality":"many-to-one","to"`, 1))
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &d))
	require.Equal(t, CardinalityOneToMany, d.Suggested)
	require.EqualValues(t, 3, d.MaxPerKey)
	require.Contains(t, d.Warning, "contradicts many-to-one", "the chosen cardinality disagrees with the data")

	f.sess.timeout = true
	rec = f.do(http.MethodPost, base, custCode)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &d))
	require.Equal(t, CardinalityUnknown, d.Suggested, "the user must choose")
	require.Contains(t, d.Warning, "timed out")
}

func TestRelationshipsAPI_BadRequests(t *testing.T) {
	f := newAPI(t, ws)
	base := "/api/v1/projects/" + f.project + "/relationships"
	graph := "/api/v1/connections/" + store.ConnectionID(f.project, store.RoleSource) + "/relations"
	for _, path := range []string{
		graph, graph + "?root=orders", graph + "?root=.orders", graph + "?root=public.orders&depth=0",
		graph + "?root=public.orders&depth=7", graph + "?root=public.orders&depth=x",
	} {
		rec := f.do(http.MethodGet, path, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, path)
		require.Equal(t, problem.CodeBadRequest, problemCode(t, rec), path)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, base}, {http.MethodPost, base + "/detect"}, {http.MethodPut, base + "/r1"},
	} {
		rec := f.do(c.method, c.path, "{")
		require.Equal(t, http.StatusBadRequest, rec.Code, c.method+" "+c.path)
	}

	rec := f.do(http.MethodPost, base, custCode)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var rel store.Relationship
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rel))
	put := func(ifMatch string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, base+"/"+rel.ID, strings.NewReader(custCode))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", ifMatch)
		f.h.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusBadRequest, put(`"zero"`).Code)
	require.Equal(t, http.StatusBadRequest, put(`"0"`).Code)
	require.Equal(t, http.StatusPreconditionFailed, put(`W/"99"`).Code, "a stale version")
	require.Equal(t, http.StatusOK, put(`"`+strconv.FormatInt(rel.Version, 10)+`"`).Code)
}

func TestRelationshipsAPI_OtherWorkspaceSeesNothing(t *testing.T) {
	f := newAPI(t, "ws-other")
	base := "/api/v1/projects/" + f.project + "/relationships"
	graph := "/api/v1/connections/" + store.ConnectionID(f.project, store.RoleSource) + "/relations?root=public.orders"
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, graph, ""},
		{http.MethodGet, base, ""},
		{http.MethodGet, base + "/r1", ""},
		{http.MethodPost, base, custCode},
		{http.MethodPost, base + "/detect", custCode},
		{http.MethodPut, base + "/r1", custCode},
		{http.MethodDelete, base + "/r1", ""},
	} {
		rec := f.do(c.method, c.path, c.body)
		require.Equal(t, http.StatusNotFound, rec.Code, c.method+" "+c.path+": "+rec.Body.String())
	}
}
