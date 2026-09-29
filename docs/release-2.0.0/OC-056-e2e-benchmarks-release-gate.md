# OC-056 — End-to-end suite & benchmarks for 2.0 (release gate)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E12 Harden & Ship 2.0 | P0 | 8 | All P0 stories | Not started |

## User story
As a **release manager**, I want the new 2.0 paths (directory target, nested and many-to-many relations, composer, exported scripts and the Kubernetes kit) covered by end-to-end tests and measured against the 2.0 performance gates, so that we ship 2.0 knowing that it works and is fast enough.

## Scope
**In:**
- Playwright E2E for the new UI flows.
- Runner E2E: script on Linux and macOS, plus the Kubernetes kit on kind.
- Server ↔ runner parity suite.
- Benchmarks for the NFRs in [architecture §7](00-architecture.md#7-additional-non-functional-requirements-release-gate-validated-by-oc-056), with results committed under `bench/results/2.0.0/`.
- The 1.0 E2E suite and OC-034 gates re-run unchanged (regression).

**Out:** load tests of the web UI (unchanged from 1.0).

## Implementation details

### Fixture database (`bench/fixtures/shop2`)
`customers` (1M), `orders` (5M), `order_items` (50M, avg 10 per order, skewed: 1% of orders have 500+), `products` (100k), `brands` (1k), `tags` (500), `order_tags` (10M), plus one table without an FK used through a virtual relationship. It is generated deterministically by `bench/fixtures/gen` with a seed.

### E2E scenarios (Playwright, `web/tests/e2e/`)
| Spec | Flow |
|---|---|
| `directory-target.spec.ts` | Create project → directory target → map `orders` → migrate → Output tab finds `1001` by key |
| `nested-relations.spec.ts` | Canvas: embed `order_items → products` path and `tags` m2m → preview → migrate to Couchbase → verify |
| `composer-order.spec.ts` | Composer from pasted JSON → bind arrays → auto-map → preview → migrate |
| `export-script.spec.ts` | Export sealed script → run it in a container with the fixture DB → import `report.json` → External run shown |

### Runner E2E
- `bats` suites from OC-053 on `ubuntu-latest` (bash 5) and `macos-latest` (bash 3.2).
- The kind suite from OC-054 (apply, kill pod, resume, fetch).
- **Parity:** for 5 golden mappings, run the server job and `odb run` against the same snapshot, then diff every document (`diff -r` for directories; a KV dump plus diff for Couchbase). Any byte difference fails.

### Benchmarks (`bench/`)
| Gate | Scenario | Target |
|---|---|---|
| Directory throughput | `orders` flat 1 KB docs, NVMe, `durability: file`, 8 vCPU | ≥ 15,000 files/s |
| Nested relations | orders + items (10) + product, Couchbase target | ≥ 8,000 docs/s |
| Sorted-merge | customers + all orders (skewed) | ≥ 1.5× batched; RSS < 768 MB |
| Memory | Any of the above, defaults | RSS < 768 MB |
| Runner cold start | `migrate.sh preflight` with a cached binary | < 5 s |
| 1.0 regression | OC-034 scenarios | Within 5% of the 1.0 results |

Results are written as JSON plus Markdown summaries, and the CI job `bench-gate` fails when a gate is missed.

## Acceptance criteria & unit tests

### AC1 — E2E suite green
**Given** the compose E2E stack, **then** all four new Playwright specs and the 1.0 suite pass in CI on `main`.

| Test | Location | Verifies |
|---|---|---|
| `directory-target.spec.ts`, `nested-relations.spec.ts`, `composer-order.spec.ts`, `export-script.spec.ts` | `web/tests/e2e/` | |

### AC2 — Runner E2E green
**Given** CI, **then** the bats suites pass on bash 3.2 and bash 5, and the kind suite passes.

| Test | Location | Verifies |
|---|---|---|
| CI jobs `runner-bats-linux`, `runner-bats-macos`, `e2e-kind` | `.github/workflows/ci.yml` | |

### AC3 — Parity
**Given** the 5 golden mappings, **then** server and runner outputs are byte-identical.

| Test | Location | Verifies |
|---|---|---|
| `TestParity_AC3_GoldenMappings` *(integration)* | `internal/headless/parity_integration_test.go` | |

### AC4 — Performance gates
**Given** the reference environment, **then** every gate in the table passes and the results are committed under `bench/results/2.0.0/`.

| Test | Location | Verifies |
|---|---|---|
| `bench-gate` | `bench/gate.go` | Reads results JSON, compares with `bench/gates-2.0.0.json` |
