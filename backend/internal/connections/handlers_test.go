package connections

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

// kindsFixture serves the connection routes with a Couchbase-like kind and a directory kind.
type kindsFixture struct {
	st      *memstore.Store
	h       http.Handler
	project string
}

func newKindsFixture(t *testing.T, directoryRoots []string) *kindsFixture {
	t.Helper()
	st := memstore.New(storetest.NewClock())
	targets := target.NewRegistry()
	targets.Register(&fakeTarget{typ: store.KindCouchbase})
	targets.Register(directory.New(directoryRoots))
	svc := NewService(st, sealer(t), source.NewRegistry(), targets, nil, nil)
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u1", WorkspaceID: ws, Role: store.RoleOwner})))
		})
	}
	h := httpapi.NewRouter(httpapi.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Features:   []httpapi.Registrar{Handlers{Svc: svc}},
		Middleware: []func(http.Handler) http.Handler{withPrincipal},
	})
	return &kindsFixture{st: st, h: h, project: p.ID}
}

func (f *kindsFixture) putTarget(body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+f.project+"/connections/target", strings.NewReader(body)))
	return rec
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p problem.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p), rec.Body.String())
	return p.Code
}

const couchbaseBody = `{"type":"couchbase","config":{},"secrets":{"password":"pw"}}`

func TestPutTarget_AC1_UnknownKind(t *testing.T) {
	f := newKindsFixture(t, []string{})
	rec := f.putTarget(`{"type":"s3","config":{}}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, problem.CodeTargetKindUnknown, code(t, rec))

	// A kind the server has switched off (no folder it may write to) is refused with the reason.
	rec = f.putTarget(`{"type":"directory","config":{"path":"/data/odb-out"}}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, problem.CodeTargetKindDisabled, code(t, rec))
	require.Contains(t, rec.Body.String(), "no folder it can write to")
}

func TestTargetKinds_AC1_Listed(t *testing.T) {
	f := newKindsFixture(t, []string{})
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/target-kinds", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Kinds []target.KindInfo `json:"kinds"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Kinds, 2)
	var types []string
	for _, k := range body.Kinds {
		types = append(types, k.Type)
		if k.Type == store.KindDirectory {
			require.False(t, k.Enabled)
			require.False(t, k.Capabilities.SupportsExpiry)
		}
	}
	require.ElementsMatch(t, []string{"couchbase", "directory"}, types)
}

func TestPutTarget_AC3_KindChangeGuard(t *testing.T) {
	f := newKindsFixture(t, []string{"/data/odb-out"})
	require.Equal(t, http.StatusCreated, f.putTarget(couchbaseBody).Code)
	m, err := f.st.Mappings().Create(ctx, ws, store.Mapping{
		ProjectID: f.project, SourceSchema: "shop", SourceTable: "orders",
		Target: store.MappingTarget{Bucket: "shop", Scope: "sales", Collection: "orders"}, Spec: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	rec := f.putTarget(`{"type":"directory","config":{"path":"/data/odb-out"}}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, problem.CodeTargetKindInUse, code(t, rec))
	require.Contains(t, rec.Body.String(), "shop.orders → shop.sales.orders", "the detail lists the mappings")
	// Re-saving the same kind is unaffected.
	require.Equal(t, http.StatusOK, f.putTarget(couchbaseBody).Code)

	// With no mapping holding a location, the kind changes, and a directory needs no password.
	require.NoError(t, f.st.Mappings().Delete(ctx, ws, m.ID))
	rec = f.putTarget(`{"type":"directory","config":{"path":"/data/odb-out"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	conn, err := f.st.Connections().Get(ctx, ws, store.ConnectionID(f.project, store.RoleTarget))
	require.NoError(t, err)
	require.Equal(t, store.KindDirectory, conn.Type)
	require.Empty(t, conn.Secret.Ciphertext, "a directory connection stores no secret")
	require.False(t, conn.SecretMeta.PasswordSet)

	// An invalid directory config is a validation error.
	rec = f.putTarget(`{"type":"directory","config":{"path":"relative/dir"}}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, problem.CodeValidation, code(t, rec))
}

// OC-038 AC4: a saved directory connection stores no secret and returns no secrets block.
func TestPutTarget_AC4_DirectoryNoSecret(t *testing.T) {
	f := newKindsFixture(t, []string{"/data/odb-out"})
	rec := f.putTarget(`{"type":"directory","config":{"path":"/data/odb-out/shop"},"secrets":{"password":"ignored"}}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotContains(t, body, "secrets")
	require.NotContains(t, rec.Body.String(), "passwordSet")
	require.Equal(t, "/data/odb-out/shop", body["name"], "named by its path")
	require.Equal(t, "flat", body["config"].(map[string]any)["layout"], "defaults applied")

	conn, err := f.st.Connections().Get(ctx, ws, store.ConnectionID(f.project, store.RoleTarget))
	require.NoError(t, err)
	require.Empty(t, conn.Secret.Ciphertext)
	raw, _ := json.Marshal(conn.Secret)
	require.NotContains(t, string(raw), "ignored")

	// Outside the allowed roots: refused.
	rec = f.putTarget(`{"type":"directory","config":{"path":"/etc"}}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, problem.CodeDirectoryOutsideRoots, code(t, rec))
}

func TestFolderPicker_OC038(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "exports"), 0o750))
	f := newKindsFixture(t, []string{root})
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/target-kinds/directory/folders?path="+url.QueryEscape(path), nil))
		return rec
	}
	rec := get("")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), root)
	rec = get(root)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"name":"exports"`)
	require.Equal(t, http.StatusUnprocessableEntity, get("/etc").Code)

	post := httptest.NewRecorder()
	f.h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/api/v1/target-kinds/directory/folders", strings.NewReader(`{"parent":"`+root+`","name":"odb-out"}`)))
	require.Equal(t, http.StatusCreated, post.Code, post.Body.String())
	require.DirExists(t, filepath.Join(root, "odb-out"))
	evs, err := f.st.Audit().List(ctx, ws, 10)
	require.NoError(t, err)
	require.Equal(t, "folder.create", evs[0].Action)

	// Disabled (a server with no folder to write to): refused.
	g := newKindsFixture(t, []string{})
	rec = httptest.NewRecorder()
	g.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/target-kinds/directory/folders", nil))
	require.Equal(t, problem.CodeTargetKindDisabled, code(t, rec))
	post = httptest.NewRecorder()
	g.h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/api/v1/target-kinds/directory/folders", strings.NewReader(`{"parent":"/x","name":"y"}`)))
	require.Equal(t, problem.CodeTargetKindDisabled, code(t, post))
}

const directoryBody = `{"type":"directory","config":{"path":"/data/odb-out"}}`

func (f *kindsFixture) mapping(t *testing.T, table string, l store.Location) store.Mapping {
	t.Helper()
	m := store.Mapping{ProjectID: f.project, SourceSchema: "shop", SourceTable: table, Spec: json.RawMessage(`{}`)}
	m.SetLocation(l)
	m, err := f.st.Mappings().Create(ctx, ws, m)
	require.NoError(t, err)
	return m
}

func (f *kindsFixture) where(t *testing.T, id string) store.Location {
	t.Helper()
	m, err := f.st.Mappings().Get(ctx, ws, id)
	require.NoError(t, err)
	return m.Where()
}

func TestSave_RelocateToCouchbase_OC068_AC1(t *testing.T) {
	f := newKindsFixture(t, []string{"/data/odb-out"})
	require.Equal(t, http.StatusCreated, f.putTarget(directoryBody).Code)
	od := f.mapping(t, "Order Details", store.Location{Kind: store.KindDirectory, Folder: "order_details", CreateIfMissing: true})
	cu := f.mapping(t, "customer", store.Location{Kind: store.KindDirectory, Folder: "archive/Customer.v2/"})

	rec := f.putTarget(`{"type":"couchbase","config":{},"secrets":{"password":"pw"},"relocate":{"bucket":"shop","scope":"sales"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	conn, err := f.st.Connections().Get(ctx, ws, store.ConnectionID(f.project, store.RoleTarget))
	require.NoError(t, err)
	require.Equal(t, store.KindCouchbase, conn.Type)
	require.Equal(t, store.Location{Kind: store.KindCouchbase, Bucket: "shop", Scope: "sales", Collection: "order_details", CreateIfMissing: true}, f.where(t, od.ID))
	require.Equal(t, store.Location{Kind: store.KindCouchbase, Bucket: "shop", Scope: "sales", Collection: "customer_v_2", CreateIfMissing: true}, f.where(t, cu.ID),
		"the folder's last segment, made a valid collection name")

	events, err := f.st.Audit().List(ctx, ws, 50)
	require.NoError(t, err)
	var moved []string
	for _, e := range events {
		if e.Action == "mapping.relocate" {
			moved = append(moved, string(e.Metadata))
		}
	}
	require.Len(t, moved, 2)
	require.Contains(t, moved[0], `"to":"shop.sales.`)
}

func TestSave_RelocateToDirectory_OC068_AC2(t *testing.T) {
	f := newKindsFixture(t, []string{"/data/odb-out"})
	require.Equal(t, http.StatusCreated, f.putTarget(couchbaseBody).Code)
	m := f.mapping(t, "Order Details", store.Location{Kind: store.KindCouchbase, Bucket: "shop", Scope: "sales", Collection: "order_details"})

	rec := f.putTarget(`{"type":"directory","config":{"path":"/data/odb-out"},"relocate":{}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, store.Location{Kind: store.KindDirectory, Folder: "order_details", CreateIfMissing: true}, f.where(t, m.ID))
	got, err := f.st.Mappings().Get(ctx, ws, m.ID)
	require.NoError(t, err)
	require.Equal(t, store.MappingTarget{}, got.Target, "a directory location has no 1.0 target")
}

func TestSave_RelocateGuard_OC068_AC3(t *testing.T) {
	f := newKindsFixture(t, []string{"/data/odb-out"})
	require.Equal(t, http.StatusCreated, f.putTarget(directoryBody).Code)
	dir := store.Location{Kind: store.KindDirectory, Folder: "order_details"}
	m := f.mapping(t, "Order Details", dir)

	rec := f.putTarget(couchbaseBody)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, problem.CodeTargetKindInUse, code(t, rec))
	require.Contains(t, rec.Body.String(), "Move them to the new target")

	rec = f.putTarget(`{"type":"couchbase","config":{},"secrets":{"password":"pw"},"relocate":{"bucket":"shop"}}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, problem.CodeLocationInvalid, code(t, rec))

	conn, err := f.st.Connections().Get(ctx, ws, store.ConnectionID(f.project, store.RoleTarget))
	require.NoError(t, err)
	require.Equal(t, store.KindDirectory, conn.Type, "nothing changed")
	require.Equal(t, dir, f.where(t, m.ID))
}
