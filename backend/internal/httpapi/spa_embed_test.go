//go:build embedspa

package httpapi

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// Runs in the image build and after `make build` (go test -tags embedspa ./internal/httpapi).
func TestEmbeddedSPA_AC1_IndexPresent(t *testing.T) {
	spa := EmbeddedSPA()
	require.NotNil(t, spa, "the web app is embedded")
	b, err := fs.ReadFile(spa, "index.html")
	require.NoError(t, err)
	require.Contains(t, string(b), `<div id="root">`)

	rec := httptest.NewRecorder()
	NewRouter(Options{Logger: slog.New(slog.DiscardHandler), SPA: spa}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/p1", nil))
	require.Equal(t, http.StatusOK, rec.Code, "client-side routes fall back to index.html")
	require.Contains(t, rec.Body.String(), `<div id="root">`)
}
