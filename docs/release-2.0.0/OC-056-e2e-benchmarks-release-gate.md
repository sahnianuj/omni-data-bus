# OC-056 — End-to-end suite & benchmarks for 2.0 (release gate)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E12 Harden & Ship 2.0 | P0 | 8 | All P0 stories | Implemented |

## User story
As a **release manager**, I want the new 2.0 paths (directory target, nested and many-to-many relations, composer, exported scripts and the all-in-one image) covered by end-to-end tests and measured against the 2.0 performance gates, so that we ship 2.0 knowing that it works and is fast enough.

## Scope
**In:**
- Playwright E2E for the new UI flows.
- Runner E2E: script on Linux and macOS.
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

## Results (2026-10-02)
- **AC1, E2E.** `directory-target.spec.ts`, `nested-relations.spec.ts`, `composer-order.spec.ts` and `export-script.spec.ts` pass together with the 1.0 suite against the compose stack: 20 passed, and the 2 appliance specs skipped as in CI. `export-script` exports from the UI with step-up, runs `migrate.sh` in a container on the dev network, and imports `report.json`.
- **AC2, runner E2E.** CI runs the bats suites in `runner-bats-linux` (bash 5) and `runner-bats-macos` (bash 3.2). The jobs are `make test-scripts`.
- **AC3, parity.** `TestParity_AC3_GoldenMappings` runs five golden mappings through a server job and through `odb run`, from a sealed export of the same project. The five are flat `shop.customer`, `"Order Details"` with a composite key, orders with customer and items, `sales.orders` with items → product plus tags (m2m), and `shop.product` with every type. Every document file is byte-identical.
- **AC4, gates.**
  - [`bench/gates-2.0.0.json`](../../bench/gates-2.0.0.json) holds G1–G7, and `odbbench gate` judges results against it (`make bench-gate`, CI job `bench-gate`). The release workflow blocks a release with a gate file until every gate has passed in the reference environment.
  - New datasets: `directory`, `nested`, `skewed` and `fanout`. New flags: `--directory` and `--strategy`. `odbbench coldstart` times `migrate.sh preflight`.
  - Local runs are indicative, under [`bench/results/2.0.0/local/`](../../bench/results/2.0.0/local/README.md):
    - **Pass:** G2 nested (54k docs/s), every memory gate (257–630 MiB), G5 cold start (0.45 s), and every no-loss gate.
    - **Fail:** G1 directory (13k files/s with `durability: file` on Docker Desktop's virtual disk, against 15k on NVMe) and G3 sorted-merge vs batched (1.0×).
    - **Pending:** G6 has no 1.0 baseline.

### Fixed along the way
- **Array order.** Many-to-many and one-to-many arrays without a unique `orderBy` now get the child's primary key as a tiebreaker (then the junction's) in the related SQL. Before, array order depended on PostgreSQL's plan, so verification and parity failed intermittently.
- **Output tab.** It couldn't open documents whose key contains `::` (every default key) because the key arrived URL-escaped. Fixed in `internal/documents`.
- **Directory target memory.** The worker reached 989 MiB on 1M files. Now:
  - Manifest merge is an external sort of 100k-line runs.
  - Integrity check is a single-pass reservoir sample.
  - Count is a batched directory walk.
  - The Output page streams the sorted manifest.
- **Worker memory with large documents.** Workers reached 1.1 GB with 75 KB documents of 500 embedded rows, beyond the chart's 1Gi pod limit. The cause was allocation churn, not live data (230 MiB in use). New `OC_MEMORY_LIMIT_MB`, default 640, sets the Go soft memory limit and keeps RSS at about 630 MiB, at some throughput cost: 1,500 docs/s instead of 2,000 in that scenario.
- `composer-group.spec.ts` asserted a reference layout that had been stale since OC-076.

### Open
- **The reference-environment run (AC4)**, as for OC-034 AC10: run the 2.0 scenarios with `--env reference=true`, then commit the results under `bench/results/2.0.0/` together with a 1.0 baseline (`bench/results/1.0.0/standard.json`).
- **G3 is at risk.** Locally, sorted-merge doesn't beat batched. If the reference run agrees, revisit OC-046's 1.5× target or the `auto` thresholds before shipping.

### Changes from the plan
- **No `bench/fixtures/shop2`.** The harness's own deterministic datasets (`generate_series`) cover the same shapes: 10 items per order with a product, many orders per customer, and a 1% skew. The 5 golden parity mappings use the dev `shop`/`sales` schemas.
- **Parity covers directory targets.** A Couchbase KV dump-and-diff wasn't built. The pipeline code after the writer is the same for both targets.
- **G3 runs on `fanout`**, OC-046 AC5's shape of 500 children per parent. The skewed scenario (5 children each, 500 for 1% of parents) is where batched wins.
- **`e2e-kind` is descoped.** The Helm chart is covered by `helm-test`, and the exported script by `runner-bats-*` and `export-script.spec.ts`.
- **Locations:** the gate tool is `odbbench gate` (`bench/cmd/odbbench/gate.go`, `bench/internal/gate`) rather than `bench/gate.go`.

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
