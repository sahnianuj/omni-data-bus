# OC-114 — Oracle source and PL/SQL conversion: parity, E2E, benchmark and guides

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 5 | OC-101, OC-111 | Not started |

## User story
As a **release manager**, I want proof that Oracle migrations produce the same documents as PostgreSQL ones, perform within the gate, and that the PL/SQL conversion flow works end to end, with guides customers can follow, so that 4.0 can ship.

## Scope
**In:**
- Parity: the 2.0 golden mappings on the Oracle seed produce the documented-identical output on every target kind (Couchbase, Directory, Edge Server).
- E2E (Playwright): Oracle project → map → migrate → verify; Functions area: inventory → assess → convert → deploy → run side by side → batch verify.
- Benchmarks (`bench/gates-4.0.0.json`): Oracle read throughput ≥ 70% of PostgreSQL on the same dataset, assessment and conversion speed (architecture §8), side-by-side overhead. Results under `bench/results/4.0.0/`.
- User guide chapters: *Oracle as a source* (connection modes, wallets, privileges with the grant snippet, flashback and undo retention, type mapping table, Oracle on RDS and Autonomous Database), *Converting PL/SQL to Couchbase UDFs* (ratings, what converts, `odb_plsql`, editing, deploying, side-by-side verification, AI assistance and its data notice). Every command in the guides is tested (`docs_commands.sh`).
- Upgrade notes 3.0 → 4.0 (new collections, provisioning step, no mapping changes).

**Out:** new features.

## Acceptance criteria & unit tests

### AC1 — Parity
**Given** the golden mappings, **then** Oracle runs match PostgreSQL runs on all three target kinds.

### AC2 — E2E
**Given** the CI E2E stack with Oracle Free, **then** both specs pass.

### AC3 — Gates
**Given** a reference-environment run, **then** every 4.0 gate passes.

| Test | Location | Verifies |
|---|---|---|
| `TestParity_AC1_OracleAllTargets` | `internal/pipeline/oracle_parity_integration_test.go` | AC1 |
| `oracle-migration.spec.ts`, `functions-side-by-side.spec.ts` | `web/tests/e2e/` | AC2 |
| `make bench-gate` | `bench/gates-4.0.0.json` | AC3 |
