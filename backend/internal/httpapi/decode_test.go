package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

type body struct {
	Name  string `json:"name" validate:"required"`
	Count int    `json:"count" validate:"min=1"`
}

func decode(t *testing.T, raw string) error {
	t.Helper()
	var b body
	return DecodeJSON(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(raw)), &b)
}

func TestDecodeJSON(t *testing.T) {
	require.NoError(t, decode(t, `{"name":"x","count":2}`))
	require.Equal(t, http.StatusBadRequest, problem.From(decode(t, ``)).Status, "empty body")
	require.Equal(t, http.StatusBadRequest, problem.From(decode(t, `{"name":"x","count":2,"extra":1}`)).Status, "unknown field")
	require.Equal(t, http.StatusBadRequest, problem.From(decode(t, `{`)).Status, "malformed")
	p := problem.From(decode(t, `{"name":"","count":0}`))
	require.Equal(t, http.StatusUnprocessableEntity, p.Status)
	require.Len(t, p.FieldErrors, 2)
	big := `{"name":"` + strings.Repeat("x", 2<<20) + `","count":1}`
	p = problem.From(decode(t, big))
	require.Equal(t, http.StatusRequestEntityTooLarge, p.Status, "bodies over 1 MiB are rejected")
	require.Equal(t, problem.CodePayloadTooLarge, p.Code)
}

func TestURLParam_DecodesPercentEncoding(t *testing.T) {
	r := chi.NewRouter()
	var got string
	r.Get("/c/{id}", func(_ http.ResponseWriter, req *http.Request) { got = URLParam(req, "id") })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/c/p1%3A%3Asource", nil))
	require.Equal(t, "p1::source", got)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/c/plain", nil))
	require.Equal(t, "plain", got)
}
