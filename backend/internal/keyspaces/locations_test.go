package keyspaces

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

// folderKind is a browsable target kind with folders, like a directory.
type folderKind struct {
	folders map[string]bool
	gotConn target.Conn
}

func (k *folderKind) Descriptor() connectors.Descriptor {
	return connectors.Descriptor{Type: store.KindDirectory, Role: connectors.RoleTarget, DisplayName: "Folders"}
}

func (k *folderKind) Capabilities() target.Capabilities {
	return target.Capabilities{SupportsCreateLocation: true, LocationLevels: []string{"folder"}}
}
func (k *folderKind) Validate(json.RawMessage) error { return nil }
func (k *folderKind) Test(context.Context, json.RawMessage, connectors.Credentials) connectors.TestResult {
	return connectors.TestResult{OK: true}
}
func (k *folderKind) Close(context.Context) error { return nil }

func (k *folderKind) ListLocations(_ context.Context, c target.Conn, parent target.Location) ([]target.LocationNode, error) {
	k.gotConn = c
	out := []target.LocationNode{}
	for f := range k.folders {
		if parent.Folder == "" && !strings.Contains(f, "/") || strings.HasPrefix(f, parent.Folder+"/") {
			out = append(out, target.LocationNode{Name: f, Level: "folder", Path: f, Leaf: true, Location: target.Location{Kind: store.KindDirectory, Folder: f}})
		}
	}
	return out, nil
}

func (k *folderKind) StatLocation(context.Context, target.Conn, target.Location) (target.LocationNode, error) {
	return target.LocationNode{}, nil
}

func (k *folderKind) CreateLocation(_ context.Context, _ target.Conn, loc target.Location) error {
	if strings.Contains(loc.Folder, "..") {
		return problem.New(http.StatusUnprocessableEntity, problem.CodeLocationInvalid, "Invalid location", "")
	}
	if loc.Folder == "slow" {
		return target.ErrPending
	}
	k.folders[loc.Folder] = true
	return nil
}

func locationsRouter(t *testing.T, role store.MemberRole) (http.Handler, *folderKind, *memstore.Store) {
	t.Helper()
	st := memstore.New(storetest.NewClock())
	kind := &folderKind{folders: map[string]bool{"orders": true, "orders/2026": true}}
	targets := target.NewRegistry()
	targets.Register(kind)
	kr, err := crypto.ParseKeyring(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "", "")
	require.NoError(t, err)
	svc := connections.NewService(st, crypto.NewLocalSealer(kr), source.NewRegistry(), targets, nil, nil)
	p, err := st.Projects().Create(context.Background(), ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	_, err = st.Connections().Upsert(context.Background(), ws, p.ID, store.RoleTarget, store.ConnectionInput{Type: store.KindDirectory, Config: json.RawMessage(`{"path":"/data"}`)})
	require.NoError(t, err)
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u1", WorkspaceID: ws, Role: role})))
		})
	}
	h := httpapi.NewRouter(httpapi.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Features:   []httpapi.Registrar{LocationHandlers{Svc: svc, Audit: st.Audit()}},
		Middleware: []func(http.Handler) http.Handler{withPrincipal},
	})
	return h, kind, st
}

func TestLocationHandlers_ListAndCreate(t *testing.T) {
	h, kind, st := locationsRouter(t, store.RoleEditor)
	id := "/api/v1/connections/" + strings.ReplaceAll(firstTarget(t, st), ":", "%3A")

	rec := call(h, "GET", id+"/locations", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var list LocationList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Equal(t, "directory", list.Kind)
	require.Len(t, list.Nodes, 1)
	require.Equal(t, "orders", list.Nodes[0].Name)
	require.NotEmpty(t, kind.gotConn.Version, "the stored connection's version keys pooled sessions")

	rec = call(h, "GET", id+"/locations?parent=orders", "")
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Equal(t, "orders/2026", list.Nodes[0].Path)

	rec = call(h, "POST", id+"/locations", `{"folder":"customers"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"status":"ready","location":{"kind":"directory","folder":"customers"}}`, rec.Body.String())
	require.True(t, kind.folders["customers"])
	require.Equal(t, http.StatusAccepted, call(h, "POST", id+"/locations", `{"folder":"slow"}`).Code)
	require.Equal(t, http.StatusUnprocessableEntity, call(h, "POST", id+"/locations", `{"folder":"../x"}`).Code)

	events, err := st.Audit().List(context.Background(), ws, 10)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	require.Equal(t, "location.create", events[0].Action)
}

func TestLocationHandlers_ViewerCantCreate(t *testing.T) {
	h, _, st := locationsRouter(t, store.RoleViewer)
	id := "/api/v1/connections/" + firstTarget(t, st)
	require.Equal(t, http.StatusOK, call(h, "GET", id+"/locations", "").Code)
	require.Equal(t, http.StatusForbidden, call(h, "POST", id+"/locations", `{"folder":"x"}`).Code)
}

func firstTarget(t *testing.T, st *memstore.Store) string {
	t.Helper()
	ps, _, err := st.Projects().List(context.Background(), ws, store.Page{})
	require.NoError(t, err)
	return store.ConnectionID(ps[0].ID, store.RoleTarget)
}
