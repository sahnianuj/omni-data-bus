# OC-126 — Continuous replication: convergence check, chaos tests, benchmark and guide

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E16 Continuous Replication | P0 | 5 | OC-122, OC-123, OC-124 | Not started |

## User story
As a **release manager**, I want replication proven correct under failures, measured against its targets and documented, so that 5.0 can ship to customers who will run it on production databases.

## Scope
**In:**
- **Convergence check** in CI ([architecture §9](00-architecture.md#9-definition-of-done-additions)) for the golden mappings on all three target kinds, PostgreSQL 13 and 17.
- **Chaos suite** (nightly): worker `kill -9`, PostgreSQL restart, network partition between worker and source and between worker and target, worker failover, slot dropped by hand, mapping drift, truncate. Each ends with the convergence check.
- **Benchmarks** (`bench/gates-5.0.0.json`): sustained changes/s and p95 lag for flat and golden relation mappings, fan-out rebuild rate, memory bound. Results under `bench/results/5.0.0/`.
- **User guide** chapter *Continuous replication*: when to use it, guarantees (D4) in plain words, setup per provider, policies, monitoring, WAL safety, cut-over runbook, troubleshooting by error code. Commands tested.
- Upgrade notes 4.0 → 5.0 (new collections, provisioning step, `DocDeleter` for custom targets).

**Out:** Oracle (6.0).

## Acceptance criteria & unit tests

### AC1 — Convergence in CI
**Given** the CI integration stage, **then** the convergence check passes for every golden mapping × target kind × PostgreSQL version.

### AC2 — Chaos
**Given** the nightly chaos suite, **then** every scenario ends with the convergence check passing, three nights in a row before release.

### AC3 — Gates
**Given** a reference-environment run, **then** every 5.0 gate passes.

| Test | Location | Verifies |
|---|---|---|
| `TestConvergence_AC1_Matrix` | `internal/replication/convergence_matrix_integration_test.go` | AC1 |
| `chaos/*.sh` | `bench/chaos/` | AC2 |
| `make bench-gate` | `bench/gates-5.0.0.json` | AC3 |
