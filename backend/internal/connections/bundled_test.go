package connections

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

// bundledFixture serves the connection routes, with or without a bundled Couchbase node.
func bundledFixture(t *testing.T, b Bundled) (http.Handler, *memstore.Store, *Service, string) {
	t.Helper()
	st := memstore.New(storetest.NewClock())
	targets := target.NewRegistry()
	targets.Register(&fakeTarget{typ: store.KindCouchbase})
	svc := NewService(st, sealer(t), source.NewRegistry(), targets, nil, nil).WithBundled(b)
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
	return h, st, svc, p.ID
}

var appliance = Bundled{ConnStr: "couchbase://127.0.0.1", Username: "Administrator", Password: "s3cret-from-file", Buckets: []string{"sandbox"}}

// OC-072 AC6: /target-kinds offers the bundled node only when the server has one.
func TestTargetKinds_OC072_AC6_BundledOnlyWhenConfigured(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    Bundled
		want bool
	}{{"appliance", appliance, true}, {"regular image", Bundled{}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, _ := bundledFixture(t, tc.b)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/target-kinds", nil))
			require.Equal(t, http.StatusOK, rec.Code)
			var body struct {
				Bundled *BundledInfo `json:"bundled"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			if !tc.want {
				require.Nil(t, body.Bundled)
				return
			}
			require.Equal(t, &BundledInfo{Kind: "couchbase", Label: "Bundled Couchbase (this container)", Buckets: []string{"sandbox"}}, body.Bundled)
			require.NotContains(t, rec.Body.String(), "s3cret", "no credentials in the listing")
		})
	}
}

// OC-072 AC6: one call saves and tests the bundled node; the password comes from the server.
func TestSaveBundled_OC072_AC6_SavesAndTests(t *testing.T) {
	h, st, svc, project := bundledFixture(t, appliance)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project+"/connections/target/bundled", nil))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "s3cret", "the password never leaves the server")
	var body struct {
		Connection struct {
			Name           string          `json:"name"`
			Type           string          `json:"type"`
			Config         json.RawMessage `json:"config"`
			LastTestStatus string          `json:"lastTestStatus"`
		} `json:"connection"`
		Test struct {
			OK bool `json:"ok"`
		} `json:"test"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "Bundled Couchbase", body.Connection.Name)
	require.Equal(t, "couchbase", body.Connection.Type)
	require.JSONEq(t, `{"connectionString":"couchbase://127.0.0.1","username":"Administrator","deployment":"self-managed"}`, string(body.Connection.Config))
	require.True(t, body.Test.OK)
	require.Equal(t, "ok", body.Connection.LastTestStatus)

	// Stored sealed, like any credential, and opened with the server's password.
	conn, err := st.Connections().Get(ctx, ws, store.ConnectionID(project, store.RoleTarget))
	require.NoError(t, err)
	sec, err := Open(svc.sealer, conn)
	require.NoError(t, err)
	require.Equal(t, "s3cret-from-file", sec.Password)

	// Again: updates the same connection.
	again := httptest.NewRecorder()
	h.ServeHTTP(again, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project+"/connections/target/bundled", nil))
	require.Equal(t, http.StatusOK, again.Code)
}

func TestSaveBundled_OC072_AC6_NotOnRegularImage(t *testing.T) {
	h, _, _, project := bundledFixture(t, Bundled{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project+"/connections/target/bundled", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}
