# OC-001 — Monorepo scaffolding, local dev environment & CI

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E1 Foundation | P0 | 5 | — | Implemented |

## User story
As an **engineer on Omni-Data-Bus**, I want a monorepo with one-command setup, a local stack, and CI quality gates, so that every later story starts from a consistent, tested baseline.

## Scope
**In:** repo layout (see [architecture §4](00-architecture.md#4-repository-layout)), Go module, Vite React app, Makefile, docker-compose dev stack (Couchbase for the metadata bucket and the migration target, plus a sample source PostgreSQL with seed data), linters, formatters, GitHub Actions CI, pre-commit hooks, CODEOWNERS, PR template.
**Out:** production deployment (OC-032).

## Implementation details

### Repository bootstrap
- `backend/go.mod`: module `github.com/<org>/omni-data-bus/backend`, with the Go version pinned in `go.mod` and `.tool-versions`.
- `web/`: `npm create vite@latest` (react-ts template), `pnpm` as the package manager, and `"type": "module"`.
- `tsconfig.json`: `strict: true`, `noUncheckedIndexedAccess: true`, and the path alias `@/* → src/*`.
- `.editorconfig`, `.gitignore` (Go, Node, `.env*`, `dist`, `coverage`), `.gitattributes` (LF).

### Makefile targets
| Target | Action |
|---|---|
| `make setup` | Download Go modules and install `golangci-lint`, then `npm ci` in `web/` |
| `make dev` | `docker compose -f deploy/compose/dev.yml up -d`, then run the backend with `air` (hot reload) and `pnpm dev` concurrently |
| `make gen` | `npm run gen:api` (TypeScript types from `api/openapi.yaml`) |
| `make lint` | `golangci-lint run ./...`, `pnpm lint`, `pnpm typecheck`, `redocly lint api/openapi.yaml` |
| `make test` | `go test -race -coverprofile=… ./...` and `pnpm test --coverage` |
| `make test-integration` | `go test -tags=integration ./...` (testcontainers) |
| `make e2e` | Playwright against the compose stack |
| `make check-gen` | `make gen` followed by `git diff --exit-code` (fails when generated code is stale) |

### Dev compose (`deploy/compose/dev.yml`)
- `source-pg`: `postgres:16`, port 5434. Seeded from `deploy/compose/seed/source/*.sql` with a realistic sample schema (`shop`: customer, address, orders, order_item, product, category) covering every type in [§6.4](00-architecture.md#64-logical-type-normalization-postgresql--jsontype), one table without a PK, a table with 1M generated rows (`generate_series`), and an identifier with odd characters (`"Order Details"`).
- `couchbase`: `couchbase/server:enterprise-8.0.3`, ports 8091–8097, 11210. An init container runs `couchbase-cli cluster-init` (services data, index, query), creates the admin user, and creates two buckets: **`omniconnect`** (Omni-Data-Bus's own metadata, standing in for the Capella bucket) and **`sandbox`** (a migration target).
- Health checks gate dependent services.

### CI (GitHub Actions `.github/workflows/ci.yml`)
Jobs run in parallel:
1. `backend-lint`: golangci-lint (enabled: govet, staticcheck, errcheck, gosec, revive, gocritic, bodyclose, contextcheck).
2. `backend-test`: `go test -race`, coverage uploaded, and the coverage gate script fails below the thresholds in the architecture doc.
3. `backend-integration`: testcontainers (Docker available on ubuntu-latest).
4. `web`: lint, typecheck, vitest with coverage, and `vite build`.
5. `contract`: `make check-gen`.
6. `e2e` (on `main` and labeled PRs): compose up plus Playwright. Traces are uploaded as artifacts on failure.

Go modules and the pnpm store are cached.

### Pre-commit (`lefthook.yml`)
gofumpt, goimports, eslint --fix, and prettier on staged files, plus a commit-msg check for Conventional Commits.

## Acceptance criteria & unit tests

### AC1 — One-command setup
**Given** a clean machine with Go, Node/npm and a `.env` pointing the metadata store at Couchbase Capella, **when** a developer runs `make setup && make dev`, **then** the backend responds on `http://localhost:8080/readyz` with `200` and `metadata: ok`, and the web app loads on `http://localhost:5173` with `/api` proxied to the backend. Docker is **not** required on this path.
**Given** no `.env` (fully local development), **when** `make dev` runs, **then** the Docker dev stack (Couchbase 8.0.3 + sample PostgreSQL) starts healthy first and the backend uses it.

| Test | Location | Verifies |
|---|---|---|
| `TestConfig_AC1_DefaultsMatchDevCompose` | `backend/internal/config/config_test.go` | Default DSNs and ports in config match `dev.yml` (parses the compose YAML and compares) |
| `scripts/check-compose.test.ts` › `AC1: all services declare healthchecks` | `web/scripts/check-compose.test.ts` | Every service in `dev.yml` has a `healthcheck` block |

### AC2 — Seed data covers the type matrix
**Given** the dev stack is up, **when** the seed runs, **then** `source-pg` contains the `shop` schema with at least one column for every PostgreSQL type in §6.4, one table without a PK, `"Order Details"`, and `big_events` with 1,000,000 rows.

| Test | Location | Verifies |
|---|---|---|
| `TestSeed_AC2_ContainsAllMappedTypes` | `backend/internal/devseed/seed_test.go` | Parses the seed SQL and asserts each §6.4 type name appears in a `CREATE TABLE` |
| `TestSeed_AC2_HasTableWithoutPK` | same | At least one `CREATE TABLE` has no `PRIMARY KEY` |

### AC3 — Lint and format gates
**Given** a PR containing a lint violation (e.g., an unchecked error or a TS `any`), **when** CI runs, **then** the corresponding lint job fails and the PR cannot merge (branch protection requires all CI jobs).

| Test | Location | Verifies |
|---|---|---|
| `TestLintConfig_AC3_RequiredLintersEnabled` | `backend/tools/lintcfg_test.go` | `.golangci.yml` enables errcheck, gosec, staticcheck, govet |
| `eslint-config.test.ts` › `AC3: forbids explicit any` | `web/scripts/eslint-config.test.ts` | ESLint config sets `@typescript-eslint/no-explicit-any: error` |

### AC4 — Generated code freshness
**Given** a developer edits `api/openapi.yaml` or a `.sql` query without regenerating, **when** CI runs `make check-gen`, **then** the job fails and names the stale files.

| Test | Location | Verifies |
|---|---|---|
| `TestCheckGen_AC4_DetectsDiff` | `backend/tools/checkgen_test.go` | The helper that runs `git diff --name-only` returns a non-empty list and a non-zero exit when a fixture file differs |

### AC5 — Coverage gate
**Given** backend coverage below the thresholds, **when** CI runs, **then** `backend-test` fails with a message naming each package under threshold.

| Test | Location | Verifies |
|---|---|---|
| `TestCoverageGate_AC5_FailsBelowThreshold` | `backend/tools/covergate/covergate_test.go` | Parses a fixture `cover.out`; returns an error listing `internal/automap 72% < 90%` |
| `TestCoverageGate_AC5_PassesAtThreshold` | same | Exactly 80% / 90% passes |

## Notes
- The tools under `backend/tools/` carry build tag `//go:build tools` where needed, so they never ship in the binary.
- The version of every container image is pinned by digest in CI.

## Implementation notes (as built)
- **Docker is optional.** Omni-Data-Bus's own data lives in Couchbase Capella (see OC-003), so `make dev` skips the Docker stack whenever `.env` points the metadata store at a remote cluster. The stack (`make dev-stack`) still provides an offline metadata bucket, a sample PostgreSQL source and a local Couchbase target.
- **Verified:** both paths of AC1. Capella: `make dev` → `/readyz` 200 with `metadata: ok`, web app served, `/api` proxied. Docker: `make dev-stack` brings Couchbase 8.0.3 and the seeded PostgreSQL up healthy (buckets `omniconnect` and `sandbox` created, 1,000,000-row `big_events` loaded), and `make test-integration` passes on local containers.
- **npm instead of pnpm**, and **no sqlc/goose/oapi-codegen** (see OC-002/OC-003 notes). `CGO_ENABLED=0` is set in the Makefile.
- Toolchain pins: Go `go1.26.8` via `toolchain` in `go.mod` (downloaded automatically); TypeScript 5.9 (typescript-eslint and openapi-typescript don't support 6/7 yet); golangci-lint v2.14.0.

