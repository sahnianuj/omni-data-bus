# Omni-Data-Bus 1.0.0 — Architecture & Technical Decisions

This document is the shared reference for every story in `docs/release-1.0.0`. Stories link back here instead of repeating the domain model, conventions, and test strategy.

---

## 1. Product summary

Omni-Data-Bus is a web application for moving relational data into Couchbase. In 1.0 it supports PostgreSQL → Couchbase:

0. **Create a project.** Everything below happens inside a project. A project owns exactly one source and one target repository (connection). Their credentials are entered and tested once, stored encrypted on the project, and reused by every mapping and migration run in it (OC-007).
1. **Connect a source.** Inside the project, the user configures a PostgreSQL connection, clicks **Test connection**, and saves it.
2. **Discover.** The app lists the schemas, tables, views, columns, keys, and indexes in the source and estimates row counts.
3. **Connect a target.** Inside the project, the user configures a Couchbase (self-managed or Capella) connection, clicks **Test connection**, and saves it.
4. **Pick the target location.** The user selects or creates a bucket → scope → collection.
5. **Design the document.** The user builds a target JSON document structure in one of four ways: generated from the table, imported by dropping a JSON sample or JSON Schema, sampled from the existing collection, or built by hand.
6. **Auto-map.** The engine maps source columns to target fields and shows a score, for example *"10 of 23 fields automatically mapped"*.
7. **Map manually.** The user finishes the mapping on a drag-and-drop canvas, or in an accessible table view, and adds transformations.
8. **Preview.** The user sees real transformed documents built from sample rows (a dry run).
9. **Migrate.** Rows stream from the source, are transformed, and are written to Couchbase. A live progress bar shows throughput, ETA, and errors, and the job can be paused, resumed, or cancelled.
10. **Verify and report.** When the job finishes, the app checks counts and spot-checks sample documents, and the error report can be downloaded.

---

## 2. Technology decision

### 2.1 Backend: Go

| Criterion | Go | Rust | Java/Kotlin | Node.js |
|---|---|---|---|---|
| Streaming concurrency model | Goroutines and bounded channels give backpressure with no extra framework | async/await with tokio; very fast but more complex | Virtual threads (21+) are good; more ceremony | Single-threaded event loop; CPU-bound transforms block it |
| Couchbase SDK | **gocb v2 — official, GA, KV pipelining, range scan** | Official SDK less mature | Official, mature | Official, mature |
| PostgreSQL driver | **pgx v5 — fastest, binary protocol, snapshot export** | tokio-postgres (good) | JDBC (good) | pg (ok) |
| Memory and footprint | ~20–50 MB RSS, one static binary | Smallest | JVM, 200 MB+ | Moderate |
| Hiring and maintainability | High | Lower | High | High |
| Cold start and container density | Excellent | Excellent | Poor to OK | Good |

**Decision: Go (latest stable, ≥ 1.25).** A migration spends most of its time waiting on the network. Transforms are cheap, about microseconds per row. Go gives enough raw performance with the simplest code for a pipeline of readers, transformers, and writers, and both database drivers it needs are first-class. Rust would give better CPU performance, but that doesn't help an I/O-bound workload, and it would cost more engineering time.

### 2.2 Stack

| Layer | Choice | Notes |
|---|---|---|
| HTTP | `net/http` + `github.com/go-chi/chi/v5` | Middleware chain, route groups |
| API contract | OpenAPI 3.1 (`api/openapi.yaml`) → `oapi-codegen` (Go types and server interface) + `openapi-typescript`/`openapi-fetch` (TS client) | The spec is the single source of truth |
| Metadata store | **Couchbase Capella** (Couchbase Server 7.6+ locally and in CI) | `gocb/v2`: KV for documents, SQL++ with GSI indexes for lists, ACID transactions for multi-document writes, versioned provisioning instead of migrations (OC-003) |
| Source driver | `github.com/jackc/pgx/v5` | |
| Target SDK | `github.com/couchbase/gocb/v2` (latest v2.x) | |
| Expressions | `github.com/expr-lang/expr` | Sandboxed, compiled once and run per row |
| Logging | `log/slog` (JSON) | |
| Metrics and tracing | Prometheus client + OpenTelemetry | |
| Validation | `github.com/go-playground/validator/v10` | |
| Password hashing | `golang.org/x/crypto/argon2` (argon2id) | |
| Rate limiting | `golang.org/x/time/rate` | Migration throttle and login limiter |
| Go tests | `testing`, `testify`, `testcontainers-go` (Postgres, Couchbase), `go test -race` | |
| Frontend | React 19, TypeScript (strict), Vite | |
| Styling / UI kit | Tailwind CSS v4 + shadcn/ui (Radix primitives) + lucide-react icons + Inter/JetBrains Mono | |
| Data fetching | TanStack Query v5 | |
| Routing | React Router v7 (library mode) | |
| Forms | react-hook-form + zod | |
| Client state | Zustand (mapping canvas and designer) | |
| Visual mapping | `@xyflow/react` (React Flow) | |
| Charts | Recharts (throughput sparkline) | |
| Toasts | sonner | |
| FE tests | Vitest + React Testing Library + MSW; Playwright for E2E | |

### 2.3 Real-time progress: Server-Sent Events (SSE)

Progress only flows from server to client, so SSE is enough. It is simpler than WebSockets, works through proxies, and reconnects automatically with `Last-Event-ID`. Commands such as pause and cancel go over plain REST.

---

## 3. System architecture

```
┌──────────────────────────── Browser (React SPA) ────────────────────────────┐
│ Projects+Conn│ Schema Explorer │ Doc Designer │ Mapping Canvas │ Job Monitor  │
└───────────────┬───────────────────────────────────────────────▲─────────────┘
                │ REST (JSON, problem+json errors)              │ SSE /jobs/{id}/events
┌───────────────▼───────────────────────────────────────────────┴─────────────┐
│ odb server (Go, stateless; N replicas)                                      │
│  api/ ─ auth/ ─ connections/ ─ discovery/ ─ couchbase admin/ ─ mapping/      │
│  automap/ ─ transform/ ─ preview/ ─ jobs/ (API)                              │
│  ┌──────────────── worker (same binary, --role=worker|all) ───────────────┐  │
│  │ job claimer (query queued → CAS-guarded claim) → pipeline:              │  │
│  │  Reader(s) ─chan─▶ Transformer pool ─chan─▶ Writer pool ─▶ Checkpointer │  │
│  │  progress ticker → job_progress doc in the metadata bucket (1 s)        │  │
│  └─────────────────────────────────────────────────────────────────────────┘  │
└────────┬──────────────────────┬─────────────────────────────┬────────────────┘
         │                      │                             │
   Metadata: Couchbase     Source PostgreSQL              Target Couchbase
   Capella bucket          (read-only, snapshot           (KV upserts, mgmt API;
   (users, projects,       isolation)                      may be the same Capella
    connections, jobs…)                                    cluster, other bucket)
```

**Horizontal scale.**
- API nodes are stateless: sessions and all other state live in the Couchbase metadata bucket.
- Workers find queued jobs with a SQL++ query and claim one with a **CAS-guarded replace**. If two workers race, one gets a CAS mismatch and moves on, so adding replicas adds capacity.
- **Progress fan-out:** the worker writes a small `job_progress::<jobId>` document every second. Each API node runs one poller per watched job (a KV get every second, shared by all of that node's SSE subscribers), so any API node can serve the SSE stream for any job. There is no Redis, NATS, or LISTEN/NOTIFY.
- **Leader-only chores** (the stale-job reaper) use a lease document inserted with an expiry: whoever inserts `lease::reaper` holds it until it expires.
- One binary runs in three roles: `--role=api`, `--role=worker`, or `--role=all` (the default, for single-node installs).

---

## 4. Repository layout

```
omni-connect/
├── api/openapi.yaml                 # API contract (source of truth)
├── backend/
│   ├── go.mod                       # module github.com/<org>/omni-data-bus/backend
│   ├── cmd/odb/main.go     # flags: --role, --config
│   ├── internal/
│   │   ├── app/                     # wiring / DI
│   │   ├── config/
│   │   ├── httpapi/                 # chi router, middleware, handlers (generated iface impl)
│   │   ├── problem/                 # RFC 9457 errors
│   │   ├── auth/                    # users, sessions, csrf
│   │   ├── crypto/                  # envelope encryption for secrets
│   │   ├── netguard/                # SSRF / egress policy
│   │   ├── store/                   # Couchbase metadata repositories, SQL++ files, provisioning
│   │   ├── connectors/
│   │   │   ├── source/              # SourceConnector interface, registry, types
│   │   │   │   └── postgres/
│   │   │   └── target/
│   │   │       └── couchbase/
│   │   ├── discovery/
│   │   ├── docschema/               # target doc model, inference, JSON Schema import
│   │   ├── mapping/                 # mapping model, validation
│   │   ├── automap/                 # matching engine + score
│   │   ├── transform/               # type coercion, transform steps, compiler
│   │   ├── preview/
│   │   ├── jobs/                    # job lifecycle, claimer, heartbeats
│   │   ├── pipeline/                # reader / transformer / writer / checkpoint
│   │   ├── progress/                # counters, EWMA, ETA, notifier
│   │   ├── events/                  # SSE hub (per-job progress-doc poller)
│   │   └── observability/
├── web/
│   ├── src/
│   │   ├── app/                     # router, providers, layout
│   │   ├── components/ui/           # shadcn components
│   │   ├── components/              # shared app components
│   │   ├── features/{auth,connections,discovery,targets,projects,docschema,mapping,jobs}/
│   │   ├── lib/{api,sse,format,utils}/
│   │   └── styles/
│   └── tests/e2e/                   # Playwright
├── deploy/{docker,compose,helm}/
├── docs/
└── Makefile
```

---

## 5. Domain model (metadata store: Couchbase Capella)

Everything lives in **one bucket** (default `omniconnect`, created by the operator in Capella) and **one scope** (default `meta`). The app creates the collections and indexes (OC-003). Documents are JSON with camelCase fields, and every tenant document carries `workspaceId`.

| Collection | Document key | Main fields |
|---|---|---|
| `users` | `<userId>` | id (UUID v7), email (lower-cased), passwordHash, displayName, createdAt, updatedAt |
| `workspaces` | `<workspaceId>` | id, name, createdAt |
| `members` | `<workspaceId>::<userId>` | workspaceId, userId, role (`owner`\|`editor`\|`viewer`) |
| `sessions` | `<sha256(token)>` | userId, workspaceId, csrfToken, createdAt, lastSeenAt, userAgent, ip. **Document expiry** = absolute TTL, so there is no cleanup job |
| `uniques` | `email::<email>`, `projname::<workspaceId>::<lower(name)>` | owner id. Inserted **in the same transaction** as the owning document to enforce uniqueness |
| `projects` | `<projectId>` | id, workspaceId, name, description, createdBy, createdAt, updatedAt |
| `connections` | `<projectId>::<role>` | id, workspaceId, projectId, role (`source`\|`target`), type (`postgres`\|`couchbase`), name, config (non-secret), secret {keyId, wrappedDek, dekNonce, ciphertext, nonce} (base64), lastTestStatus, lastTestedAt, createdAt, updatedAt. The deterministic key gives **one source and one target per project** |
| `discovery_snapshots` | `<snapshotId>`, plus head `latest::<connectionId>` (IDs newest first) | connectionId, workspaceId, capturedAt, durationMs, payload (§6.1), isLatest |
| `mappings` | `<mappingId>` | projectId, workspaceId, source/target refs, spec (§6.2), automapStats. **CAS** is the optimistic lock (sent to clients as the ETag) |
| `jobs` | `<jobId>` | projectId, workspaceId, status (§6.3), options, mappingIds, checkpoint, claimedBy, heartbeatAt, errorSummary, createdBy, createdAt, startedAt, finishedAt |
| `job_progress` | `<jobId>` | the latest progress snapshot (OC-027), written every second by the owning worker |
| `job_errors` | `<jobId>::<seq>` | jobId, mappingId, sourceKey, stage, code, message, sample (≤ 4 KB). Expiry = the error retention period |
| `job_reports` | `<jobId>` | report |
| `audit` | `<eventId>` | workspaceId, userId, action, entityType, entityId, metadata, createdAt. Expiry = the audit retention period |
| `leases` | `lease::<name>` | holder, expiry (leader election for background chores) |
| `rate_limits` | `rl::<scope>::<key>` | counter documents with expiry (login limiter across nodes) |
| `system` | `schema` | the provisioning version applied (OC-003) |

**Guarantees and how they're achieved:**
- **Multi-document atomicity:** sign-up (user, email lock, workspace, membership, session), project create (project and name lock), and project delete (project, connections, name lock) run in Couchbase ACID transactions (`cluster.Transactions().Run`).
- **Uniqueness:** `Insert` of a `uniques` key fails with `ErrDocumentExists` → `409`.
- **Optimistic concurrency:** CAS from the last read. A mismatch → `412`.
- **Tenant isolation:** KV reads verify `doc.workspaceId == principal.workspaceId` (otherwise `404`), and every SQL++ list query filters on `workspaceId` (enforced by a lint test, OC-003).
- **Lists** use SQL++ with `request_plus` scan consistency (read-your-own-writes) over GSI indexes such as `(workspaceId, createdAt DESC)`.

IDs are UUID v7, which sort by time and index well.

**Connections are owned by projects.** A connection always belongs to exactly one project, keyed `<projectId>::<role>`, so each project has at most one source and one target. There is no workspace-level connection library in 1.0. Credentials are entered once per project and reused by all of its mappings and jobs, and deleting the project deletes its connections and their sealed secrets.

---

## 6. Canonical models (shared JSON contracts)

### 6.1 Discovery snapshot (`discovery.Snapshot`)

```json
{
  "database": "shop", "serverVersion": "16.3",
  "schemas": [{
    "name": "public",
    "tables": [{
      "name": "customer", "kind": "table",            // table | view | materialized_view | partitioned_table
      "rowEstimate": 120345, "sizeBytes": 30932992, "comment": null,
      "columns": [{
        "name": "customer_id", "ordinal": 1, "dataType": "integer", "udtName": "int4",
        "nullable": false, "default": "nextval('customer_customer_id_seq')",
        "isPrimaryKey": true, "isUnique": true, "maxLength": null, "numericPrecision": 32,
        "numericScale": 0, "isArray": false, "enumValues": null, "comment": null,
        "jsonType": "integer"                          // normalized logical type, see §6.4
      }],
      "primaryKey": ["customer_id"],
      "uniqueConstraints": [["email"]],
      "foreignKeys": [{ "name": "fk_addr", "columns": ["address_id"],
                        "refSchema": "public", "refTable": "address", "refColumns": ["address_id"] }],
      "referencedBy": [{ "schema": "public", "table": "orders", "columns": ["customer_id"] }],
      "indexes": [{ "name": "customer_pkey", "columns": ["customer_id"], "unique": true }]
    }]
  }]
}
```

### 6.2 Mapping spec (`mapping.Spec`, stored in `mappings.spec`)

```json
{
  "docSchema": {
    "root": { "id": "fld_root", "name": "$", "type": "object", "children": [
      { "id": "fld_01", "name": "customerId", "type": "integer", "required": true },
      { "id": "fld_02", "name": "name", "type": "object", "children": [
          { "id": "fld_03", "name": "first", "type": "string" },
          { "id": "fld_04", "name": "last",  "type": "string" } ] },
      { "id": "fld_05", "name": "tags", "type": "array", "items": { "id": "fld_06", "name": "[]", "type": "string" } }
    ]},
    "source": "generated"                     // generated | json-sample | json-schema | collection-sample | manual
  },
  "key": { "template": "customer::{customer_id}" },
  "typeField": { "enabled": true, "name": "type", "value": "customer" },
  "nullHandling": "include",                  // include | omit
  "fieldMappings": [{
    "id": "map_01", "targetFieldId": "fld_03",
    "sources": [{ "column": "first_name" }],
    "transforms": [{ "op": "trim" }],
    "origin": "auto",                         // auto | suggested | manual
    "state": "active",                        // active | pending (suggestion awaiting accept) | rejected
    "confidence": 0.97,
    "reason": "normalized-name-match"
  }],
  "embeds": [],                               // see OC-021
  "ignoredColumns": ["legacy_flag"]
}
```

A field's **path** is derived, not stored: `name.first`, `tags[]`, `orders[].sku`.

**Target field types** (`docschema.FieldType`): `string`, `integer`, `number`, `boolean`, `datetime` (written as an ISO-8601 string by default), `object`, `array`, `any`.

### 6.3 Job state machine

```
queued ──claim──▶ running ──▶ completed
   │                │  ├──▶ completed_with_errors
   │                │  ├──▶ failed
   │                │  ├──pause──▶ pausing ──▶ paused ──resume──▶ queued
   │                │  └──cancel─▶ cancelling ─▶ cancelled
   └──cancel──▶ cancelled
running (stale heartbeat > 30s) ──reclaim──▶ running (new worker, from checkpoint)
```

### 6.4 Logical type normalization (PostgreSQL → `jsonType`)

| PostgreSQL | jsonType | Default JSON encoding |
|---|---|---|
| smallint, integer | integer | number |
| bigint | integer | number (warning if \|v\| > 2^53−1; option: string) |
| numeric/decimal | number | number when precision ≤ 15, otherwise string (configurable) |
| real, double precision | number | number (NaN/Inf → null + warning) |
| boolean | boolean | true/false |
| text, varchar, char, citext, name | string | string (char is right-trimmed) |
| uuid | string | canonical lowercase string |
| date | datetime | `YYYY-MM-DD` |
| timestamp | datetime | ISO-8601 without zone (option: assume UTC) |
| timestamptz | datetime | ISO-8601 UTC `Z` |
| time, timetz | string | `HH:MM:SS[.ffffff][±hh:mm]` |
| interval | string | ISO-8601 duration |
| json, jsonb | any | embedded JSON value |
| bytea | string | base64 |
| enum | string | label |
| arrays (`_int4`, …) | array | JSON array of the element encoding |
| inet, cidr, macaddr | string | text form |
| money | number | numeric value (locale-free) |
| xml, tsvector, geometry, other | string | text form + "unsupported type, sent as text" warning |

---

## 7. API conventions

- The base path is `/api/v1`. Bodies are JSON with camelCase keys.
- Errors use **RFC 9457** `application/problem+json`: `{ type, title, status, detail, code, instance, fieldErrors?: [{field, message}] }`. `code` is a stable machine string such as `CONNECTION_AUTH_FAILED`.
- Authentication uses an HttpOnly, Secure, `SameSite=Lax` session cookie `oc_session`. Every mutating request must send the `X-CSRF-Token` header.
- Writes that can conflict (mappings) use optimistic concurrency: `If-Match: "<version>"`, and a mismatch returns `412`.
- Lists use `?limit=&cursor=` and return `{ items, nextCursor }`.
- Secrets are write-only. Responses never echo them, only flags such as `passwordSet: true`.
- Connections are created and replaced under their project (`PUT /projects/{projectId}/connections/{role}`). Their sub-resources (test, discover, schema, preview, buckets, …) are addressed as `/connections/{connectionId}/…`, and authorization resolves connection → project → workspace.
- Every request carries `X-Request-ID`, which is generated if absent and echoed in the response and in logs.

---

## 8. Frontend UX principles (see OC-004)

- **Guided flow and free navigation together.** A project has a stepper (Source → Target → Tables → Design & Map → Review → Migrate), and every step can also be opened directly.
- **Autosave.** Designer and mapping edits autosave, debounced at 800 ms, with a visible "Saved · 2s ago" indicator.
- **Every async surface has four states:** loading (skeleton), empty (illustration + CTA), error (problem detail + retry), and success.
- **Accessibility:** WCAG 2.1 AA. The mapping canvas has a keyboard-accessible **table view** alternative.
- **Light and dark themes**, persisted per user.
- **Responsive:** fully usable at ≥ 1280 px. The canvas and designer degrade to the table view below 1024 px.

---

## 9. Non-functional requirements (release gate)

| NFR | Target (validated by OC-034 benchmark) |
|---|---|
| Migration throughput | ≥ 20,000 docs/s sustained for a 1 KB doc, 20-column table, 1 worker (8 vCPU), same-region Couchbase 3-node cluster |
| Memory | Worker RSS < 512 MB with default settings, regardless of table size |
| Progress latency | UI reflects a counter change within ≤ 2 s |
| Auto-map | < 50 ms for 500 source columns × 500 target fields |
| Discovery | < 10 s for a database with 1,000 tables / 20,000 columns |
| API p95 | < 200 ms for CRUD endpoints |
| Availability of jobs | Worker crash → job resumes on another worker within 60 s with no data loss (at-least-once, idempotent upsert) |
| Security | OWASP ASVS L2 controls for auth/session; no secret in logs or API responses |

---

## 10. Testing strategy

| Level | Tooling | Scope |
|---|---|---|
| Unit (Go) | `go test -race`, testify, table-driven, fakes for ports | Every AC in every story has at least one named unit test |
| Unit (Web) | Vitest + RTL + MSW | Components, hooks, stores, reducers |
| Integration (Go) | testcontainers-go: `couchbase/server:enterprise` (metadata store and migration target), `postgres:16` (source only) | Metadata repositories, connectors, discovery, pipeline end-to-end |
| Contract | OpenAPI lint (`redocly lint`) + generated-type compile check | API drift |
| E2E | Playwright against docker-compose stack | Golden-path migration, pause/resume, error flows |
| Performance | `go test -bench` + OC-034 harness | Throughput / memory gates |

- Coverage gates: backend `internal/...` ≥ 80% lines, and ≥ 90% for `automap`, `transform`, and `pipeline`; web ≥ 75%.
- Test names follow `Test<Unit>_<AC-id>_<behavior>` (Go) and `describe('<Component>') / it('AC<n>: <behavior>')` (TS), so a failed test can be traced back to its story and AC.

---

## 11. Global Definition of Done

A story is done when all of the following hold:
- [ ] All ACs are implemented and their named unit tests pass in CI (`make test`).
- [ ] Coverage gates hold, and `go vet`, `staticcheck`, `golangci-lint`, `eslint`, `tsc --noEmit`, and `prettier --check` all pass.
- [ ] The OpenAPI spec is updated, clients are regenerated, and there is no diff.
- [ ] The UI matches the design system (OC-004), with all four async states and dark mode, and passes an axe accessibility check.
- [ ] No secrets appear in logs (asserted by the log-redaction test helper).
- [ ] Every user-visible string is in the i18n catalog (`web/src/i18n/en.json`). 1.0 ships English only.
- [ ] The docs are updated (README, or a user guide section when UI changes).
- [ ] The code is reviewed, merged to `main`, and deployed to the staging compose stack.
