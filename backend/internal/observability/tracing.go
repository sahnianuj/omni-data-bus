package observability

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TracingConfig turns on OpenTelemetry tracing (OC-031).
type TracingConfig struct {
	// Endpoint is the OTLP/HTTP collector URL (OC_OTEL_EXPORTER_OTLP_ENDPOINT); empty = off.
	Endpoint string
	// SampleRatio is the parent-based trace ID ratio (OC_OTEL_SAMPLE_RATIO, default 0.1).
	SampleRatio float64
	Version     string
	// Exporter replaces the OTLP exporter (tests use an in-memory one).
	Exporter sdktrace.SpanExporter
}

const instrumentation = "omnidatabus"

// Tracer is the application's tracer. It's a no-op until SetupTracing installs a provider.
func Tracer() trace.Tracer { return otel.Tracer(instrumentation) }

// SetupTracing installs the global tracer provider and W3C propagation. It returns the flush
// function to call at shutdown; with no endpoint and no exporter tracing stays off.
func SetupTracing(ctx context.Context, c TracingConfig) (func(context.Context) error, error) {
	exp := c.Exporter
	if exp == nil {
		if c.Endpoint == "" {
			return func(context.Context) error { return nil }, nil
		}
		var err error
		if exp, err = otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(c.Endpoint)); err != nil {
			return nil, err
		}
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", "omni-data-bus"), attribute.String("service.version", c.Version),
	))
	if err != nil {
		return nil, err
	}
	sp := sdktrace.NewBatchSpanProcessor(exp)
	if c.Exporter != nil {
		sp = sdktrace.NewSimpleSpanProcessor(exp) // tests read spans as soon as they end
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(c.SampleRatio))),
		sdktrace.WithSpanProcessor(sp), sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp.Shutdown, nil
}

// Tracing starts a server span per request (joining an incoming traceparent) and names it
// "<METHOD> <route pattern>" once routing has run.
func Tracing(next http.Handler) http.Handler {
	named := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		span := trace.SpanFromContext(r.Context())
		route := RoutePattern(r)
		span.SetName(r.Method + " " + route)
		span.SetAttributes(attribute.String("http.route", route))
	})
	return otelhttp.NewHandler(named, "http.server",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method }),
		otelhttp.WithMeterProvider(nil),
	)
}

// TraceID is the current trace ID, or "" when the context has no valid span.
func TraceID(ctx context.Context) string {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		return sc.TraceID().String()
	}
	return ""
}

// traceHandler adds trace_id and span_id to records logged with a traced context.
type traceHandler struct{ next slog.Handler }

func (h traceHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r = r.Clone()
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.next.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.next.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.next.WithGroup(name)}
}

type noDBSpansKey struct{}

// WithoutDBSpans marks ctx so source queries under it get no spans (bulk reads would make one
// span per batch; the job's phase spans carry batch events instead).
func WithoutDBSpans(ctx context.Context) context.Context {
	return context.WithValue(ctx, noDBSpansKey{}, true)
}

// PgxTracer implements pgx.QueryTracer: it times every statement and, inside a traced
// request, adds a client span with the SQL text. Arguments are never recorded.
type PgxTracer struct{}

type pgxStartKey struct{}

type pgxStart struct {
	at   time.Time
	span trace.Span
}

const maxStatement = 2048

// TraceQueryStart implements pgx.QueryTracer.
func (PgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	st := &pgxStart{at: time.Now()}
	if trace.SpanContextFromContext(ctx).IsValid() && ctx.Value(noDBSpansKey{}) == nil {
		stmt := data.SQL
		if len(stmt) > maxStatement {
			stmt = stmt[:maxStatement]
		}
		ctx, st.span = Tracer().Start(ctx, "pgx "+operation(data.SQL), trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(attribute.String("db.system.name", "postgresql"), attribute.String("db.query.text", stmt)))
	}
	return context.WithValue(ctx, pgxStartKey{}, st)
}

// TraceQueryEnd implements pgx.QueryTracer.
func (PgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	st, ok := ctx.Value(pgxStartKey{}).(*pgxStart)
	if !ok {
		return
	}
	SourceQuerySeconds.Observe(time.Since(st.at).Seconds())
	if st.span == nil {
		return
	}
	if data.Err != nil && !errors.Is(data.Err, context.Canceled) {
		// Only the SQLSTATE: PostgreSQL messages can quote row values.
		msg := "query failed"
		var pe *pgconn.PgError
		if errors.As(data.Err, &pe) {
			msg = "SQLSTATE " + pe.Code
		}
		st.span.SetStatus(codes.Error, msg)
	}
	st.span.End()
}

// operation is the statement's first keyword: SELECT, FETCH, DECLARE…
func operation(sql string) string {
	f := strings.Fields(sql)
	if len(f) == 0 {
		return "query"
	}
	return strings.ToUpper(f[0])
}
