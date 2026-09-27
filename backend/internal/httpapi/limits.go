package httpapi

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"omnidatabus/internal/problem"
)

// Request limits (OC-033): body size, request rate, and concurrent expensive operations.

// DefaultBodyLimit applies to every request body; LargeBodyLimit to JSON samples, schema
// exports and connection settings (which can carry certificate bundles).
const (
	DefaultBodyLimit int64 = 1 << 20
	LargeBodyLimit   int64 = 6 << 20
)

type bodyLimitKey struct{}

var largeBodyRoutes = regexp.MustCompile(`/doc-schemas/(infer|export)$|/projects/[^/]+/connections/[^/]+$|/connections/test$`)

// LargeBody reports whether a request may carry up to LargeBodyLimit.
func LargeBody(r *http.Request) bool {
	return (r.Method == http.MethodPost || r.Method == http.MethodPut) && largeBodyRoutes.MatchString(r.URL.Path)
}

// BodyLimit caps request bodies: a declared Content-Length over the limit gets 413 at once, and
// reading past it fails (DecodeJSON turns that into 413 too).
func BodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := DefaultBodyLimit
		if LargeBody(r) {
			limit = LargeBodyLimit
		}
		if r.ContentLength > limit {
			problem.Write(w, r, tooLarge(limit))
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), bodyLimitKey{}, limit)))
	})
}

func bodyLimit(ctx context.Context) int64 {
	if l, ok := ctx.Value(bodyLimitKey{}).(int64); ok {
		return l
	}
	return DefaultBodyLimit
}

func tooLarge(limit int64) problem.Problem {
	return problem.New(http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge, "Request too large",
		"The request body is over "+strconv.FormatInt(limit>>20, 10)+" MB.")
}

// IsTooLarge reports whether err came from reading past a body limit.
func IsTooLarge(err error) (int64, bool) {
	var mb *http.MaxBytesError
	if errors.As(err, &mb) {
		return mb.Limit, true
	}
	return 0, false
}

// Rate is a token bucket: PerSecond steady, Burst at once.
type Rate struct {
	PerSecond float64
	Burst     int
}

// Default request rates: per session, and per client IP for unauthenticated routes.
var (
	SessionRate   = Rate{PerSecond: 20, Burst: 40}
	AnonymousRate = Rate{PerSecond: 5, Burst: 10}
)

type bucket struct {
	tokens float64
	last   time.Time
}

// RateLimiter keeps one token bucket per key in memory (per node).
type RateLimiter struct {
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	calls   int
}

// NewRateLimiter returns a limiter; now is time.Now in production.
func NewRateLimiter(now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{now: now, buckets: map[string]*bucket{}}
}

// Allow takes a token for key; when there is none it returns how long until there will be.
func (l *RateLimiter) Allow(key string, rate Rate) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.calls++; l.calls%1024 == 0 {
		l.sweep(now, rate)
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(rate.Burst), last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(float64(rate.Burst), b.tokens+now.Sub(b.last).Seconds()*rate.PerSecond)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / rate.PerSecond * float64(time.Second))
}

// sweep forgets buckets that have refilled (idle keys cost nothing to recreate).
func (l *RateLimiter) sweep(now time.Time, rate Rate) {
	full := time.Duration(float64(rate.Burst) / rate.PerSecond * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) > full {
			delete(l.buckets, k)
		}
	}
}

// RateKey identifies the caller: a session ID (authenticated) or the client IP.
type RateKey func(r *http.Request) (key string, authenticated bool)

// Middleware limits requests per key and answers 429 with Retry-After.
func (l *RateLimiter) Middleware(key RateKey) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k, authed := key(r)
			rate := AnonymousRate
			if authed {
				rate = SessionRate
			}
			if ok, wait := l.Allow(k, rate); !ok {
				retryAfter(w, r, wait, "Too many requests", "Slow down and try again shortly.", problem.CodeRateLimited)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func retryAfter(w http.ResponseWriter, r *http.Request, wait time.Duration, title, detail, code string) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
	problem.Write(w, r, problem.New(http.StatusTooManyRequests, code, title, detail))
}

// ClientIP is the request's remote address without the port.
func ClientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// OpLimiter bounds how many operations run at once per key (e.g. previews per workspace).
type OpLimiter struct {
	mu       sync.Mutex
	inflight map[string]int
}

// NewOpLimiter returns an empty limiter.
func NewOpLimiter() *OpLimiter { return &OpLimiter{inflight: map[string]int{}} }

// Acquire takes a slot for key if fewer than limit are taken; call release when done.
func (o *OpLimiter) Acquire(key string, limit int) (release func(), ok bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.inflight[key] >= limit {
		return nil, false
	}
	o.inflight[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			defer o.mu.Unlock()
			if o.inflight[key]--; o.inflight[key] <= 0 {
				delete(o.inflight, key)
			}
		})
	}, true
}

// ErrTooManyConcurrent is the 429 for a full OpLimiter.
func ErrTooManyConcurrent(what string, limit int) problem.Problem {
	return problem.New(http.StatusTooManyRequests, problem.CodeTooManyConcurrentOps, "Too many at once",
		"At most "+strconv.Itoa(limit)+" "+what+" can run at the same time. Try again when one finishes.")
}

// Middleware holds a slot for the whole request; key "" skips the limit.
func (o *OpLimiter) Middleware(what string, limit int, key func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			if k == "" {
				next.ServeHTTP(w, r)
				return
			}
			release, ok := o.Acquire(k, limit)
			if !ok {
				w.Header().Set("Retry-After", "2")
				problem.Write(w, r, ErrTooManyConcurrent(what, limit))
				return
			}
			defer release()
			next.ServeHTTP(w, r)
		})
	}
}

// WindowLimiter allows at most N events per key in a sliding window (connection tests).
type WindowLimiter struct {
	now    func() time.Time
	mu     sync.Mutex
	events map[string][]time.Time
	calls  int
}

// NewWindowLimiter returns an empty limiter.
func NewWindowLimiter(now func() time.Time) *WindowLimiter {
	if now == nil {
		now = time.Now
	}
	return &WindowLimiter{now: now, events: map[string][]time.Time{}}
}

// Allow records an event for key unless n already happened within window.
func (l *WindowLimiter) Allow(key string, n int, window time.Duration) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.calls++; l.calls%256 == 0 {
		for k, ts := range l.events { // forget keys with nothing inside the window
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= window {
				delete(l.events, k)
			}
		}
	}
	kept := l.events[key][:0]
	for _, t := range l.events[key] {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= n {
		l.events[key] = kept
		return false, window - now.Sub(kept[0])
	}
	l.events[key] = append(kept, now)
	return true, 0
}
