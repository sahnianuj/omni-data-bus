# OC-130 — Oracle replication end to end: SCN handoff from the initial load

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E18 Oracle Change Capture | P0 | 5 | OC-129, OC-099, OC-119 | Not started |

## User story
As a **data engineer**, I want to choose Replicate continuously for an Oracle project and have it load, then stream, with no gap and no duplicates, so that Oracle projects get the same replication as PostgreSQL ones.

## Scope
**In:**
- `ChangeSource.Prepare` for Oracle: capture `CURRENT_SCN` and the oldest open transaction's start SCN together; hand the SCN to the 4.0 initial load (`AS OF SCN`); store both in the handoff ([§4](00-architecture.md#4-position-and-handoff)). No server-side objects are created, so `Drop` is a no-op apart from state.
- Re-materialization reads per window `AS OF SCN` the window's last commit SCN ([D3](00-architecture.md#d3--keys-not-values)), falling back to current reads with a warning when flashback isn't granted.
- Migrate step offers Replicate for Oracle sources once readiness passes.
- Side-by-side function runner (4.0 OC-110): the "as of" option gains **as of the replication** (the applied SCN).

**Out:** monitoring specifics (OC-132).

## Acceptance criteria & unit tests

### AC1 — Exact handoff
**Given** 10 concurrent writers during the initial load, including a long transaction open across the start SCN, **then** the convergence check passes and no change is applied twice (counted).

### AC2 — Restart
**Given** the worker killed during streaming and Oracle restarted (separately), **then** streaming resumes from the checkpoint and the convergence check passes.

### AC3 — Function runner alignment
**Given** a streaming replication, **then** the side-by-side runner's "as of the replication" runs the Oracle side at the applied SCN and matches the UDF on rows changed after the initial load.

| Test | Location | Verifies |
|---|---|---|
| `TestOracleReplication_AC1_Handoff` | `internal/replication/oracle_integration_test.go` | AC1 |
| `TestOracleReplication_AC2_Restart` | same | AC2 |
| `TestRunner_AC3_AsOfReplication` | `internal/plsql/compare/runner_integration_test.go` | AC3 |
