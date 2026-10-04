package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/store"
)

func TestLimitRoutes(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/v1/connections/c1/tables/public/customer/preview": true,
		"/api/v1/projects/p1/mappings/m1/preview":               true,
		"/api/v1/projects/p1/mappings/m1":                       false,
	} {
		require.Equal(t, want, previewRoute.MatchString(path), path)
	}
	for path, want := range map[string]bool{"/api/v1/connections/test": true, "/api/v1/connections/c1/test": false, "/api/v1/connections/c1": false} {
		require.Equal(t, want, connTestRoute.MatchString(path), path)
	}
	for path, want := range map[string]bool{"/api/v1/events": true, "/api/v1/jobs/j1/events": true, "/api/v1/jobs/j1/events/log": false} {
		require.Equal(t, want, streamRoute.MatchString(path), path)
	}
}

func TestLimitMiddleware_ConnectionTestsPerSession(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	chain := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	mws := limitMiddleware(func() time.Time { return now })
	for i := len(mws) - 1; i >= 0; i-- {
		chain = mws[i](chain)
	}
	call := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{SessionID: "s1", WorkspaceID: store.WorkspaceID("w1")}))
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		return rec
	}
	for range 3 {
		require.Equal(t, http.StatusNoContent, call("/api/v1/connections/test").Code)
	}
	require.Equal(t, http.StatusNoContent, call("/api/v1/connections/c1/test").Code, "stored tests aren't windowed")
	rec := call("/api/v1/connections/test")
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "a 4th test within 10 s")
	require.Equal(t, "10", rec.Header().Get("Retry-After"))
	require.Equal(t, http.StatusNoContent, call("/api/v1/projects").Code, "other routes aren't affected")

	key, authed := rateKey(httptest.NewRequest(http.MethodGet, "/", nil))
	require.False(t, authed)
	require.Equal(t, "ip:192.0.2.1", key)
}

func TestMemoryLimit_OC056(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	limit, ok := memoryLimit(640, getenv)
	require.True(t, ok)
	require.Equal(t, int64(640)<<20, limit)
	_, ok = memoryLimit(0, getenv)
	require.False(t, ok, "0 turns it off")
	env["GOMEMLIMIT"] = "2GiB"
	_, ok = memoryLimit(640, getenv)
	require.False(t, ok, "the runtime's own setting wins")
}
