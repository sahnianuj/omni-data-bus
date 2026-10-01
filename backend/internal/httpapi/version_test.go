package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"

	"github.com/stretchr/testify/require"
)

var testBuild = buildinfo.Info{
	Version: "2.0.0", Commit: "3f2a9c1", BuildTime: "2026-11-02T14:05:11Z", Channel: "release",
	GoVersion: "go1.26.8", Platform: "linux/amd64",
}

func signedIn(*http.Request) bool  { return true }
func anonymous(*http.Request) bool { return false }

func getVersion(t *testing.T, h http.Handler) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var v map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	return v
}

func TestVersionAPI_AC2_AuthenticatedFull(t *testing.T) {
	st := memstore.New(nil)
	ctx := context.Background()
	require.NoError(t, st.Nodes().Beat(ctx, store.NodeBeat{ID: "a", Role: store.NodeRoleAPI, Version: "2.0.0"}))
	require.NoError(t, st.Nodes().Beat(ctx, store.NodeBeat{ID: "w", Role: store.NodeRoleWorker, Version: "2.0.0"}))
	h := &VersionHandler{Info: testBuild, Expose: true, Authenticated: signedIn, Nodes: st.Nodes(), Schema: st.SchemaVersion}
	v := getVersion(t, h)
	require.Equal(t, "2.0.0", v["version"])
	require.Equal(t, "3f2a9c1", v["commit"])
	require.Equal(t, "2026-11-02T14:05:11Z", v["buildTime"])
	require.Equal(t, "release", v["channel"])
	require.Equal(t, "go1.26.8", v["goVersion"])
	require.Equal(t, "linux/amd64", v["platform"])
	require.Equal(t, false, v["dirty"])
	require.EqualValues(t, 4, v["metadataSchemaVersion"], "the latest provisioning step (4: relationships, OC-042)")
	require.Equal(t, map[string]any{"api": []any{"2.0.0"}, "worker": []any{"2.0.0"}}, v["nodes"])
	require.Equal(t, true, v["consistent"])
}

func TestVersionAPI_AC2_AnonymousReduced(t *testing.T) {
	h := &VersionHandler{Info: testBuild, Expose: true, Authenticated: anonymous, Nodes: memstore.New(nil).Nodes()}
	require.Equal(t, map[string]any{"version": "2.0.0", "channel": "release"}, getVersion(t, h))

	// OC_EXPOSE_BUILD_INFO=false hides even the version from anonymous callers.
	h = &VersionHandler{Info: testBuild, Expose: false, Authenticated: anonymous}
	require.Equal(t, map[string]any{}, getVersion(t, h))
	// No authenticator wired (bare router) is anonymous.
	require.Equal(t, map[string]any{}, getVersion(t, &VersionHandler{Info: testBuild}))
}

func TestVersionAPI_AC4_MixedVersions(t *testing.T) {
	st := memstore.New(nil)
	ctx := context.Background()
	require.NoError(t, st.Nodes().Beat(ctx, store.NodeBeat{ID: "a", Role: store.NodeRoleAPI, Version: "2.0.0"}))
	require.NoError(t, st.Nodes().Beat(ctx, store.NodeBeat{ID: "w", Role: store.NodeRoleWorker, Version: "1.0.1"}))
	v := getVersion(t, &VersionHandler{Info: testBuild, Authenticated: signedIn, Nodes: st.Nodes()})
	require.Equal(t, false, v["consistent"])
	require.Equal(t, map[string]any{"api": []any{"2.0.0"}, "worker": []any{"1.0.1"}}, v["nodes"])
}

type failingNodes struct{ calls int }

func (f *failingNodes) Beat(context.Context, store.NodeBeat) error { return nil }
func (f *failingNodes) Live(context.Context) ([]store.NodeBeat, error) {
	f.calls++
	return nil, errors.New("metadata store unavailable")
}

func TestVersionAPI_CachedAndDegradesToSelf(t *testing.T) {
	now := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)
	nodes := &failingNodes{}
	h := &VersionHandler{
		Info: testBuild, Authenticated: signedIn, Nodes: nodes, Role: store.NodeRoleWorker, Now: func() time.Time { return now },
		Schema: func(context.Context) (int, error) { return 0, errors.New("down") },
	}
	v := getVersion(t, h)
	// The store failed, so the response still renders and reports this node alone.
	require.Equal(t, map[string]any{"api": []any{}, "worker": []any{"2.0.0"}}, v["nodes"])
	require.Equal(t, true, v["consistent"])
	require.EqualValues(t, 0, v["metadataSchemaVersion"])

	getVersion(t, h)
	require.Equal(t, 1, nodes.calls, "cached for 10 s")
	now = now.Add(VersionCacheTTL)
	getVersion(t, h)
	require.Equal(t, 2, nodes.calls, "refreshed after the TTL")
}

func TestRouter_VersionRunsAfterMiddleware(t *testing.T) {
	var sawMiddleware bool
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sawMiddleware = true; next.ServeHTTP(w, r) })
	}
	r := NewRouter(Options{Logger: slog.New(slog.DiscardHandler), Middleware: []func(http.Handler) http.Handler{mw}, Version: &VersionHandler{Info: testBuild, Expose: true}})
	v := getVersion(t, r)
	require.True(t, sawMiddleware, "the session middleware must run before /version")
	require.Equal(t, "2.0.0", v["version"])
	// Without an explicit handler the router serves the anonymous view of this binary.
	require.Equal(t, "dev", getVersion(t, NewRouter(Options{Logger: slog.New(slog.DiscardHandler)}))["version"])
}
