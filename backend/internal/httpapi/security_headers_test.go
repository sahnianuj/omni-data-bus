package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

func headersFor(t *testing.T, hsts bool, path string) http.Header {
	t.Helper()
	spa := fstest.MapFS{"index.html": {Data: []byte(`<div id="root"></div>`)}}
	h := NewRouter(Options{
		Logger: slog.New(slog.DiscardHandler), HSTS: hsts, SPA: spa,
		Features: []Registrar{RegistrarFunc(func(r chi.Router) {
			r.Get("/boom", func(w http.ResponseWriter, r *http.Request) {
				problem.Write(w, r, problem.New(400, problem.CodeBadRequest, "x", "y"))
			})
		})},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Header()
}

func TestSecurityHeaders_AC4_Present(t *testing.T) {
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
			"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'",
		"X-Content-Type-Options":     "nosniff",
		"Referrer-Policy":            "strict-origin-when-cross-origin",
		"Permissions-Policy":         "camera=(), microphone=(), geolocation=()",
		"Cross-Origin-Opener-Policy": "same-origin",
		"X-Frame-Options":            "DENY",
	}
	// Every kind of response: health, the SPA, an API error, an unknown API route.
	for _, path := range []string{"/healthz", "/", "/projects/p1", "/api/v1/boom", "/api/v1/nope"} {
		h := headersFor(t, false, path)
		for k, v := range want {
			require.Equal(t, v, h.Get(k), "%s on %s", k, path)
		}
	}
}

func TestSecurityHeaders_AC4_HSTSOnlyHTTPS(t *testing.T) {
	require.Empty(t, headersFor(t, false, "/healthz").Get("Strict-Transport-Security"))
	require.Equal(t, "max-age=63072000; includeSubDomains", headersFor(t, true, "/healthz").Get("Strict-Transport-Security"))
	require.Equal(t, "max-age=63072000; includeSubDomains", headersFor(t, true, "/api/v1/boom").Get("Strict-Transport-Security"))
}
