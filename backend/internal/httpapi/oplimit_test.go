package httpapi

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

func TestOpLimiter_AC5_ConcurrentDiscoveries(t *testing.T) {
	o := NewOpLimiter()
	r1, ok := o.Acquire("discovery:ws1", 2)
	require.True(t, ok)
	r2, ok := o.Acquire("discovery:ws1", 2)
	require.True(t, ok)
	_, ok = o.Acquire("discovery:ws1", 2)
	require.False(t, ok, "a 3rd concurrent discovery in the workspace")
	_, ok = o.Acquire("discovery:ws2", 2)
	require.True(t, ok, "other workspaces have their own slots")
	r1()
	r1() // releasing twice is harmless
	r3, ok := o.Acquire("discovery:ws1", 2)
	require.True(t, ok)
	r2()
	r3()
	require.Empty(t, o.inflight["discovery:ws1"])

	p := ErrTooManyConcurrent("discoveries", 2)
	require.Equal(t, http.StatusTooManyRequests, p.Status)
	require.Equal(t, problem.CodeTooManyConcurrentOps, p.Code)
}

func TestOpLimiter_MiddlewareHoldsASlotPerRequest(t *testing.T) {
	o := NewOpLimiter()
	started, release := make(chan struct{}, 4), make(chan struct{})
	h := o.Middleware("previews", 4, func(r *http.Request) string {
		if r.URL.Path == "/other" {
			return "" // routes without a key aren't limited
		}
		return "ws1"
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/other" {
			started <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		}()
	}
	for range 4 {
		<-started
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "a 5th concurrent preview")
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
	require.Equal(t, http.StatusNoContent, rec.Code)
	close(release)
	wg.Wait()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusNoContent, rec.Code, "slots are released")
}
