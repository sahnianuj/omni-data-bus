package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnidatabus/internal/observability"
	"omnidatabus/internal/problem"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newTestRouter(buf *bytes.Buffer, register func(r chi.Router)) http.Handler {
	logger := observability.NewLogger(buf, "json", "info")
	return NewRouter(Options{Logger: logger, Features: []Registrar{RegistrarFunc(register)}})
}

func TestAccessLog_AC2_SingleLineWithFields(t *testing.T) {
	var buf bytes.Buffer
	h := newTestRouter(&buf, func(r chi.Router) {
		r.Get("/connections/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/connections/abc123", nil))
	require.Equal(t, http.StatusNoContent, rec.Code)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 1, "exactly one access log line")
	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry))
	require.Equal(t, "/api/v1/connections/{id}", entry["route"], "route pattern, not the raw path")
	require.Equal(t, "GET", entry["method"])
	require.EqualValues(t, 204, entry["status"])
	require.NotEmpty(t, entry["request_id"])
	require.Contains(t, entry, "duration_ms")
}

func TestRecoverer_AC3_PanicReturnsProblem(t *testing.T) {
	var buf bytes.Buffer
	h := newTestRouter(&buf, func(r chi.Router) {
		r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("secret internals") })
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, problem.ContentType, rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), problem.CodeInternal)
	require.NotContains(t, rec.Body.String(), "secret internals")
	require.NotContains(t, rec.Body.String(), "goroutine", "no stack trace in the body")
	require.Contains(t, buf.String(), "stack", "stack trace is logged")
}

func TestRequestID_AC5_EchoesIncoming(t *testing.T) {
	var buf bytes.Buffer
	h := newTestRouter(&buf, func(chi.Router) {})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, "abc", rec.Header().Get("X-Request-ID"))
	require.Contains(t, buf.String(), `"request_id":"abc"`)
}

func TestRequestID_AC5_GeneratesWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	h := newTestRouter(&buf, func(chi.Router) {})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	id, err := uuid.Parse(rec.Header().Get("X-Request-ID"))
	require.NoError(t, err)
	require.Equal(t, uuid.Version(7), id.Version())
}

func TestRouter_AC3_UnknownAPIRouteIsProblem404(t *testing.T) {
	var buf bytes.Buffer
	h := newTestRouter(&buf, func(chi.Router) {})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, problem.ContentType, rec.Header().Get("Content-Type"))
}

func TestSecurityHeaders_AC2_Baseline(t *testing.T) {
	var buf bytes.Buffer
	rec := httptest.NewRecorder()
	newTestRouter(&buf, func(chi.Router) {}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

func TestLoggerFrom_DefaultsWhenMissing(t *testing.T) {
	require.Equal(t, slog.Default(), LoggerFrom(httptest.NewRequest(http.MethodGet, "/", nil).Context()))
}
