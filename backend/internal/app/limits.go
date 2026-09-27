package app

import (
	"math"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
)

// Per-session and per-workspace limits on expensive routes (OC-033). Discoveries are limited
// inside the discovery service, because they keep running after the request returns.
const (
	MaxPreviewsPerWorkspace = 4
	MaxStreamsPerSession    = 10
	MaxConnTestsPerSession  = 3
	ConnTestWindow          = 10 * time.Second
)

var (
	previewRoute = regexp.MustCompile(`^/api/v1/(connections/[^/]+/tables/[^/]+/[^/]+|projects/[^/]+/mappings/[^/]+)/preview$`)
	// Only tests of unsaved settings are windowed: they can point at any host (the probing
	// primitive). A stored connection's test reaches an already-saved destination, and the UI
	// re-tests right after every save.
	connTestRoute = regexp.MustCompile(`^/api/v1/connections/test$`)
	streamRoute   = regexp.MustCompile(`^/api/v1/(jobs/[^/]+/)?events$`)
)

// rateKey is the session for signed-in callers and the client IP otherwise.
func rateKey(r *http.Request) (string, bool) {
	if p, ok := auth.PrincipalFrom(r.Context()); ok {
		return "session:" + p.SessionID, true
	}
	return "ip:" + httpapi.ClientIP(r), false
}

// limitMiddleware returns the rate limit and the route-specific limits, in order.
func limitMiddleware(now func() time.Time) []func(http.Handler) http.Handler {
	rate := httpapi.NewRateLimiter(now)
	ops := httpapi.NewOpLimiter()
	tests := httpapi.NewWindowLimiter(now)
	previews := ops.Middleware("previews", MaxPreviewsPerWorkspace, func(r *http.Request) string {
		p, ok := auth.PrincipalFrom(r.Context())
		if !ok || !previewRoute.MatchString(r.URL.Path) {
			return ""
		}
		return "preview:" + string(p.WorkspaceID)
	})
	streams := ops.Middleware("live event streams", MaxStreamsPerSession, func(r *http.Request) string {
		p, ok := auth.PrincipalFrom(r.Context())
		if !ok || r.Method != http.MethodGet || !streamRoute.MatchString(r.URL.Path) {
			return ""
		}
		return "sse:" + p.SessionID
	})
	connTests := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p, ok := auth.PrincipalFrom(r.Context()); ok && r.Method == http.MethodPost && connTestRoute.MatchString(r.URL.Path) {
				if ok, wait := tests.Allow("test:"+p.SessionID, MaxConnTestsPerSession, ConnTestWindow); !ok {
					w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
					problem.Write(w, r, problem.New(http.StatusTooManyRequests, problem.CodeRateLimited, "Too many connection tests",
						"At most 3 connection tests every 10 seconds. Try again shortly."))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
	return []func(http.Handler) http.Handler{rate.Middleware(rateKey), previews, streams, connTests}
}
