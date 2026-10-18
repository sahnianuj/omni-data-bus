package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// OC-189 AC5: /licenses.txt serves the licence, NOTICE and the third-party notices, signed in or not.
func TestLicenses_OC189_AC5(t *testing.T) {
	h := NewRouter(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/licenses.txt", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	for _, s := range []string{"\nLICENSE\n", "Apache License", "\nNOTICE\n", "Copyright 2026 Couchbase, Inc.", "Author: Anuj Sahni", "\nTHIRD_PARTY_NOTICES.md\n", "| github.com/couchbase/gocb/v2 |"} {
		require.Contains(t, body, s)
	}
}
