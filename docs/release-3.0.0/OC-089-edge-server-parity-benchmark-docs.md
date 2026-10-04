# OC-089 — Edge Server target: parity, end-to-end tests, benchmark and setup guide

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Edge Server Target | P0 | 3 | OC-086, OC-087, OC-088 | Not started |

## User story
As the **release owner**, I want proof that the Edge Server target writes the same documents as the other targets at an acceptable speed, plus a setup guide for customers, so that we can ship it.

## Scope
**In:**
- **Parity test:** the 2.0 reference mappings (flat, embedded many-to-one, `items[]` arrays, grouped rows) written to Couchbase, Directory and Edge Server produce identical document IDs and bodies.
- **CI:** an Edge Server container (pinned by digest, generated config with writes and ad-hoc queries on) in the integration and E2E suites; `make test-integration-edge`.
- **Benchmark:** throughput for 100k and 1M documents of ~1 KB at batch sizes 50/200/1,000 and concurrency 1/4/8, on the reference host; set the defaults and the NFR in [architecture §5](00-architecture.md#5-additional-non-functional-requirements); results under `bench/results/edge/`.
- **Setup guide** `docs/guides/edge-server-target.md`: the Edge settings (database, `collections`, `enable_client_writes`, `enable_adhoc_queries`, a write user, TLS), what to do when a check fails, and the replication choice: documents written to an Edge database that replicates to Capella App Services will flow upstream, subject to its sync function; disable or filter that replication if it isn't wanted.

**Out:** benchmarking replication to Capella.

## Acceptance criteria

### AC1 — Parity
**Given** the reference mappings, **then** all three targets hold identical IDs and bodies (ignoring Edge's `_rev`).

### AC2 — Benchmark gate
**Given** the reference host, **then** the default settings reach the throughput set in §5 for 100k documents, and the results file is committed.

### AC3 — Guide works as written
**Given** a fresh machine, **when** someone follows the guide, **then** a migration into Edge Server and an Explore query succeed; the guide's commands are run in CI (as OC-074 does).

| Test | Location | Verifies |
|---|---|---|
| `TestParity_AC1_ThreeTargets` | `internal/pipeline/parity_edge_test.go` (integration) | AC1 |
| `bench/edge` run | `bench/results/edge/` | AC2 |
| `docs_commands_edge.sh` | `deploy/appliance/test/` | AC3 |
