# Development guide

How to run Omni-Data-Bus locally, configure it, and test it. For building and deploying, see [deployment.md](deployment.md); for the contributor workflow (stories, tests, notices, releases), see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Architecture

```
 1. Create project ─▶ 2. Source repo (PostgreSQL) ─▶ 3. Target repo (Couchbase or JSON files)
        │                  test ✓ · save (encrypted)       test ✓ · save (encrypted)
        ▼
 4. Select tables ─▶ 5. Design document + auto-map ─▶ 6. Map the rest ─▶ 7. Preview
        ▼
 8. Migrate: read (PostgreSQL snapshot) ─▶ transform ─▶ write (Couchbase KV or files)
        │         live progress bar · pause · resume · errors
        ▼
 9. Verify & download report          (or export it, and run it anywhere with `odb run`)
```

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
 Metadata store            Source database          Target
 Couchbase Capella         PostgreSQL               Couchbase (Capella or
 (bucket "omniconnect")    (read-only)              self-managed) or a folder
```

- The server is **stateless**: all state lives in the Couchbase metadata bucket, so several replicas can run behind a load balancer.
- One binary, three roles: `--role=api`, `--role=worker`, or `--role=all` (default). The same binary runs exported migrations (`odb run`).
- Full design, domain model, API conventions and NFRs: [release 1.0 architecture](release-1.0.0/00-architecture.md), then each release's `00-architecture.md`.

### Technology stack

| Layer | Choice | Why |
|---|---|---|
| Backend | **Go 1.26** (toolchain pinned in `backend/go.mod`) | Goroutines and bounded channels give a simple, fast streaming pipeline with natural back-pressure; first-class drivers for both databases; one static binary |
| HTTP | `net/http` + `go-chi/chi` | Small, idiomatic, composable middleware |
| Metadata store | **Couchbase Capella** (Couchbase Server **8.0.3** locally) via `gocb/v2` | KV for documents, SQL++ for lists, ACID transactions, CAS for optimistic locking, TTL for sessions |
| Source driver | `jackc/pgx/v5` | Fastest PostgreSQL driver; snapshot export for consistent parallel reads |
| Frontend | React 19, TypeScript 5.9 (strict), Vite 8 | Fast dev loop, type safety |
| UI | Tailwind CSS 4, Radix primitives, lucide icons, Geist / Geist Mono fonts | Accessible, light and dark themes |
| Data fetching / forms | TanStack Query, react-hook-form + zod, openapi-fetch (types generated from OpenAPI) | Typed end to end |
| Tests | Go `testing` + testify + testcontainers; Vitest + Testing Library + MSW; Playwright | Every acceptance criterion has named tests |

## Prerequisites

| Tool | Version | Notes |
|---|---|---|
| Couchbase Capella cluster | — | Omni-Data-Bus's metadata store (see below). Not needed with the Docker dev stack |
| Docker Desktop *(optional)* | recent | Only for the offline dev stack: Couchbase 8.0.3 + sample PostgreSQL source. Give it **≥ 4 GB RAM** |
| Go | 1.21 or newer installed | `go.mod` pins **go1.26.9**; Go downloads that toolchain on first use (`GOTOOLCHAIN=auto`) |
| Node.js | 20+ (tested with 24) | npm (no pnpm needed) |
| make, git, python3 | any recent | `python3` for the story index and the third-party notices |

Ports used locally: **8080** (API), **5173** (web), **8091–8097, 11210** etc. (Couchbase), **5434** (sample PostgreSQL).

## Running it locally

**With your Capella cluster (no Docker needed):**

```bash
cp .env.example .env   # fill in OC_META_* for Capella and OC_MASTER_KEY (see below)
make setup             # Go modules, golangci-lint, web dependencies
make dev               # API on :8080 + web on :5173
```

**Fully offline (Docker):** skip the `.env` step. `make dev` then starts the Docker dev stack (Couchbase 8.0.3 for the metadata store and a sample PostgreSQL source) before the API and web app.

Then open **http://localhost:5173** and create an account.

What `make dev` does (see [`scripts/dev.sh`](../scripts/dev.sh)):
1. If `.env` points the metadata store at a remote cluster (Capella), it skips Docker. Otherwise it runs `make dev-stack` (containers and the one-shot `couchbase-init` job) and uses `OC_DEV=true`.
2. Builds every machine's odb into `dist/odb` in the background when it's missing or older than the sources, so exports can add odb for Linux, Windows and macOS (OC-188).
3. Starts the Go API (it reads `.env` itself, connects to the metadata store and provisions it on first start).
4. Starts the Vite dev server (it proxies `/api`, `/healthz`, `/readyz` and `/licenses.txt` to `:8080`).

The Docker stack is still handy alongside Capella: `make dev-stack` gives you the sample PostgreSQL source (`localhost:5434`) and a local Couchbase target to migrate into. Stop the app with `Ctrl+C`, the containers with `make dev-stack-down` (`make dev-reset` also deletes their data).

| URL | What |
|---|---|
| http://localhost:5173 | The web app |
| http://localhost:8080/healthz | Liveness |
| http://localhost:8080/readyz | Readiness (checks the metadata bucket) |
| http://localhost:8080/api/v1/version | Build version |
| http://localhost:8080/licenses.txt | Licence, NOTICE and third-party notices |
| http://localhost:8091 | Couchbase Web Console (dev login `Administrator` / `password`) |

### The local dev stack

Defined in [`deploy/compose/dev.yml`](../deploy/compose/dev.yml):

| Service | Image | Purpose |
|---|---|---|
| `couchbase` | `couchbase/server:enterprise-8.0.3` | Hosts **`omniconnect`** (Omni-Data-Bus's own metadata; stands in for your Capella bucket) and **`sandbox`** (a migration target to experiment with) |
| `couchbase-init` | same | Idempotent one-shot: `cluster-init` (data, index, query), creates both buckets |
| `source-pg` | `postgres:16` on **localhost:5434** | Sample **source** database `shop` to migrate *from* |

The Enterprise image is pulled by you for local development and isn't redistributed; production use of Couchbase Server Enterprise needs a licence.

Sample source database (seeded from [`deploy/compose/seed/source`](../deploy/compose/seed/source)):

- Schema `shop`: `customer`, `address`, `product`, `category`, `orders`, `"Order Details"`, `page_view` (no primary key), `big_events` (1,000,000 rows). Together they cover every PostgreSQL type Omni-Data-Bus converts.
- Schema `billing`: a few tables the read-only role **cannot** read, to demonstrate the privilege warning in *Test connection*.
- The point-of-sale fixtures for design-first mapping (OC-162).
- Logins: owner `shop_owner` / `shop_owner`; **read-only role for Omni-Data-Bus `omni_ro` / `omni_ro`**.

To use the sandbox target in the app: connection string `couchbase://localhost`, user `Administrator`, password `password`, deployment *Self-managed*.

> Dev credentials above exist only in the local containers. Never reuse them anywhere else.

## Configuration reference

Configuration comes from environment variables, usually through a **`.env` file** (`cp .env.example .env`, then edit it):

- `.env` is git-ignored. The server reads it itself from the repo root or `backend/` (override the path with `OC_ENV_FILE`). Real environment variables win over the file.
- Values are taken literally, so passwords containing `$`, `` ` ``, `#` or quotes work without escaping.
- Without a `.env`, `make dev` runs with `OC_DEV=true` against the **local** dev stack.
- **Dev defaults never apply to a remote cluster.** If `OC_META_CONNSTR` points at Capella, the username, password and `OC_MASTER_KEY` must be set; the server refuses to start with the published development key.

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
| `OC_MASTER_KEY` | *(required)* | Base64 32-byte key that encrypts stored credentials. Generate with `odb admin gen-key` or `openssl rand -base64 32` |
| `OC_MASTER_KEYS` / `OC_MASTER_KEY_ACTIVE` | — | Key rotation: `k2:<b64>,k1:<b64>` plus the active id |
| `OC_SIGNUP_ENABLED` | `true` | Allow self sign-up |
| `OC_SESSION_IDLE_TTL` / `OC_SESSION_ABSOLUTE_TTL` | `12h` / `168h` | Session lifetimes |
| `OC_SESSION_COOKIE_SECURE` | `true` | Forced `false` in dev (plain http) |
| `OC_SHUTDOWN_TIMEOUT` | `30s` | Graceful shutdown budget |
| `OC_REDISCOVER_ON_START` | `false` | Rescan a source's saved schema (row counts, new tables) the first time it's opened after this start; `make dev FORCE=1` sets it |
| `OC_METRICS_ADDR` | — | Serve Prometheus `/metrics` on its own listener (e.g. `:9090`); empty = on the public port |
| `OC_PPROF_ADDR` | — | Serve Go `net/http/pprof` on its own listener (e.g. `127.0.0.1:6060`) for benchmark profiling; never expose it publicly |
| `OC_OTEL_EXPORTER_OTLP_ENDPOINT` | — | OTLP/HTTP collector URL (e.g. `http://otel-collector:4318`); empty = tracing off |
| `OC_OTEL_SAMPLE_RATIO` | `0.1` | Parent-based trace ID ratio sampling |
| `OC_ALLOW_PRIVATE_NETWORKS` | `true` | Allow connections to private/loopback addresses (set `false` for a multi-tenant service) |
| `OC_EGRESS_ALLOW_CIDRS` / `OC_EGRESS_DENY_CIDRS` | — | Extra allowed / refused ranges, comma-separated (deny wins) |
| `OC_EGRESS_PORTS` | PostgreSQL, Couchbase, 443 | Allowed destination ports, e.g. `5432-5439,6432,11207,18091-18097,443` |
| `OC_AUDIT_RETENTION_DAYS` | `365` | How long audit events are kept |
| `OC_EXPORT_*` | see `.env.example` | Exported migrations: the image and download URL their READMEs name, the odb dist folder, fetching odb on demand and its cache (OC-182, OC-188) |

### Omni-Data-Bus's own data: the Couchbase metadata store

Omni-Data-Bus stores **its own data** (users, sessions, projects, encrypted connection credentials, mappings, jobs) in a Couchbase bucket: **Couchbase Capella in production**, Couchbase Server 8.0.3 in the local dev stack. There is no PostgreSQL in Omni-Data-Bus's own runtime; PostgreSQL only appears as a *source* you migrate from.

Setting up Capella:
1. In Capella, create the bucket `omniconnect` (or your `OC_META_BUCKET`); the SDK can't create buckets on Capella.
2. Create a database credential with read/write access to that bucket.
3. Add your machine's (or the server's egress) IP to the cluster's **Allowed IP addresses**.
4. Set `OC_META_CONNSTR` (the `couchbases://…` string from Capella → Connect), `OC_META_USERNAME`, `OC_META_PASSWORD`, `OC_META_CAPELLA=true`, and a master key (`odb admin gen-key`).
5. `make run-backend` (or `make dev`). On first start Omni-Data-Bus provisions scope `meta`, its collections and indexes, and `/readyz` reports `"metadata":"ok"`. If the credential lacks permission, `odb admin provision --print` lists the scopes, collections and indexes to create in the Capella UI.

How relational-style guarantees are achieved on Couchbase: ACID transactions for multi-document writes, "uniqueness" documents (e.g. `email::<address>`) inserted in the same transaction, CAS for optimistic concurrency, document expiry for sessions and retention. Details: [architecture §5](release-1.0.0/00-architecture.md) and [OC-003](release-1.0.0/OC-003-metadata-store-couchbase.md).

## Testing

Every story's acceptance criteria (ACs) map to **named tests** that carry the story and AC, for example `TestExport_OC182_AC1_Files` in Go and `it('OC-182 AC4: …')` in the web app (1.0 tests use `Test<Unit>_AC<n>_<Behaviour>`), so a failure points back to the story.

```bash
make test               # all unit tests (Go with -race + coverage gate, web with coverage)
make test-backend       # Go unit tests only
make test-web           # web unit tests only
make test-integration   # Go integration tests: needs Docker (Couchbase 8.0.3, PostgreSQL via testcontainers)
make e2e                # Playwright end-to-end suite against the built image (local; CI runs the @smoke set)
make lint               # golangci-lint, eslint, tsc
make notices-check      # third-party notices current, every shipped licence allowed
```

Coverage gates: Go packages ≥ 80% (≥ 90% for automap/transform/pipeline), web ≥ 75% lines.

## Make targets

`make help` lists every target. The most used:

| Target | Description |
|---|---|
| `make setup` | Install Go modules, golangci-lint, web dependencies |
| `make dev` | Dev stack + API + web (one command); `make dev FORCE=1` / `make dev-force` also rescans saved schemas |
| `make dev-stack` / `dev-stack-down` / `dev-reset` | Start / stop / wipe the containers |
| `make run-backend` / `run-web` | Run one side only |
| `make test`, `test-backend`, `test-web`, `test-integration`, `test-integration-ce` | Tests (the last against Couchbase Server CE, as in the all-in-one image) |
| `make e2e` / `e2e-smoke` | Playwright end-to-end suite / the CI smoke set |
| `make lint` | Linters and type checks |
| `make gen` / `check-gen` | Regenerate TypeScript API types / fail if stale |
| `make build` | Build the web app and one binary with it embedded (`backend/bin/odb`) |
| `make image` / `image-test` | Build / check the container image |
| `make helm-test` | Lint and unit-test the Helm chart |
| `make dist` / `verify-dist` | Build odb for every machine (linux, darwin, windows × amd64/arm64, universal macOS with `lipo`) with `SHA256SUMS` in `dist/odb/` / check them |
| `make appliance`, `appliance-save`, `appliance-push`, `appliance-test` | Build, package, push and test the [all-in-one image](deployment.md#all-in-one-image-app--couchbase-ce) |
| `make notices` / `notices-check` | Regenerate / check `THIRD_PARTY_NOTICES.md` (OC-189) |
| `make bench`, `bench-gate` | Performance benchmark / judge the release gates ([bench/README.md](../bench/README.md)) |
| `make test-scripts` | The 2.0 `migrate.sh` checks, kept until 3.0: shellcheck and the bats suites |
| `make index` | Regenerate `docs/release-1.0.0/INDEX.md`; other releases: `python3 scripts/update-story-index.py docs/release-<version>` |

## Troubleshooting

| Symptom | Fix |
|---|---|
| `Cannot connect to the Docker daemon` | Start Docker Desktop and wait until it reports *running* (`docker info` works). If you have two Docker apps installed, open the one you normally use |
| `make dev-stack` hangs on couchbase | First start pulls ~1.5 GB; check `docker compose -f deploy/compose/dev.yml logs couchbase`. Ensure Docker has ≥ 4 GB RAM |
| `go: downloading go1.26.9` on first build | Expected: Go fetches the pinned toolchain once |
| Server exits with `missing required environment variables` | Set the `OC_META_*` and `OC_MASTER_KEY` variables, or `OC_DEV=true` for local development |
| `/readyz` returns 503 `metadata: unavailable` | Couchbase isn't reachable: check the dev stack or Capella Allowed IPs / credentials |
| Port already in use | Stop the other process or change `OC_HTTP_ADDR` / the compose port mapping |
| Go build errors mentioning `runtime/cgo` or `#include nested too deeply` | A broken C header on the machine (e.g. a stray `/usr/local/include/stdint.h`). Omni-Data-Bus doesn't need cgo: the Makefile sets `CGO_ENABLED=0`; do the same when running `go` directly |
| npm peer-dependency errors about TypeScript | TypeScript is pinned to 5.9 because `typescript-eslint` and `openapi-typescript` don't support 6/7 yet: don't upgrade it independently |
| The export dialog offers only this machine's odb | `dist/odb` is still building in the background (about a minute), or the build failed: see `dist-build.log` |
