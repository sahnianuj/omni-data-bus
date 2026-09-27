package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func limitedRouter(mw ...func(http.Handler) http.Handler) http.Handler {
	type body struct {
		Name string `json:"name"`
	}
	echo := func(w http.ResponseWriter, r *http.Request) {
		var b body
		if err := DecodeJSON(w, r, &b); err != nil {
			problem.Write(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
	return NewRouter(Options{
		Logger:     slog.New(slog.DiscardHandler),
		Middleware: append([]func(http.Handler) http.Handler{BodyLimit}, mw...),
		Features: []Registrar{RegistrarFunc(func(r chi.Router) {
			r.Post("/things", echo)
			r.Post("/doc-schemas/infer", echo)
			r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		})},
	})
}

func post(h http.Handler, path string, size int, declare bool) *httptest.ResponseRecorder {
	payload := `{"name":"` + strings.Repeat("x", size) + `"}`
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(payload))
	if !declare {
		req.ContentLength = -1 // chunked: the limit is enforced while reading
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBodyLimit_AC5_413(t *testing.T) {
	h := limitedRouter()
	for _, declare := range []bool{true, false} {
		rec := post(h, "/api/v1/things", 2<<20, declare)
		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "declared length: %v", declare)
		require.Contains(t, rec.Body.String(), problem.CodePayloadTooLarge)
	}
	require.Equal(t, http.StatusNoContent, post(h, "/api/v1/things", 900<<10, true).Code, "under 1 MB is fine")
	// JSON samples may be up to 6 MB.
	require.Equal(t, http.StatusNoContent, post(h, "/api/v1/doc-schemas/infer", 2<<20, true).Code)
	require.Equal(t, http.StatusRequestEntityTooLarge, post(h, "/api/v1/doc-schemas/infer", 7<<20, false).Code)
}

func TestLargeBody_Routes(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/v1/doc-schemas/infer": true, "/api/v1/doc-schemas/export": true,
		"/api/v1/projects/p1/connections/source": true, "/api/v1/connections/test": true,
		"/api/v1/projects/p1": false, "/api/v1/projects/p1/mappings/m1": false,
	} {
		require.Equal(t, want, LargeBody(httptest.NewRequest(http.MethodPut, path, nil)) || LargeBody(httptest.NewRequest(http.MethodPost, path, nil)), path)
	}
	require.False(t, LargeBody(httptest.NewRequest(http.MethodGet, "/api/v1/doc-schemas/infer", nil)))
}

func TestRateLimit_AC5_SessionBurst(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	l := NewRateLimiter(clock.now)
	session := func(r *http.Request) (string, bool) {
		if s := r.Header.Get("X-Test-Session"); s != "" {
			return "s:" + s, true
		}
		return "ip:" + ClientIP(r), false
	}
	h := limitedRouter(l.Middleware(session))
	get := func(sess string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
		if sess != "" {
			req.Header.Set("X-Test-Session", sess)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for i := range 40 {
		require.Equal(t, http.StatusNoContent, get("a").Code, "request %d", i+1)
	}
	rec := get("a")
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "the 41st request")
	require.Equal(t, "1", rec.Header().Get("Retry-After"))
	require.Contains(t, rec.Body.String(), problem.CodeRateLimited)
	require.Equal(t, http.StatusNoContent, get("b").Code, "other sessions are unaffected")

	clock.add(100 * time.Millisecond) // 20/s refills 2 tokens
	require.Equal(t, http.StatusNoContent, get("a").Code)
	require.Equal(t, http.StatusNoContent, get("a").Code)
	require.Equal(t, http.StatusTooManyRequests, get("a").Code)

	// Unauthenticated callers: 5/s with a burst of 10, per IP.
	for range 10 {
		require.Equal(t, http.StatusNoContent, get("").Code)
	}
	require.Equal(t, http.StatusTooManyRequests, get("").Code)
}

func TestWindowLimiter_ConnectionTests(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	l := NewWindowLimiter(clock.now)
	for range 3 {
		ok, _ := l.Allow("s1", 3, 10*time.Second)
		require.True(t, ok)
	}
	ok, wait := l.Allow("s1", 3, 10*time.Second)
	require.False(t, ok, "a 4th test within 10 s")
	require.Equal(t, 10*time.Second, wait)
	ok, _ = l.Allow("s2", 3, 10*time.Second)
	require.True(t, ok)
	clock.add(10 * time.Second)
	ok, _ = l.Allow("s1", 3, 10*time.Second)
	require.True(t, ok, "the window slid")
}
