# OC-031 — Observability: metrics, tracing & structured logs

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E6 Operate & Harden | P1 | 5 | OC-002, OC-025, OC-027 | Implemented |

## User story
As an **operator**, I want Prometheus metrics, OpenTelemetry traces, and correlated logs for the API and the migration workers, so that I can run Omni-Data-Bus in production, alert on problems, and debug slow or failing migrations.

## Implementation details

### Metrics (`internal/observability/metrics.go`, `prometheus/client_golang`)
Exposed on `/metrics` (or on `OC_METRICS_ADDR` to keep it off the public port).

| Metric | Type | Labels |
|---|---|---|
| `oc_http_requests_total` | counter | route, method, status |
| `oc_http_request_duration_seconds` | histogram | route, method |
| `oc_jobs` | gauge | status |
| `oc_job_docs_total` | counter | outcome (written/skipped/failed) |
| `oc_job_throughput_docs_per_second` | gauge | job_id (only while running; removed at end to avoid cardinality growth) |
| `oc_reader_*`, `oc_writer_*` | see OC-024/025 | **No per-row labels**; job_id only on gauges that are cleaned up |
| `oc_sse_subscribers` | gauge | — |
| `oc_events_poll_errors_total` | counter | — |
| `oc_worker_capacity`, `oc_worker_active_jobs` | gauge | worker |
| `oc_discovery_duration_seconds` | histogram | source_type |
| `oc_automap_duration_seconds` | histogram | — |
| Go runtime & process collectors | — | — |

A **cardinality guard** unit test fails if any metric uses a label from a deny-list (`key`, `table`, `user_id`, `email`).

### Tracing (OpenTelemetry)
- Enabled with `OC_OTEL_EXPORTER_OTLP_ENDPOINT` (the OTLP gRPC/HTTP exporter) and sampled with `parentbased_traceidratio` at 0.1 by default.
- Spans:
  - The HTTP server via `otelhttp`, with the route pattern as the span name.
  - pgx via a tracer hook (`pgx.QueryTracer`), with statement names but **not** parameters.
  - Discovery, automap, and preview.
  - Per job: a root span `job.run` with child spans per mapping phase, plus **per-batch events** rather than per-row spans, to control volume.
- Trace IDs are injected into slog records (`trace_id`, `span_id`) and into the problem+json `instance` field, so a user-reported error can be found.

### Logs
- JSON slog with redaction (OC-002). Job-scoped loggers add `job_id`, `project_id`, `mapping_id`, and `partition`.
- Log levels: per-batch logs at debug level only; state transitions at info.

### Operational assets (`deploy/observability/`)
- A **Grafana dashboard JSON** with rows for API health (RPS, p95, 5xx), Jobs (active, throughput, errors by code, writer effective concurrency, retries), Workers (capacity, heartbeats), and Postgres/Couchbase dependency latency.
- **Prometheus alert rules:**
  - `OmniDataBusHighErrorRate` (5xx > 2% for 5 m);
  - `OmniDataBusJobStalled` (running job throughput 0 for 5 m);
  - `OmniDataBusWriterThrottled` (effective concurrency < 25% of configured for 10 m);
  - `OmniDataBusProgressPollFailing` (poll errors increasing).

## Acceptance criteria & unit tests

### AC1 — HTTP metrics
**Given** requests to `/api/v1/projects/{id}`, **then** `oc_http_requests_total{route="/api/v1/projects/{id}"}` increments (route pattern, not the raw path), and the latency histogram records it.

| Test | Location | Verifies |
|---|---|---|
| `TestMetricsMiddleware_AC1_RoutePatternLabel` | `internal/observability/metrics_test.go` | `testutil.ToFloat64` |
| `TestMetricsMiddleware_AC1_Histogram` | same | |

### AC2 — Job metrics & cleanup
**Given** a job runs and completes, **then** the docs counters increase by the outcome counts, and the per-job throughput gauge is deleted at the end.

| Test | Location | Verifies |
|---|---|---|
| `TestJobMetrics_AC2_CountersAndGaugeCleanup` | `internal/observability/jobmetrics_test.go` | |

### AC3 — Cardinality guard
**Given** all registered metrics, **then** none use a deny-listed label.

| Test | Location | Verifies |
|---|---|---|
| `TestMetrics_AC3_NoHighCardinalityLabels` | `internal/observability/metrics_test.go` | Iterates `prometheus.DefaultGatherer` descriptors |

### AC4 — Tracing & correlation
**Given** tracing is enabled with an in-memory exporter, **then** an API request produces a server span named after the route with child pgx spans without SQL parameters, and the log record and the problem `instance` carry the same trace ID.

| Test | Location | Verifies |
|---|---|---|
| `TestTracing_AC4_ServerAndDBSpans` | `internal/observability/tracing_test.go` | `tracetest.NewInMemoryExporter` |
| `TestTracing_AC4_NoSQLParams` | same | |
| `TestTracing_AC4_LogAndProblemCorrelation` | same | |

### AC5 — Metrics endpoint placement
**Given** `OC_METRICS_ADDR=:9090`, **then** `/metrics` is served there and returns 404 on the public port.

| Test | Location | Verifies |
|---|---|---|
| `TestMetricsServer_AC5_SeparateAddr` | `internal/app/server_test.go` | |

### AC6 — Dashboards & alerts are valid
**Given** the dashboard JSON and alert rules, **then** the JSON parses, every panel's PromQL references existing metric names, and `promtool check rules` passes (in CI).

| Test | Location | Verifies |
|---|---|---|
| `TestDashboards_AC6_MetricNamesExist` | `internal/observability/assets_test.go` | Extracts metric names by regex from the dashboard and compares them with the registry |
| CI step `promtool check rules deploy/observability/alerts.yml` | `.github/workflows/ci.yml` | |

## Implementation notes
- Metrics are registered on the default registry through a recording registerer (`observability.Collectors()`), so the cardinality guard and the dashboard test see every metric, including vectors with no series yet.
- Counters carry no job, mapping or partition labels (OC-024's `oc_reader_rows_total{job,mapping,partition}` became unlabelled). `job_id` appears only on `oc_job_throughput_docs_per_second`, `oc_writer_effective_concurrency` and `oc_writer_configured_concurrency`, which `JobMetrics.End` deletes when a run ends (including pause).
- Added beyond the table: `oc_job_errors_total{stage,code}` (errors by code), `oc_worker_heartbeats_total{worker}`, `oc_writer_configured_concurrency{job_id}` (for the throttling alert) and `oc_source_query_duration_seconds` (Postgres dependency latency). `oc_writer_latency_seconds` times a document batch.
- `oc_jobs{status}` covers unfinished statuses only, refreshed every 15 s from the metadata store; every node reports the same numbers, so dashboards use `max()`.
- Tracing is OTLP/HTTP (`otlptracehttp`); span names are `<METHOD> <route pattern>`. pgx spans are created only inside a traced request and never for bulk migration reads (`WithoutDBSpans`); SQL text is recorded, arguments and PostgreSQL error messages are not. Job spans: `job.run` → `job.mapping` → `mapping.prepare` / `mapping.migrate` (one `batch` event per acknowledged batch; the SDK caps events per span at 128) / `mapping.verify`.
- Correlation: when a request is traced and has no `X-Request-ID`, its request ID is the trace ID, so `X-Request-ID`, the problem `instance`, and the logs' `request_id`/`trace_id` all match.
- `/metrics` is a documented route in `api/openapi.yaml`; with `OC_METRICS_ADDR` it returns 404 on the public port instead of falling through to the SPA.
- An end-to-end check that a pipeline run feeds and cleans up the job metrics is in `internal/pipeline/metrics_test.go`.
