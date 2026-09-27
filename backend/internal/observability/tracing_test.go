package observability_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/problem"
)

var (
	exporterOnce sync.Once
	exporter     *tracetest.InMemoryExporter
)

// traced installs an always-sampling provider with an in-memory exporter (once per process)
// and clears previously exported spans.
func traced(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporterOnce.Do(func() {
		exporter = tracetest.NewInMemoryExporter()
		_, err := observability.SetupTracing(context.Background(), observability.TracingConfig{Exporter: exporter, SampleRatio: 1})
		require.NoError(t, err)
	})
	exporter.Reset()
	return exporter
}

// query runs a statement through the pgx tracer hook, as pgx does for every query.
func query(ctx context.Context, sql string, args ...any) {
	tr := observability.PgxTracer{}
	ctx = tr.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: sql, Args: args})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
}

func router(logger *slog.Logger, h http.HandlerFunc) http.Handler {
	return httpapi.NewRouter(httpapi.Options{
		Logger:   logger,
		Features: []httpapi.Registrar{httpapi.RegistrarFunc(func(r chi.Router) { r.Get("/projects/{id}", h) })},
	})
}

func byName(spans tracetest.SpanStubs, name string) *tracetest.SpanStub {
	for i := range spans {
		if spans[i].Name == name {
			return &spans[i]
		}
	}
	return nil
}

func TestTracing_AC4_ServerAndDBSpans(t *testing.T) {
	exp := traced(t)
	h := router(slog.New(slog.DiscardHandler), func(w http.ResponseWriter, r *http.Request) {
		query(r.Context(), "SELECT id, name FROM project WHERE id = $1", chi.URLParam(r, "id"))
		w.WriteHeader(http.StatusOK)
	})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/projects/p42", nil))

	spans := exp.GetSpans()
	server := byName(spans, "GET /api/v1/projects/{id}")
	require.NotNil(t, server, "the server span is named after the route pattern: %v", names(spans))
	db := byName(spans, "pgx SELECT")
	require.NotNil(t, db)
	require.Equal(t, server.SpanContext.SpanID(), db.Parent.SpanID(), "the query is a child of the request")
	require.Equal(t, server.SpanContext.TraceID(), db.SpanContext.TraceID())
}

func TestTracing_AC4_NoSQLParams(t *testing.T) {
	exp := traced(t)
	h := router(slog.New(slog.DiscardHandler), func(w http.ResponseWriter, r *http.Request) {
		query(r.Context(), "SELECT * FROM customer WHERE email = $1 AND token = $2", "ann@example.com", "s3cr3t-token")
		w.WriteHeader(http.StatusOK)
	})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/projects/p1", nil))

	db := byName(exp.GetSpans(), "pgx SELECT")
	require.NotNil(t, db)
	var all strings.Builder
	for _, a := range db.Attributes {
		all.WriteString(string(a.Key) + "=" + a.Value.String() + "\n")
	}
	for _, ev := range db.Events {
		for _, a := range ev.Attributes {
			all.WriteString(a.Value.String())
		}
	}
	require.Contains(t, all.String(), "email = $1", "the statement is recorded")
	require.NotContains(t, all.String(), "ann@example.com")
	require.NotContains(t, all.String(), "s3cr3t-token")
}

func TestTracing_DBSpansSuppressedForBulkReads(t *testing.T) {
	exp := traced(t)
	ctx, span := observability.Tracer().Start(context.Background(), "mapping.migrate")
	query(observability.WithoutDBSpans(ctx), "FETCH 1000 FROM c")
	span.End()
	require.Nil(t, byName(exp.GetSpans(), "pgx FETCH"))
	require.NotNil(t, byName(exp.GetSpans(), "mapping.migrate"))
}

func TestTracing_AC4_LogAndProblemCorrelation(t *testing.T) {
	exp := traced(t)
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, "json", "info")
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := router(logger, func(w http.ResponseWriter, r *http.Request) {
		problem.Write(w, r, errors.New("boom"))
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/projects/p1", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var p problem.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	server := byName(exp.GetSpans(), "GET /api/v1/projects/{id}")
	require.NotNil(t, server)
	traceID := server.SpanContext.TraceID().String()
	require.Equal(t, traceID, p.Instance, "the problem instance is the trace ID")
	require.Equal(t, traceID, rec.Header().Get("X-Request-ID"))

	var sawFailure, sawAccess bool
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		var rec map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &rec))
		switch rec["msg"] {
		case "request failed":
			sawFailure = true
			require.Equal(t, traceID, rec["trace_id"])
			require.NotEmpty(t, rec["span_id"])
		case "http request":
			sawAccess = true
			require.Equal(t, traceID, rec["trace_id"])
			require.Equal(t, traceID, rec["request_id"])
		}
	}
	require.True(t, sawFailure && sawAccess, buf.String())
}

func TestTracing_OffByDefault(t *testing.T) {
	flush, err := observability.SetupTracing(context.Background(), observability.TracingConfig{})
	require.NoError(t, err)
	require.NoError(t, flush(context.Background()))
	require.Empty(t, observability.TraceID(context.Background()))
}

func names(spans tracetest.SpanStubs) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Name)
	}
	return out
}

var _ sdktrace.SpanExporter = (*tracetest.InMemoryExporter)(nil)
