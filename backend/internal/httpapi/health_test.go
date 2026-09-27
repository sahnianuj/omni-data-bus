package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func readyBodyOf(t *testing.T, rec *httptest.ResponseRecorder) readyBody {
	t.Helper()
	var b readyBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &b))
	return b
}

func TestReadyz_AC4_OK(t *testing.T) {
	h := Readyz(map[string]Pinger{"metadata": PingFunc(func(context.Context) error { return nil })})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, readyBody{Status: "ok", Checks: map[string]string{"metadata": "ok"}}, readyBodyOf(t, rec))
}

func TestReadyz_AC4_MetadataDown(t *testing.T) {
	h := Readyz(map[string]Pinger{"metadata": PingFunc(func(context.Context) error { return errors.New("dial tcp: refused") })})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	b := readyBodyOf(t, rec)
	require.Equal(t, "unavailable", b.Checks["metadata"])
	require.NotContains(t, rec.Body.String(), "dial tcp", "raw error is not exposed")
}

func TestReadyz_AC4_Timeout(t *testing.T) {
	h := Readyz(map[string]Pinger{"metadata": PingFunc(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "timeout", readyBodyOf(t, rec).Checks["metadata"])
}

func TestHealthz_AC4_AlwaysOK(t *testing.T) {
	rec := httptest.NewRecorder()
	Healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}
