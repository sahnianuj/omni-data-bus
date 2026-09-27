// Package httpapi holds the HTTP router, middleware and cross-cutting handlers.
package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"omnidatabus/internal/observability"
	"omnidatabus/internal/problem"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ctxKey int

const loggerKey ctxKey = iota

// LoggerFrom returns the request-scoped logger (falls back to slog.Default).
func LoggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// WithLogger stores a logger in the context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// RequestID propagates X-Request-ID or generates one, and seeds the request logger. A traced
// request's generated ID is its trace ID, so the ID a user quotes finds the trace (OC-031).
func RequestID(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" || len(id) > 128 {
				id = observability.TraceID(r.Context())
				if id == "" {
					id = uuid.Must(uuid.NewV7()).String()
				}
				r.Header.Set("X-Request-ID", id)
			}
			w.Header().Set("X-Request-ID", id)
			ctx := WithLogger(r.Context(), base.With("request_id", id))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Recoverer turns panics into a 500 problem and logs the stack trace.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { //nolint:contextcheck // uses r.Context() via r; deferred recover can't take ctx
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				LoggerFrom(r.Context()).ErrorContext(r.Context(), "panic", "panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
				problem.Write(w, r, fmt.Errorf("panic: %v", rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Flush keeps Server-Sent Events working through the recorder.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog writes exactly one structured line per request, labelled with the route pattern.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		route := r.URL.Path
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		LoggerFrom(r.Context()).LogAttrs(r.Context(), slog.LevelInfo, "http request",
			slog.String("method", r.Method), slog.String("route", route), slog.Int("status", status),
			slog.Int("bytes", rec.bytes), slog.Int64("duration_ms", time.Since(start).Milliseconds()))
	})
}

// ContentSecurityPolicy allows only same-origin scripts, styles (plus inline styles, which
// the UI's positioning libraries set), images (plus data: URIs), fonts and API calls. Fonts are
// self-hosted; there is no CDN.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// SecurityHeaders sets the browser security headers on every response (OC-033). HSTS is sent
// only when the public URL is https: on plain http it would be ignored, or pin a dev host.
func SecurityHeaders(hsts bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", ContentSecurityPolicy)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("X-Frame-Options", "DENY")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout bounds request handling time, except for streaming (SSE) routes.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/events") {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
