package documents

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

const ws store.WorkspaceID = "ws-1"

// fakeTargets serves documents from a map by key; "evil" is a rejected path.
type fakeTargets struct {
	docs   map[string]target.Document
	gotLoc store.Location
	gotCur string
}

func (f *fakeTargets) FindDocument(_ context.Context, _ store.WorkspaceID, _ string, loc store.Location, key string) (target.Document, error) {
	f.gotLoc = loc
	if key == "../../evil" {
		return target.Document{}, &directory.PathRejected{Key: key}
	}
	d, ok := f.docs[key]
	if !ok {
		return target.Document{}, problem.New(http.StatusNotFound, problem.CodeDocumentNotFound, "Document not found", "No document with key "+key+".")
	}
	return d, nil
}

func (f *fakeTargets) ListOutput(_ context.Context, _ store.WorkspaceID, _ string, _ store.Location, _, cursor string, _ int) ([]target.OutputItem, string, error) {
	f.gotCur = cursor
	return []target.OutputItem{{Key: "1001", Path: "orders/1001.json", Bytes: 12}}, "1001", nil
}

func setup(t *testing.T) (http.Handler, *memstore.Store, *fakeTargets, store.Mapping, store.Job) {
	t.Helper()
	st := memstore.New(storetest.NewClock())
	ctx := context.Background()
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	nm := store.Mapping{ProjectID: p.ID, SourceSchema: "shop", SourceTable: "orders", Spec: json.RawMessage(`{}`)}
	nm.SetLocation(store.Location{Kind: store.KindDirectory, Folder: "orders"})
	m, err := st.Mappings().Create(ctx, ws, nm)
	require.NoError(t, err)
	j, err := st.Jobs().Create(ctx, ws, store.Job{ProjectID: p.ID, MappingIDs: []string{m.ID}, Options: json.RawMessage(`{}`)})
	require.NoError(t, err)
	mod := storetest.NewClock().Now()
	ft := &fakeTargets{docs: map[string]target.Document{
		"1001":      {Key: "1001", Path: "orders/1001.json", Bytes: 12, ModifiedAt: &mod, Body: json.RawMessage(`{"id":1001}`)},
		"orders::7": {Key: "orders::7", Path: "orders/orders%3A%3A7.json", Bytes: 8, Body: json.RawMessage(`{"id":7}`)},
		"2026/10":   {Key: "2026/10", Path: "orders/2026%2F10.json", Bytes: 9, Body: json.RawMessage(`{"a":1}`)},
	}}
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u1", WorkspaceID: ws, Role: store.RoleViewer})))
		})
	}
	h := httpapi.NewRouter(httpapi.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Features:   []httpapi.Registrar{Handlers{Store: st, Targets: ft}},
		Middleware: []func(http.Handler) http.Handler{withPrincipal},
	})
	return h, st, ft, m, j
}

func get(h http.Handler, url string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec
}

func TestGetDocument_AC5_DirectoryAndCouchbase(t *testing.T) {
	h, _, ft, m, _ := setup(t)
	base := "/api/v1/projects/" + m.ProjectID + "/mappings/" + m.ID + "/documents/"
	rec := get(h, base+"1001")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var v map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	require.Equal(t, "orders/1001.json", v["path"])
	require.Equal(t, map[string]any{"id": float64(1001)}, v["document"])
	require.Equal(t, "orders", ft.gotLoc.Folder, "the mapping's location is used")

	// A key with a slash is the rest of the path.
	require.Equal(t, http.StatusOK, get(h, base+"2026/10").Code)
	// Keys arrive percent-encoded from the web app (encodeURIComponent): "::" and "/" decode.
	require.Equal(t, http.StatusOK, get(h, base+"orders%3A%3A7").Code, "a default key, table::id")
	require.Equal(t, http.StatusOK, get(h, base+"2026%2F10").Code)

	rec = get(h, base+"404")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeDocumentNotFound)

	rec = get(h, base+"1001?download=1")
	require.Equal(t, `attachment; filename="1001.json"`, rec.Header().Get("Content-Disposition"))
	require.JSONEq(t, `{"id":1001}`, rec.Body.String())

	require.Equal(t, http.StatusBadRequest, get(h, base).Code)
	require.Equal(t, http.StatusNotFound, get(h, "/api/v1/projects/other/mappings/"+m.ID+"/documents/1001").Code, "another project's mapping")
}

func TestGetDocument_AC5_Confinement(t *testing.T) {
	h, st, _, m, _ := setup(t)
	rec := get(h, "/api/v1/projects/"+m.ProjectID+"/mappings/"+m.ID+"/documents/../../evil")
	require.Equal(t, http.StatusNotFound, rec.Code)
	evs, err := st.Audit().List(context.Background(), ws, 10)
	require.NoError(t, err)
	require.Len(t, evs, 1)
	require.Equal(t, "output.path_rejected", evs[0].Action)
}

func TestJobOutput_Pages(t *testing.T) {
	h, _, ft, m, j := setup(t)
	rec := get(h, "/api/v1/jobs/"+j.ID+"/output?cursor=0999&limit=10")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var page OutputPage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Equal(t, m.ID, page.MappingID, "defaults to the job's first mapping")
	require.Equal(t, "1001", page.NextCursor)
	require.Equal(t, "0999", ft.gotCur)
	require.Equal(t, http.StatusNotFound, get(h, "/api/v1/jobs/"+j.ID+"/output?mapping=other").Code)
	require.Equal(t, http.StatusNotFound, get(h, "/api/v1/jobs/nope/output").Code)
}
