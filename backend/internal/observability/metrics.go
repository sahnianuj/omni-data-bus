package observability

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// recorder remembers the collectors registered through it, so tests can check every metric,
// including vectors that have no series yet (Gather skips those).
type recorder struct {
	prometheus.Registerer
	mu         sync.Mutex
	collectors []prometheus.Collector
}

func (r *recorder) Register(c prometheus.Collector) error {
	r.mu.Lock()
	r.collectors = append(r.collectors, c)
	r.mu.Unlock()
	return r.Registerer.Register(c)
}

func (r *recorder) MustRegister(cs ...prometheus.Collector) {
	for _, c := range cs {
		if err := r.Register(c); err != nil {
			panic(err)
		}
	}
}

var (
	registered = &recorder{Registerer: prometheus.DefaultRegisterer}
	factory    = promauto.With(registered)
)

// Collectors are the application's metrics (not the runtime and process collectors).
func Collectors() []prometheus.Collector {
	registered.mu.Lock()
	defer registered.mu.Unlock()
	return append([]prometheus.Collector(nil), registered.collectors...)
}

// Every metric lives on the default registry, next to the Go runtime and process collectors.
// Labels are bounded: route patterns, enums and error codes; job_id only on gauges that are
// deleted when the job ends (OC-031). Never label by key, table, user or email.
var (
	HTTPRequests = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "oc_http_requests_total", Help: "HTTP requests by route pattern, method and status.",
	}, []string{"route", "method", "status"})
	HTTPDuration = factory.NewHistogramVec(prometheus.HistogramOpts{
		Name: "oc_http_request_duration_seconds", Help: "HTTP request latency by route pattern and method.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"route", "method"})

	Jobs = factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "oc_jobs", Help: "Jobs that aren't finished, by status (refreshed every 15 s).",
	}, []string{"status"})
	JobDocs = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "oc_job_docs_total", Help: "Documents processed by migration jobs, by outcome.",
	}, []string{"outcome"})
	JobErrors = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "oc_job_errors_total", Help: "Row errors by stage and error code.",
	}, []string{"stage", "code"})
	JobThroughput = factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "oc_job_throughput_docs_per_second", Help: "Current throughput of each running job (removed when it ends).",
	}, []string{"job_id"})

	ReaderRows = factory.NewCounter(prometheus.CounterOpts{
		Name: "oc_reader_rows_total", Help: "Rows read from sources.",
	})
	ReaderBatchSeconds = factory.NewHistogram(prometheus.HistogramOpts{
		Name: "oc_reader_batch_seconds", Help: "Time to read one source batch, retries included.",
		Buckets: prometheus.ExponentialBuckets(0.001, 2.5, 12),
	})
	ReaderRetries = factory.NewCounter(prometheus.CounterOpts{
		Name: "oc_reader_retries_total", Help: "Source reads retried after a transient error.",
	})
	SourceQuerySeconds = factory.NewHistogram(prometheus.HistogramOpts{
		Name: "oc_source_query_duration_seconds", Help: "PostgreSQL query latency (every statement on source connections).",
		Buckets: prometheus.ExponentialBuckets(0.0005, 3, 12),
	})

	WriterOps = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "oc_writer_ops_total", Help: "Document writes by outcome and error code (code is empty on success).",
	}, []string{"outcome", "code"})
	WriterLatency = factory.NewHistogram(prometheus.HistogramOpts{
		Name: "oc_writer_latency_seconds", Help: "Time to write one document batch to Couchbase.",
		Buckets: prometheus.ExponentialBuckets(0.001, 2.5, 12),
	})
	WriterInflight = factory.NewGauge(prometheus.GaugeOpts{
		Name: "oc_writer_inflight", Help: "Document batches being written now.",
	})
	WriterRetries = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "oc_writer_retries_total", Help: "Write attempts retried, by the write's final code (empty when it succeeded).",
	}, []string{"code"})
	WriterEffectiveConcurrency = factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "oc_writer_effective_concurrency", Help: "In-flight KV operation limit after adaptive throttling, per running job.",
	}, []string{"job_id"})
	WriterConfiguredConcurrency = factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "oc_writer_configured_concurrency", Help: "Configured KV operation limit, per running job.",
	}, []string{"job_id"})

	SSESubscribers = factory.NewGauge(prometheus.GaugeOpts{
		Name: "oc_sse_subscribers", Help: "Open job event subscriptions on this node.",
	})
	EventsPollErrors = factory.NewCounter(prometheus.CounterOpts{
		Name: "oc_events_poll_errors_total", Help: "Failed reads of job progress documents by the event hub.",
	})

	WorkerCapacity = factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "oc_worker_capacity", Help: "Jobs a worker may run at once.",
	}, []string{"worker"})
	WorkerActiveJobs = factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "oc_worker_active_jobs", Help: "Jobs a worker runs now.",
	}, []string{"worker"})
	WorkerHeartbeats = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "oc_worker_heartbeats_total", Help: "Job heartbeats written by a worker.",
	}, []string{"worker"})

	DiscoveryDuration = factory.NewHistogramVec(prometheus.HistogramOpts{
		Name: "oc_discovery_duration_seconds", Help: "Schema discovery time by source type.",
		Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"source_type"})
	AutomapDuration = factory.NewHistogram(prometheus.HistogramOpts{
		Name: "oc_automap_duration_seconds", Help: "Auto-mapping time per mapping.",
		Buckets: prometheus.ExponentialBuckets(0.0005, 3, 10),
	})
)

// MetricsHandler serves the default registry in the Prometheus text format.
func MetricsHandler() http.Handler { return promhttp.Handler() }

// RoutePattern is the chi route pattern that matched r, or "unmatched" (never the raw path,
// which would make one series per URL).
func RoutePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return "unmatched"
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// HTTPMetrics counts requests and records their latency, labelled by route pattern (OC-031 AC1).
func HTTPMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		route := RoutePattern(r)
		HTTPRequests.WithLabelValues(route, r.Method, strconv.Itoa(status)).Inc()
		HTTPDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
	})
}
