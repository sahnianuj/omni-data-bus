# OC-135 — Oracle replication: convergence, chaos, benchmark and guide

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E18 Oracle Change Capture | P0 | 5 | OC-131, OC-132 | Not started |

## User story
As a **release manager**, I want Oracle replication proven correct under failures, measured and documented, so that 6.0 can ship.

## Scope
**In:**
- Convergence check matrix: golden mappings × target kinds with an Oracle source, in CI.
- Chaos suite additions: Oracle restart, log switch storm (`ALTER SYSTEM SWITCH LOGFILE` in a loop), archived log deleted (expect `needs_resync`), 1M-row transaction, long-open transaction, worker `kill -9`.
- Benchmarks (`bench/gates-6.0.0.json`) per [§6](00-architecture.md#6-non-functional-requirements), including source CPU.
- User guide: *Continuous replication from Oracle* (readiness, the DBA script, RDS for Oracle, retention planning, monitoring, troubleshooting by code) and the external feed setup. Commands tested.
- Upgrade notes 5.0 → 6.0.

**Out:** new features.

## Acceptance criteria & unit tests

### AC1 — Convergence in CI
**Given** the CI integration stage, **then** the matrix passes.

### AC2 — Chaos
**Given** the nightly chaos suite, **then** every scenario ends converged (or in the expected `needs_resync` state and then converged after resync), three nights in a row.

### AC3 — Gates
**Given** a reference-environment run, **then** every 6.0 gate passes.

| Test | Location | Verifies |
|---|---|---|
| `TestConvergence_AC1_OracleMatrix` | `internal/replication/convergence_matrix_integration_test.go` | AC1 |
| `chaos/oracle-*.sh` | `bench/chaos/` | AC2 |
| `make bench-gate` | `bench/gates-6.0.0.json` | AC3 |
