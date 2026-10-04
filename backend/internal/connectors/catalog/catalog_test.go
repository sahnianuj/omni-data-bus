package catalog

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/httpapi"
)

type stub struct{ d connectors.Descriptor }

func (s stub) Descriptor() connectors.Descriptor { return s.d }

func TestConnectorsHandler_AC2_ListsDescriptors(t *testing.T) {
	src := connectors.NewRegistry[stub](connectors.RoleSource)
	src.Register(stub{connectors.Descriptor{Type: "postgres", Role: connectors.RoleSource, DisplayName: "PostgreSQL"}})
	src.Register(stub{connectors.Descriptor{Type: "mysql", Role: connectors.RoleSource, DisplayName: "MySQL"}})
	tgt := connectors.NewRegistry[stub](connectors.RoleTarget)
	tgt.Register(stub{connectors.Descriptor{Type: "couchbase", Role: connectors.RoleTarget, DisplayName: "Couchbase"}})
	withUser := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u"})))
		})
	}
	h := httpapi.NewRouter(httpapi.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Middleware: []func(http.Handler) http.Handler{withUser},
		Features: []httpapi.Registrar{Handlers{Sources: src, Targets: tgt}},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/connectors", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string][]connectors.Descriptor
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, []string{"MySQL", "PostgreSQL"}, []string{body["sources"][0].DisplayName, body["sources"][1].DisplayName}, "sorted by display name")
	require.Equal(t, "couchbase", body["targets"][0].Type)
}
