<p align="center">
  <img src="web/public/favicon.svg" width="72" height="72" alt="Omni-Data-Bus logo">
</p>

<h1 align="center">Omni-Data-Bus</h1>

<p align="center">
  Move relational data into Couchbase — <b>mapped, streamed, verified</b>.
</p>

---

Omni-Data-Bus is a web application for migrating data from relational databases into Couchbase.
You create a **project**, connect a **source** (PostgreSQL in 1.0) and a **target** (Couchbase
Capella or self-managed Couchbase), pick tables, design the JSON documents you want, let the
auto-mapper match fields ("*10 of 23 fields automatically mapped*"), finish the rest on a
drag-and-drop canvas, and press **Migrate**. Rows are streamed, transformed and written to
Couchbase with a live progress bar, and can be paused, resumed and verified.

> **Project status: release 1.0.0 is in development.** The build is proceeding story by story;
> live progress is tracked in [`docs/release-1.0.0/INDEX.md`](docs/release-1.0.0/INDEX.md)
> (implemented stories are ~~struck through~~). Sections below marked **(planned)** describe
> behaviour that is specified but not built yet.

## Contents

- [Features](#features)
- [How it works](#how-it-works)
- [Architecture](#architecture)
- [Technology stack](#technology-stack)
- [Repository layout](#repository-layout)
- [Prerequisites](#prerequisites)
- [Quick start (local development)](#quick-start-local-development)
- [The local dev stack](#the-local-dev-stack)
- [Configuration reference](#configuration-reference)
- [Omni-Data-Bus's own data: Couchbase metadata store](#omni-data-buss-own-data-couchbase-metadata-store)
- [Using the application](#using-the-application)
- [Security model](#security-model)
- [Testing](#testing)
- [Make targets](#make-targets)
- [Building and deploying](#building-and-deploying)
  - [All-in-one image (app + Couchbase CE)](#all-in-one-image-app--couchbase-ce)
  - [Running an exported migration (`migrate.sh`, `odb run`)](#running-an-exported-migration-migratesh-odb-run)
- [Working on a story (contributor workflow)](#working-on-a-story-contributor-workflow)
- [Troubleshooting](#troubleshooting)
- [Documentation map](#documentation-map)

## Features

| Area | What you get | Stories |
|---|---|---|
| Projects | Everything lives inside a project; each project owns exactly one source and one target | OC-007 |
| Source repository | PostgreSQL connection form, **Test connection** with a step-by-step checklist (DNS, TLS, auth, version, read privileges with a ready-to-copy `GRANT` fix) | OC-008, OC-009 |
| Target repository | Couchbase Capella or self-managed, **Test connection**, permission/capability probe, service latencies | OC-012 |
| Credentials | Entered and tested **once per project**, encrypted at rest (AES-256-GCM envelope encryption), never shown again, reused by every run | OC-006 |
| Discovery | Schemas, tables, views, columns, keys, indexes, row estimates; searchable explorer with data preview | OC-010, OC-011 |
| Keyspaces | Browse and create buckets, scopes and collections | OC-013, OC-014 |
| Document design | Generate from a table, drop a JSON sample or JSON Schema, sample existing documents, or build by hand | OC-015 |
| Auto-mapping | Deterministic, explainable matcher with a coverage score | OC-017, OC-018 |
| Visual mapping | Drag-and-drop canvas plus an accessible table view; transformations; embedding related tables | OC-019 – OC-021 |
| Preview | Real documents from sample rows before migrating | OC-022 |
| Migration | Snapshot-consistent parallel streaming, batching, retries, dead-letter log, pause/resume, crash recovery | OC-023 – OC-029 |
| Live progress | Progress bar, throughput, ETA, per-table progress, live error feed | OC-027, OC-028 |
| Verification | Count reconciliation, sampled document comparison, downloadable report | OC-030 |
| Portable runner | Export the project as configured as a `migrate.sh` script (or a bundle) that runs anywhere without the tool, its saved credentials sealed with a passphrase; import the run's report back | OC-050 – OC-055 |

## How it works

```
 1. Create project ─▶ 2. Source repo (PostgreSQL) ─▶ 3. Target repo (Couchbase)
        │                  test ✓ · save (encrypted)       test ✓ · save (encrypted)
        ▼
 4. Select tables ─▶ 5. Design document + auto-map ─▶ 6. Map the rest ─▶ 7. Preview
        ▼
 8. Migrate: read (PostgreSQL snapshot) ─▶ transform ─▶ write (Couchbase KV)
        │         live progress bar · pause · resume · errors
        ▼
 9. Verify & download report
```

## Architecture

```
┌──────────────────────── Browser (React SPA) ─────────────────────────┐
│ Projects │ Repositories │ Schema explorer │ Mapping │ Job monitor    │
└──────────────┬──────────────────────────────────────────▲────────────┘
               │ REST /api/v1 (JSON, problem+json errors)  │ SSE (progress)
┌──────────────▼──────────────────────────────────────────┴────────────┐
│ odb (single Go binary; roles: api | worker | all)                    │
│  auth · projects · connections · discovery · mapping · jobs           │
│  worker: Reader(s) ─▶ Transformer pool ─▶ Writer pool ─▶ Checkpoints   │
└──────┬─────────────────────────┬────────────────────────┬────────────┘
       │                         │                        │
 Metadata store            Source database          Target database
 Couchbase Capella         PostgreSQL               Couchbase
 (bucket "omniconnect")    (read-only)              (Capella or self-managed)
```

* The server is **stateless**; all state lives in the Couchbase metadata bucket, so you can run
  several replicas behind a load balancer.
* One binary, three roles: `--role=api`, `--role=worker`, or `--role=all` (default).
* Full design, domain model, API conventions and NFRs:
  [`docs/release-1.0.0/00-architecture.md`](docs/release-1.0.0/00-architecture.md).

## Technology stack

| Layer | Choice | Why |
|---|---|---|
| Backend | **Go 1.26** (toolchain pinned in `backend/go.mod`) | Goroutines + bounded channels give a simple, fast streaming pipeline with natural back-pressure; first-class drivers for both databases; single static binary |
| HTTP | `net/http` + `go-chi/chi` | Small, idiomatic, composable middleware |
| Metadata store | **Couchbase Capella** (Couchbase Server **8.0.3** locally) via `gocb/v2` | KV for documents, SQL++ for lists, ACID transactions, CAS for optimistic locking, TTL for sessions |
| Source driver | `jackc/pgx/v5` | Fastest PostgreSQL driver; snapshot export for consistent parallel reads |
| Frontend | React 19, TypeScript 5.9 (strict), Vite 8 | Fast dev loop, type safety |
| UI | Tailwind CSS 4, Radix primitives, lucide icons, Geist / Geist Mono fonts | Professional, accessible, light and dark themes |
| Data fetching / forms | TanStack Query, react-hook-form + zod, openapi-fetch (types generated from OpenAPI) | Typed end-to-end |
| Visual mapping | React Flow (`@xyflow/react`) (planned) | Drag-and-drop canvas |
| Tests | Go `testing` + testify + testcontainers; Vitest + Testing Library + MSW; Playwright (planned) | Every acceptance criterion has named tests |

## Repository layout

```
omni-connect/
├── api/openapi.yaml            API contract (source of truth for TypeScript types)
├── backend/                    Go module "omnidatabus"
│   ├── cmd/odb/       main: server + admin subcommands
│   ├── internal/
│   │   ├── app/                wiring and graceful HTTP server
│   │   ├── config/             environment configuration (OC_*)
│   │   ├── httpapi/            router, middleware, health, SPA serving
│   │   ├── observability/      structured logging with secret redaction
│   │   ├── problem/            RFC 9457 problem+json errors and error codes
│   │   ├── store/              Couchbase metadata store (provisioning, repositories)
│   │   ├── auth/ crypto/ projects/ connections/ connectors/ …   (added by later stories)
│   └── tools/                  quality gates: coverage gate, generated-code check
├── web/                        React app (Vite)
│   ├── public/favicon.svg      logo / favicon
│   └── src/                    app shell, features, components, API client
├── deploy/compose/dev.yml      local stack: Couchbase 8.0.3 + sample PostgreSQL source
├── docs/release-1.0.0/         architecture + 34 user stories + INDEX.md (progress)
├── scripts/                    dev.sh, update-story-index.py
└── Makefile                    all developer commands (`make help`)
```

## Prerequisites

| Tool | Version | Notes |
|---|---|---|
| Couchbase Capella cluster | — | Omni-Data-Bus's metadata store (see below). Not needed if you use the Docker dev stack instead |
| Docker Desktop *(optional)* | recent | Only for the offline dev stack: Couchbase 8.0.3 + sample PostgreSQL source. Give it **≥ 4 GB RAM** |
| Go | 1.21 or newer installed | `go.mod` pins **go1.26.8**; Go downloads that toolchain automatically on first use (`GOTOOLCHAIN=auto`) |
| Node.js | 20+ (tested with 24) | npm is used (no pnpm needed) |
| make, git, python3 | any recent | `python3` only for `make index` |

Ports used locally: **8080** (API), **5173** (web), **8091–8097, 11210** etc. (Couchbase), **5434** (sample PostgreSQL).

## Quick start (local development)

**With your Capella cluster (no Docker needed):**

```bash
git clone <repo-url> omni-connect && cd omni-connect
cp .env.example .env   # fill in OC_META_* for Capella and OC_MASTER_KEY (see below)
make setup             # Go modules, golangci-lint, web dependencies
make dev               # API on :8080 + web on :5173
```

**Fully offline (Docker):** skip the `.env` step. `make dev` then starts the Docker dev stack
(Couchbase 8.0.3 for the metadata store + a sample PostgreSQL source) before the API and web app.

Then open **http://localhost:5173** and create an account.

What `make dev` does (see [`scripts/dev.sh`](scripts/dev.sh)):
1. If `.env` points the metadata store at a remote cluster (Capella), it skips Docker. Otherwise it runs
   `make dev-stack` (containers + the one-shot `couchbase-init` job) and uses `OC_DEV=true`.
2. Starts the Go API (it reads `.env` itself, connects to the metadata store and provisions it on first start).
3. Starts the Vite dev server (it proxies `/api` to `:8080`).

The Docker stack is still handy alongside Capella: `make dev-stack` gives you the sample PostgreSQL
source (`localhost:5434`) and a local Couchbase target to migrate into. Stop the app with `Ctrl+C`,
the containers with `make dev-stack-down` (`make dev-reset` also deletes their data).

Useful URLs while developing:

| URL | What |
|---|---|
| http://localhost:5173 | The web app |
| http://localhost:8080/healthz | Liveness |
| http://localhost:8080/readyz | Readiness (checks the metadata bucket) |
| http://localhost:8080/api/v1/version | Build version |
| http://localhost:8091 | Couchbase Web Console (dev login `Administrator` / `password`) |

## The local dev stack

Defined in [`deploy/compose/dev.yml`](deploy/compose/dev.yml):

| Service | Image | Purpose |
|---|---|---|
| `couchbase` | `couchbase/server:enterprise-8.0.3` | Hosts **`omniconnect`** (Omni-Data-Bus's own metadata — stands in for your Capella bucket) and **`sandbox`** (a migration target to experiment with) |
| `couchbase-init` | same | Idempotent one-shot: `cluster-init` (data, index, query), creates both buckets |
| `source-pg` | `postgres:16` on **localhost:5434** | Sample **source** database `shop` to migrate *from* |

Sample source database (seeded from [`deploy/compose/seed/source`](deploy/compose/seed/source)):

* Schema `shop`: `customer`, `address`, `product`, `category`, `orders`, `"Order Details"`,
  `page_view` (no primary key), `big_events` (1,000,000 rows) — together they cover every
  PostgreSQL type Omni-Data-Bus converts.
* Schema `billing`: a few tables the read-only role **cannot** read, to demonstrate the
  privilege warning in *Test connection*.
* Logins: owner `shop_owner` / `shop_owner`; **read-only role for Omni-Data-Bus `omni_ro` / `omni_ro`**.

To use the sandbox target in the app: connection string `couchbase://localhost`,
user `Administrator`, password `password`, deployment *Self-managed*.

> Dev credentials above exist only in the local containers. Never reuse them anywhere else.

## Configuration reference

Configuration comes from environment variables, usually through a **`.env` file**:

```bash
cp .env.example .env     # then edit .env
```

* `.env` is git-ignored. The server reads it itself from the repo root or `backend/` (override the path with `OC_ENV_FILE`). Real environment variables win over the file.
* Values are taken literally, so passwords containing `$`, `` ` ``, `#` or quotes work without escaping.
* Without a `.env`, `make dev` runs with `OC_DEV=true` against the **local** dev stack.
* **Dev defaults never apply to a remote cluster.** If `OC_META_CONNSTR` points at Capella, the username, password and `OC_MASTER_KEY` must be set; the server refuses to start with the published development key.

### Using your Couchbase Capella cluster for the metadata store

1. In Capella, create the bucket `omniconnect` (or your `OC_META_BUCKET`) and a database credential with Read/Write on it.
2. Add your machine's public IP to the cluster's **Allowed IP addresses**.
3. In `.env`, set `OC_META_CONNSTR` (the `couchbases://…` string from Capella → Connect), `OC_META_USERNAME`, `OC_META_PASSWORD`, and `OC_META_CAPELLA=true`, and a master key from `openssl rand -base64 32`.
4. `make run-backend` (or `make dev`). On first start Omni-Data-Bus creates scope `meta`, its collections and indexes; `/readyz` should report `"metadata":"ok"`.

| Variable | Default | Description |
|---|---|---|
| `OC_DEV` | `false` | Development defaults (local Couchbase, dev master key, non-secure cookies). Never set in production |
| `OC_ROLE` | `all` | `api`, `worker` or `all` |
| `OC_HTTP_ADDR` | `:8080` | Listen address |
| `OC_PUBLIC_URL` | `http://localhost:5173` | Public origin of the web app (CSRF origin check, links) |
| `OC_LOG_LEVEL` / `OC_LOG_FORMAT` | `info` / `json` | `debug·info·warn·error` / `json·text` |
| `OC_META_CONNSTR` | *(required)* | Metadata cluster, e.g. `couchbases://cb.xxxx.cloud.couchbase.com` |
| `OC_META_USERNAME` / `OC_META_PASSWORD` | *(required)* | Capella database credential with read/write on the metadata bucket |
| `OC_META_BUCKET` | `omniconnect` | Metadata bucket (must exist on Capella) |
| `OC_META_SCOPE` | `meta` | Scope inside the bucket (created automatically when permitted) |
| `OC_META_CAPELLA` | `false` | `true` applies the Capella/WAN connection profile |
| `OC_META_TLS_SKIP_VERIFY` | `false` | Dev only |
| `OC_META_AUTO_PROVISION` | `true` | Create collections/indexes at startup |
| `OC_MASTER_KEY` | *(required)* | Base64 32-byte key that encrypts stored credentials. Generate with `odb admin gen-key` (planned, OC-006) or `openssl rand -base64 32` |
| `OC_MASTER_KEYS` / `OC_MASTER_KEY_ACTIVE` | — | Key rotation: `k2:<b64>,k1:<b64>` plus the active id |
| `OC_SIGNUP_ENABLED` | `true` | Allow self sign-up |
| `OC_SESSION_IDLE_TTL` / `OC_SESSION_ABSOLUTE_TTL` | `12h` / `168h` | Session lifetimes |
| `OC_SESSION_COOKIE_SECURE` | `true` | Forced `false` in dev (plain http) |
| `OC_SHUTDOWN_TIMEOUT` | `30s` | Graceful shutdown budget |
| `OC_METRICS_ADDR` | — | Serve Prometheus `/metrics` on its own listener (e.g. `:9090`); empty = on the public port |
| `OC_PPROF_ADDR` | — | Serve Go `net/http/pprof` on its own listener (e.g. `127.0.0.1:6060`) for benchmark profiling; never expose it publicly |
| `OC_OTEL_EXPORTER_OTLP_ENDPOINT` | — | OTLP/HTTP collector URL (e.g. `http://otel-collector:4318`); empty = tracing off |
| `OC_OTEL_SAMPLE_RATIO` | `0.1` | Parent-based trace ID ratio sampling |
| `OC_ALLOW_PRIVATE_NETWORKS` | `true` | Allow connections to private/loopback addresses (set `false` for a multi-tenant service) |
| `OC_EGRESS_ALLOW_CIDRS` / `OC_EGRESS_DENY_CIDRS` | — | Extra allowed / refused ranges, comma-separated (deny wins) |
| `OC_EGRESS_PORTS` | PostgreSQL, Couchbase, 443 | Allowed destination ports, e.g. `5432-5439,6432,11207,18091-18097,443` |
| `OC_AUDIT_RETENTION_DAYS` | `365` | How long audit events are kept |

## Omni-Data-Bus's own data: Couchbase metadata store

Omni-Data-Bus stores **its own data** (users, sessions, projects, encrypted connection
credentials, mappings, jobs) in a Couchbase bucket — **Couchbase Capella in production**,
Couchbase Server 8.0.3 in the local dev stack. There is no PostgreSQL in Omni-Data-Bus's own
runtime; PostgreSQL only appears as a *source* you migrate from.

Setting up Capella for production:
1. In Capella, create a bucket (default name `omniconnect`) — the SDK cannot create buckets on Capella.
2. Create a database credential with read/write access to that bucket.
3. Add the Omni-Data-Bus server's egress IP to **Allowed IPs**.
4. Set `OC_META_CONNSTR`, `OC_META_USERNAME`, `OC_META_PASSWORD`, `OC_META_CAPELLA=true`.
5. On startup Omni-Data-Bus **provisions** scope `meta`, its collections and indexes. If the
   credential lacks permission, it prints the exact scopes/collections/index statements to
   create in the Capella UI (planned: `odb admin provision --print`).

How relational-style guarantees are achieved on Couchbase: ACID transactions for multi-document
writes, "uniqueness" documents (e.g. `email::<address>`) inserted in the same transaction,
CAS for optimistic concurrency, document expiry for sessions and retention. Details:
[architecture §5](docs/release-1.0.0/00-architecture.md) and
[OC-003](docs/release-1.0.0/OC-003-metadata-store-couchbase.md).

## Using the application

The UI walks you through a project step by step (see the design preview linked from the docs).

1. **Sign up / sign in.** The first sign-up creates your personal workspace.
2. **Create a project** — Projects → *New project* (name + optional description).
3. **Source repository** — choose PostgreSQL, fill host/port/database/user/password and SSL
   mode (or *Paste connection string*), click **Test connection**, then **Save to project**.
   The password is encrypted and never shown again; re-testing later uses the stored secret.
   *Local example:* host `localhost`, port `5434`, database `shop`, user `omni_ro`, password
   `omni_ro`, SSL mode `disable` — you will see a warning about schema `billing`, by design.
4. **Target repository** — choose *Self-managed* or *Capella*, enter the connection string and
   credentials, **Test connection**, **Save to project**. The card shows what the credential
   may do (write documents, manage scopes/collections, create buckets).
5. **Explore the source schema** — *Explore schema →* on the saved source card opens the schema
   explorer (`/projects/<id>/source/schema`). The first visit discovers the schema (schemas,
   tables, views, columns, keys, indexes, row estimates); **Refresh** re-discovers and lists what
   changed. Search tables and columns (`cust email`), filter by *Tables / Views / Has PK / No PK*,
   navigate with the arrow keys, and open a table's **Columns**, **Relationships**, **Indexes**
   and **Preview** (sample rows, encoded exactly as the migration will write them). Tables the
   source user can't read show a lock.
6. **Browse or create target keyspaces** — *Browse keyspaces →* on the saved target card
   (`/projects/<id>/target/keyspaces`): pick bucket → scope → collection, see the document count,
   and create a bucket (self-managed only; Capella buckets are created in the Capella UI), scope
   or collection when the credential is allowed to.
7. **Tables → Design & map → Review → Migrate** *(Phase 2 and 3, see INDEX.md)* — pick tables and
   a target collection per table, review the auto-mapping score, map remaining fields on the
   canvas, preview documents, then press **Migrate** and watch the progress bar.

8. **Export** *(optional)* — Review → *Export…* opens the Export page; *New export* builds a `migrate.sh` that
   runs the migration on another machine without this tool. It exports the project exactly as
   configured — source, target, saved credentials and every ready mapping, shown read-only with a
   link to each step to change them — and asks only for a passphrase (which seals the credentials)
   and how the script runs. Afterwards *Import report* adds the run's `report.json` to the project's runs. See [Running an exported migration](#running-an-exported-migration-migratesh-odb-run).

Roles: **owner** and **editor** can change things; **viewer** is read-only.

## Security model

* **Credentials at rest:** envelope encryption — a random data key per secret (AES-256-GCM),
  wrapped by the master key; ciphertext is bound to its workspace/project/connection so it
  cannot be copied between records. Supports key rotation.
* **Never echoed:** the API returns `passwordSet: true`, never the secret; logs redact
  passwords, tokens, keys and `user:password@` in URLs.
* **Sessions:** HttpOnly cookie, server-side session documents with expiry, CSRF token on
  every mutating request, login rate limiting, argon2id password hashing.
* **Tenant isolation:** every read checks the workspace; cross-workspace IDs return 404.
* **Least privilege:** use a read-only PostgreSQL role for sources; *Test connection* warns
  about superuser or write-capable roles.
* **Egress guard:** database hosts users type in are checked at dial time: cloud metadata and
  link-local addresses are always refused, and private networks (loopback, RFC 1918, CGNAT, ULA)
  only when `OC_ALLOW_PRIVATE_NETWORKS=true` (the self-hosted default; set `false` for a shared
  service). PostgreSQL connections resolve once and dial only the checked IPs, which defeats DNS
  rebinding. The Couchbase SDK can't take a custom dialer, so its bootstrap hosts are checked
  before connecting and every node the cluster reports is checked afterwards. Pair it with a
  NetworkPolicy or egress firewall (the Helm chart has one).
* **Headers:** a strict Content-Security-Policy (`script-src 'self'`, no inline scripts), HSTS
  when `OC_PUBLIC_URL` is https, `nosniff`, `frame-ancestors 'none'`, `Permissions-Policy`, COOP.
* **Limits:** 1 MB request bodies (6 MB for JSON samples and connection settings); 20 requests/s
  per session (burst 40) and 5/s per IP when signed out; at most 2 discoveries and 4 previews
  at once per workspace, 3 connection tests per 10 s and 10 live streams per session.
* **Audit log:** sign-ins, projects, repositories, keyspaces, mappings (updates at most once per
  10 minutes each), migrations and report downloads, kept `OC_AUDIT_RETENTION_DAYS`
  (365). Owners see it in *Settings → Audit log*.
* **Scanning:** govulncheck, npm audit, gosec (golangci-lint), CodeQL and Trivy run in CI.

## Testing

Every story's acceptance criteria (ACs) map to **named tests** — Go tests are named
`Test<Unit>_AC<n>_<Behaviour>`, web tests `it('AC<n>: …')` — so a failure points back to the story.

```bash
make test               # all unit tests (Go with -race + coverage gate, web with coverage)
make test-backend       # Go unit tests only
make test-web           # web unit tests only
make test-integration   # Go integration tests — needs Docker (Couchbase 8.0.3, PostgreSQL via testcontainers)
make lint               # golangci-lint, eslint, tsc
```

Coverage gates: Go packages ≥ 80% (≥ 90% for automap/transform/pipeline), web ≥ 75% lines.

## Make targets

| Target | Description |
|---|---|
| `make help` | List all targets |
| `make setup` | Install Go modules, golangci-lint, web dependencies |
| `make dev` | Dev stack + API + web (one command) |
| `make dev-stack` / `dev-stack-down` / `dev-reset` | Start / stop / wipe the containers |
| `make run-backend` / `run-web` | Run one side only |
| `make test`, `test-backend`, `test-web`, `test-integration` | Tests |
| `make test-integration-ce` | Integration tests against Couchbase Server Community Edition (as bundled in the all-in-one image) |
| `make appliance` / `appliance-save` / `appliance-push` / `appliance-test` | Build, package, push and test the [all-in-one image](#all-in-one-image-app--couchbase-ce) |
| `make lint` | Linters and type checks |
| `make gen` / `check-gen` | Regenerate TypeScript API types / fail if stale |
| `make build` | Build the web app and a single binary with the web app embedded (`backend/bin/odb`) |
| `make dist` | Build the `odb` runner for linux/darwin × amd64/arm64 with `SHA256SUMS` (`dist/odb/`) |
| `make test-scripts` | `migrate.sh` checks: shellcheck on every variant and the bats suites (`ODB_TEST_BASH=/bin/bash` for bash 3.2) |
| `make index` | Regenerate `docs/release-1.0.0/INDEX.md` from story statuses |

## Building and deploying

* **Single binary:** `make build` produces `backend/bin/odb` with the web app embedded;
  run it with the production environment variables above and it serves both UI and API on `:8080`.
* **Container image:** `make image` builds `deploy/docker/Dockerfile`: a distroless, non-root
  (uid 65532) image of about 38 MB holding only `odb` and CA certificates; `make image-test` checks
  it. Releases (`v*` tags) publish it multi-arch (amd64/arm64) to GHCR, signed with cosign, with
  an SPDX SBOM attached and a Trivy gate on HIGH/CRITICAL vulnerabilities.
* **Docker Compose (single VM):** `deploy/compose/prod.yml`, pointed at Capella, with an optional
  Caddy HTTPS proxy. See [deploy/compose/README.md](deploy/compose/README.md) for the first run,
  upgrades, backup and key rotation.
* **Kubernetes:** the Helm chart in `deploy/helm/omni-data-bus` (separate api and worker
  Deployments, or one; migration hook; hardened pods; HPA/KEDA; NetworkPolicy). See its
  [README](deploy/helm/omni-data-bus/README.md); `make helm-test` lints and unit-tests it.
* Secrets can be given as files: `OC_MASTER_KEY_FILE`, `OC_MASTER_KEYS_FILE`,
  `OC_META_PASSWORD_FILE`. `odb healthcheck` probes `/readyz` (for container healthchecks).
* Put a TLS-terminating proxy in front and set `OC_PUBLIC_URL` to the public https origin.
* **Monitoring:** scrape `/metrics` (all `oc_*` metrics plus Go runtime and process metrics; use
  `OC_METRICS_ADDR` to keep it off the public port). Import `deploy/observability/dashboard.json`
  into Grafana and load `deploy/observability/alerts.yml` into Prometheus. With tracing on, the
  request ID in an error (the problem `instance`) is the trace ID, and every log line carries
  `trace_id`.

### All-in-one image (app + Couchbase CE)

For handing the tool to a customer who has no Couchbase cluster: **one Docker image** holds Omni-Data-Bus and a single-node **Couchbase Server Community Edition** that stores the app's data and is offered as a migration target (*Use the bundled Couchbase* on the Target step). The customer loads one file and runs one command; everything lives in one Docker volume. Built from [`deploy/appliance/`](deploy/appliance/).

| Use | When |
|---|---|
| All-in-one image | A customer (or a laptop) without Couchbase; evaluations; one machine, one team |
| Regular image + Capella / your cluster | Production with your own Couchbase; several API or worker nodes ([Docker Compose](deploy/compose/README.md)) |
| Helm chart | Kubernetes ([chart README](deploy/helm/omni-data-bus/README.md)) |

**1. Prerequisites.** Docker with buildx (Docker Desktop includes it). To build for the other CPU (an amd64 image on an Apple-silicon Mac, or the reverse), buildx needs QEMU: Docker Desktop has it; on Linux run `docker run --privileged --rm tonistiigi/binfmt --install all` once. The target machine needs 4 GB of memory for Docker.

**2. Build** for this machine, or for the customer's CPU (most servers are `amd64`):

<!-- docs-test -->
```bash
make appliance                 # this machine's CPU
make appliance ARCH=amd64      # Intel/AMD servers
```

It builds the regular image for that platform, then `omni-data-bus-appliance:<version>` on top of `couchbase/server:community-8.0.2` (pinned by digest) with the same `odb` binary.

**3. Try it:**

```bash
ODB_IMAGE=omni-data-bus-appliance:<version> deploy/appliance/run.sh start    # → http://localhost:8080
deploy/appliance/run.sh stop
```

**4. Package** it to share:

<!-- docs-test -->
```bash
make appliance-save ARCH=amd64    # or ARCH=arm64, or ARCH=all for both
```

`dist/appliance/` then holds everything the customer needs: `omni-data-bus-appliance-<version>-<arch>.tar.gz` (about 500 MB), its `.sha256`, `run.sh`, `compose.yml` and the customer guide `README.md`.

**5. Share** the `dist/appliance/` folder (a file share, a USB drive, or the assets of a GitHub release, which the release workflow attaches for every tag). For customers who can pull from a registry instead:

```bash
make appliance-push REGISTRY=registry.example.com/team    # both CPUs as one multi-arch image
```

**6. The customer** follows [`deploy/appliance/README.md`](deploy/appliance/README.md): `./run.sh load`, `./run.sh start`, open http://localhost:8080. It also covers sign-in, stopping, upgrading, backups, troubleshooting and removal.

**7. Test** a build: `make appliance-test` (image structure, then a full start / sign-up / bundled target / stop / restart / crash / memory-floor smoke test, about 3 minutes); `make test-integration-ce` runs the Go integration suite against Couchbase CE.

**Licensing.** The image redistributes Couchbase Server Community Edition unmodified, under Couchbase's [Community Edition license](https://www.couchbase.com/community-license-agreement/) (included in the image at `/opt/couchbase/LICENSE.txt`). Before delivering it to a customer, confirm with Couchbase that this use is covered.

### Running an exported migration (`migrate.sh`, `odb run`)

An export (OC-053) is a zip with `migrate.sh` and a `README.md` written for that export. The script carries the mappings as a portable bundle and runs **`odb run`**, the same pipeline as the server (documents are byte-identical), with no web app, metadata store or access to the tool:

```bash
export ODB_BUNDLE_PASSPHRASE='…'      # sealed exports (or leave unset to be asked)
./migrate.sh preflight                 # source, credentials, schema drift, target: writes nothing
./migrate.sh run                       # JSON-lines progress on stdout, logs on stderr
./migrate.sh resume | status | verify  # continue after Ctrl+C or a crash; summarize; count documents
```

`migrate.sh` finds `odb` in this order: `ODB_BIN`, a binary embedded in the script (air-gapped sites), `odb` on the `PATH`, Docker, then a download checked against the checksum written into the script. It needs bash 3.2+ (macOS or Linux). Exit codes: 0 completed, 2 completed with errors, 3 failed, 4 preflight or configuration error, 5 cancelled (resume continues). The state directory (`ODB_STATE_DIR`, default `./odb-state`) holds `checkpoint.json`, `errors.jsonl` and `report.json`.

The same works without the script: `odb bundle validate | inspect | diff` and `odb run --bundle bundle.odb.json …` (`odb run help`). Release binaries for linux and darwin come from `make dist` (`dist/odb/odb_<version>_<os>_<arch>.tar.gz` and `SHA256SUMS`; the release workflow signs `SHA256SUMS` with cosign and attaches them to the GitHub release). Point the server's `OC_EXPORT_DIST_DIR` at that folder so scripts carry the checksums. The other export settings are in `.env.example`.

## Working on a story (contributor workflow)

1. Pick the next story from [`INDEX.md`](docs/release-1.0.0/INDEX.md) (respect **Depends on**).
2. Set its **Status** to `In progress` in the story file and run `make index`.
3. Implement it; every AC needs its named tests passing (`make test`, `make lint`).
4. Set **Status** to `Implemented`, run `make index` (the row becomes ~~struck through~~), open a PR
   using the template.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `Cannot connect to the Docker daemon` | Start Docker Desktop and wait until it reports *running* (`docker info` works). If you have two Docker apps installed, open the one you normally use |
| `make dev-stack` hangs on couchbase | First start pulls ~1.5 GB; check `docker compose -f deploy/compose/dev.yml logs couchbase`. Ensure Docker has ≥ 4 GB RAM |
| `go: downloading go1.26.8` on first build | Expected — Go fetches the pinned toolchain once |
| Server exits with `missing required environment variables` | Set the `OC_META_*` and `OC_MASTER_KEY` variables, or `OC_DEV=true` for local development |
| `/readyz` returns 503 `metadata: unavailable` | Couchbase isn't reachable — check the dev stack or Capella Allowed IPs / credentials |
| Port already in use | Stop the other process or change `OC_HTTP_ADDR` / the compose port mapping |
| Go build errors mentioning `runtime/cgo` or `#include nested too deeply` | A broken C header on the machine (e.g. a stray `/usr/local/include/stdint.h`). Omni-Data-Bus doesn't need cgo: the Makefile sets `CGO_ENABLED=0`; do the same when running `go` directly |
| npm peer-dependency errors about TypeScript | TypeScript is pinned to 5.9 because `typescript-eslint` and `openapi-typescript` don't support 6/7 yet — don't upgrade it independently |

## Documentation map

| Document | What's in it |
|---|---|
| [`docs/release-1.0.0/INDEX.md`](docs/release-1.0.0/INDEX.md) | **Progress:** all stories grouped by priority, sizes, status |
| [`docs/release-1.0.0/00-architecture.md`](docs/release-1.0.0/00-architecture.md) | Tech decisions, architecture, domain model, API conventions, NFRs, testing strategy, Definition of Done |
| `docs/release-1.0.0/OC-0NN-*.md` | One user story each: implementation detail, acceptance criteria, named tests |
| [`docs/release-2.0.0/INDEX.md`](docs/release-2.0.0/INDEX.md) | **2.0 progress:** stories by phase, sizes, status; [architecture](docs/release-2.0.0/00-architecture.md) and NFRs (§7) |
| [`docs/user-guide/`](docs/user-guide/README.md) | **User guide (2.0):** directory targets, relationship mapping, the document composer, running migrations outside the tool. The in-app **?** icons link here. |
| [`docs/upgrade/1.0-to-2.0.md`](docs/upgrade/1.0-to-2.0.md) | Upgrading 1.0 → 2.0: prerequisites (1.0.1, backup), what changes, Helm values, rollback |
| [`bench/README.md`](bench/README.md) | Performance benchmark and release gates (1.0: OC-034; 2.0: OC-056, [`bench/gates-2.0.0.json`](bench/gates-2.0.0.json)) |
| [`api/openapi.yaml`](api/openapi.yaml) | REST API contract |
| [`deploy/appliance/README.md`](deploy/appliance/README.md) | Customer guide for the all-in-one image (load, start, sign in, upgrade, back up, remove) |
