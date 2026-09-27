# OC-002 — Backend service skeleton (config, logging, errors, health, OpenAPI)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E1 Foundation | P0 | 5 | OC-001 | Implemented |

## User story
As an **engineer**, I want a production-grade HTTP service skeleton with typed config, structured logs, a uniform error format, health endpoints, and a contract-first API, so that feature stories only add handlers and business logic.

## Implementation details

### Entry point `cmd/odb/main.go`
- Flags: `--role=all|api|worker` (default `all`) and `--config=<path>` (optional YAML). Environment variables override the file.
- Startup sequence: load config → init logger → connect to the metadata cluster → run provisioning (when `OC_META_AUTO_PROVISION=true`, the default in dev) → build the app container → start the HTTP server and/or the worker → wait for SIGINT/SIGTERM → graceful shutdown (HTTP `Shutdown` with a 30 s timeout, workers drain, see OC-029).

### Config (`internal/config`)
```go
type Config struct {
    Role          string        `env:"OC_ROLE" default:"all" validate:"oneof=all api worker"`
    HTTPAddr      string        `env:"OC_HTTP_ADDR" default:":8080"`
    PublicURL     string        `env:"OC_PUBLIC_URL" default:"http://localhost:5173"`
    Meta          MetaConfig    // OC_META_* — Couchbase Capella metadata store (OC-003); password is secret
    MasterKey     string        `env:"OC_MASTER_KEY" validate:"required"`     // secret, OC-006
    LogLevel      string        `env:"OC_LOG_LEVEL" default:"info"`
    LogFormat     string        `env:"OC_LOG_FORMAT" default:"json" validate:"oneof=json text"`
    AllowPrivateNetworks bool   `env:"OC_ALLOW_PRIVATE_NETWORKS" default:"true"` // OC-033
    Worker        WorkerConfig  // concurrency, heartbeat intervals (OC-023)
    Session       SessionConfig // ttl, cookie secure flag (OC-005)
}
```
- Uses `github.com/caarlos0/env/v11` plus the validator. `Config.Redacted()` returns a copy with secrets replaced by `"***"` for startup logging.

### Logging
- `slog` with a JSON handler. A request-scoped logger is stored in the context and carries `request_id`, `user_id`, `workspace_id`, `route`, `method`, `status`, and `duration_ms`.
- The redaction handler wraps the base handler and masks any attribute whose key matches `(?i)(password|secret|token|key|authorization|cookie|dsn)`, as well as values that look like DSNs with credentials (`postgres://user:pass@`).

### Router & middleware (`internal/httpapi`)
The chain runs in this order: `RequestID` → `RealIP` (only when behind a trusted proxy list) → `Recoverer` (500 problem, stack trace logged) → `AccessLog` → `SecurityHeaders` (OC-033) → `Timeout(60s)` (skipped for SSE routes) → `Auth` (OC-005, per group) → `CSRF` (per group).

- `/healthz` is liveness and always returns 200 once the process is serving.
- `/readyz` pings the metadata bucket's KV service (1 s timeout). It returns 200 or 503 with a JSON body listing each check.
- `/metrics` is Prometheus (OC-031), bound to `OC_METRICS_ADDR` if set.
- `/api/v1/*` is the generated `StrictServerInterface` from `oapi-codegen` (strict mode), implemented by feature packages.
- `/` serves the embedded SPA (`web/dist` via `embed.FS`) with an `index.html` fallback for client routes.

### Error model (`internal/problem`)
```go
type Problem struct {
    Type string `json:"type"`; Title string `json:"title"`; Status int `json:"status"`
    Detail string `json:"detail,omitempty"`; Code string `json:"code"`
    Instance string `json:"instance,omitempty"`; FieldErrors []FieldError `json:"fieldErrors,omitempty"`
}
func Write(w http.ResponseWriter, r *http.Request, err error)
```
- Domain errors implement `interface{ Problem() Problem }`. Unknown errors become a 500 with `code=INTERNAL`, and the detail is never leaked. `validator.ValidationErrors` becomes a 422 with `fieldErrors` whose field names are camelCase JSON paths.
- The catalog of error codes lives in `internal/problem/codes.go` as constants and is documented in OpenAPI `components.schemas.ErrorCode` (enum).

### OpenAPI
- `api/openapi.yaml` starts with `/healthz`, `/readyz`, the `Problem` schema, the `ErrorCode` enum, and security scheme `cookieAuth`.
- `oapi-codegen` config: `strict-server: true`, `models: true`, `chi-server: true`, output `internal/httpapi/gen/api.gen.go`.
- Web: `pnpm gen:api` runs `openapi-typescript` to produce `src/lib/api/schema.d.ts`. `src/lib/api/client.ts` wraps `openapi-fetch` with credentials `include`, CSRF header injection, and problem+json parsing into an `ApiError` class.

## Acceptance criteria & unit tests

### AC1 — Config validation fails fast
**Given** `OC_META_CONNSTR` is missing, **when** the binary starts, **then** it exits with code 2 and a single log line naming the missing variable, without printing any secret values.

| Test | Location | Verifies |
|---|---|---|
| `TestLoad_AC1_MissingRequiredVar` | `internal/config/config_test.go` | Returns an error containing `OC_META_CONNSTR` |
| `TestRedacted_AC1_MasksSecrets` | same | `Redacted()` masks the metadata password and MasterKey |
| `TestLoad_AC1_InvalidRole` | same | `OC_ROLE=foo` → validation error |

### AC2 — Structured, redacted logs
**Given** any request, **when** it completes, **then** exactly one access log JSON line is emitted with request_id, method, route pattern (not the raw path), status, and duration_ms. **And** any attribute containing a secret is masked.

| Test | Location | Verifies |
|---|---|---|
| `TestAccessLog_AC2_SingleLineWithFields` | `internal/httpapi/middleware_test.go` | Captures the handler output and asserts the keys and the route pattern `/api/v1/connections/{id}` |
| `TestRedactHandler_AC2_MasksKeys` | `internal/observability/redact_test.go` | Table cases: `password`, `Authorization`, `dsn`, nested groups |
| `TestRedactHandler_AC2_MasksDSNCredentials` | same | `postgres://u:p@h/db` → `postgres://u:***@h/db` |

### AC3 — Uniform problem+json errors
**Given** a handler returns a validation error, a domain error, or panics, **when** the response is written, **then** it has `Content-Type: application/problem+json` and a body matching the `Problem` schema with the correct status and code. A panic yields a 500 with `code=INTERNAL` and no stack trace in the body.

| Test | Location | Verifies |
|---|---|---|
| `TestWrite_AC3_ValidationErrorTo422` | `internal/problem/problem_test.go` | Field errors are mapped to camelCase paths |
| `TestWrite_AC3_DomainErrorUsesItsProblem` | same | Custom `Problem()` is honored |
| `TestWrite_AC3_UnknownErrorIsOpaque500` | same | Detail doesn't contain the original error text |
| `TestRecoverer_AC3_PanicReturnsProblem` | `internal/httpapi/middleware_test.go` | Panic handler → 500 problem |

### AC4 — Health & readiness
**Given** the metadata bucket is reachable, **when** `GET /readyz` is called, **then** it returns `200 {"status":"ok","checks":{"metadata":"ok"}}`. **Given** it is unreachable, **then** it returns `503` with `checks.metadata` holding the error class. `/healthz` returns 200 in both cases.

| Test | Location | Verifies |
|---|---|---|
| `TestReadyz_AC4_OK` | `internal/httpapi/health_test.go` | Fake pinger returns nil → 200 |
| `TestReadyz_AC4_DBDown` | same | Fake pinger returns an error → 503 |
| `TestHealthz_AC4_AlwaysOK` | same | 200 |

### AC5 — Request ID propagation
**Given** a request with `X-Request-ID: abc`, **then** the response echoes it and logs carry it. **Given** none, a UUID v7 is generated.

| Test | Location | Verifies |
|---|---|---|
| `TestRequestID_AC5_EchoesIncoming` | `internal/httpapi/middleware_test.go` | |
| `TestRequestID_AC5_GeneratesWhenAbsent` | same | Valid UUID v7 format |

### AC6 — Graceful shutdown
**Given** an in-flight request, **when** SIGTERM is received, **then** the server stops accepting new connections, the in-flight request completes (up to 30 s), and the process exits 0.

| Test | Location | Verifies |
|---|---|---|
| `TestServer_AC6_GracefulShutdownWaitsForInflight` | `internal/app/server_test.go` | Handler blocks on a channel; `Shutdown` returns only after release |

### AC7 — SPA serving
**Given** a production build, **when** `GET /projects/123` (a client route) is requested, **then** `index.html` is served with `Cache-Control: no-cache`, and hashed assets under `/assets/*` are served with `Cache-Control: public, max-age=31536000, immutable`.

| Test | Location | Verifies |
|---|---|---|
| `TestSPA_AC7_FallbackToIndex` | `internal/httpapi/spa_test.go` | Uses `fstest.MapFS` |
| `TestSPA_AC7_AssetCacheHeaders` | same | |
| `TestSPA_AC7_APIPathNotFallback` | same | `/api/v1/unknown` returns a problem 404, not index.html |

### AC8 — Web API client error handling
**Given** the API returns problem+json, **when** a call is made via `apiClient`, **then** it throws an `ApiError` exposing `status`, `code`, `detail`, and `fieldErrors`. **And** mutating requests include `X-CSRF-Token` read from the in-memory token store.

| Test | Location | Verifies |
|---|---|---|
| `client.test.ts` › `AC8: parses problem+json into ApiError` | `web/src/lib/api/client.test.ts` | MSW returns a 422 problem |
| `client.test.ts` › `AC8: adds CSRF header on POST/PUT/PATCH/DELETE only` | same | |

## Implementation notes (as built)
- **Configuration is environment-only.** There is no `--config` YAML file: every setting is an `OC_*` variable (see README). `OC_DEV=true` fills local dev-stack defaults; without it, `OC_META_*` and `OC_MASTER_KEY` are required.
- **Handlers are hand-written on chi instead of generated by `oapi-codegen`.** `api/openapi.yaml` stays the source of truth. `internal/app/contract_test.go` fails if a route exists in the spec but not in the router (or vice versa), and if the `ErrorCode` enum drifts from `internal/problem`. TypeScript types are generated with `openapi-typescript` (`make gen`).
