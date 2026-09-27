package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

var webFS = fstest.MapFS{
	"index.html":        {Data: []byte("<html>app</html>")},
	"favicon.svg":       {Data: []byte("<svg/>")},
	"assets/app-abc.js": {Data: []byte("console.log(1)")},
}

func serve(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	SPA(webFS).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestSPA_AC7_FallbackToIndex(t *testing.T) {
	rec := serve(t, "/projects/123")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "<html>app</html>", rec.Body.String())
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
}

func TestSPA_AC7_AssetCacheHeaders(t *testing.T) {
	rec := serve(t, "/assets/app-abc.js")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
	require.Empty(t, serve(t, "/favicon.svg").Header().Get("Cache-Control"), "only hashed assets are immutable")
}

func TestSPA_AC7_APIPathNotFallback(t *testing.T) {
	rec := serve(t, "/api/v1/unknown")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "problem+json")
}
