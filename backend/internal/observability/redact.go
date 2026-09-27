// Package observability provides logging, Prometheus metrics and OpenTelemetry tracing for Omni-Data-Bus.
package observability

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

var (
	sensitiveKey = regexp.MustCompile(`(?i)(password|secret|token|key|authorization|cookie|dsn)`)
	// scheme://user:password@host → keep the user, mask the password.
	credentialsInURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/@\s]+):([^@\s]+)@`)
)

const masked = "***"

// RedactHandler masks sensitive attributes before they reach the wrapped handler.
type RedactHandler struct{ next slog.Handler }

// NewRedactHandler wraps next.
func NewRedactHandler(next slog.Handler) *RedactHandler { return &RedactHandler{next: next} }

// Enabled implements slog.Handler.
func (h *RedactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

// Handle implements slog.Handler.
func (h *RedactHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, redactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

// WithAttrs implements slog.Handler.
func (h *RedactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = redactAttr(a)
	}
	return &RedactHandler{next: h.next.WithAttrs(clean)}
}

// WithGroup implements slog.Handler.
func (h *RedactHandler) WithGroup(name string) slog.Handler {
	return &RedactHandler{next: h.next.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		group := v.Group()
		clean := make([]any, 0, len(group))
		for _, g := range group {
			clean = append(clean, redactAttr(g))
		}
		return slog.Group(a.Key, clean...)
	}
	if sensitiveKey.MatchString(a.Key) && !isSafeKey(a.Key) {
		return slog.String(a.Key, masked)
	}
	if v.Kind() == slog.KindString {
		return slog.String(a.Key, redactString(v.String()))
	}
	return slog.Attr{Key: a.Key, Value: v}
}

// isSafeKey allows well-known non-secret keys that happen to contain a sensitive word.
func isSafeKey(k string) bool {
	switch strings.ToLower(k) {
	case "keyspace", "key_id", "keyid", "monkey":
		return true
	}
	return false
}

func redactString(s string) string {
	return credentialsInURL.ReplaceAllString(s, "$1:"+masked+"@")
}

// NewLogger builds the process logger. format is "json" or "text".
func NewLogger(w io.Writer, format, level string) *slog.Logger {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(level))
	opts := &slog.HandlerOptions{Level: lvl}
	var base slog.Handler = slog.NewJSONHandler(w, opts)
	if format == "text" {
		base = slog.NewTextHandler(w, opts)
	}
	return slog.New(NewRedactHandler(traceHandler{base}))
}
