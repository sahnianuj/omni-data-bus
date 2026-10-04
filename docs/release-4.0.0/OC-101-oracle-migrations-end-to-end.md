# OC-101 — Oracle migrations end to end: resume, verification, export and odb run

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 5 | OC-099, OC-100 | Not started |

## User story
As a **data engineer**, I want an Oracle migration to have everything a PostgreSQL migration has (preflight, live progress, pause and resume, error retries, verification report, exported bash runner, appliance), so that Oracle is a first-class source and not a demo.

## Scope
**In:**
- Preflight (OC-078) for Oracle: privileges on every mapped table, flashback rights, undo retention vs estimated read time (warn when the estimate exceeds `UNDO_RETENTION`), LOB columns over the size guard.
- Verification report (OC-030): counts at the job's SCN, sample comparison.
- Portable bundle and `odb run` (OC-050/051): the bundle carries the Oracle connection (wallet sealed with the other secrets), and `odb run` executes it. Bash runner tests on Linux and macOS cover an Oracle bundle.
- All-in-one appliance (OC-072): reaches an Oracle on the host (`host.docker.internal`), and the README has an Oracle example.
- Job options: `fetchSize` and per-table reader count, with Oracle defaults.

**Out:** benchmarks and guides (OC-114).

## Acceptance criteria & unit tests

### AC1 — End to end in the UI
**Given** the Oracle seed and a Couchbase target, **when** a user creates a project, maps `ORDERS` with embedded `ORDER_ITEMS` and runs it, **then** the job completes, the verification report passes, and the Output tab shows the documents.

### AC2 — Exported runner
**Given** that project exported as a bundle, **when** `migrate.sh` runs it on Linux and macOS against the same Oracle, **then** the documents match the in-app run.

### AC3 — Preflight undo warning
**Given** `UNDO_RETENTION` of 60 s and a table estimated to take 10 minutes, **then** preflight shows the warning with the suggested setting.

| Test | Location | Verifies |
|---|---|---|
| `oracle-migration.spec.ts` | `web/tests/e2e/oracle-migration.spec.ts` | AC1 |
| `runner_oracle.bats` | `backend/internal/export/testdata/bats/runner_oracle.bats` | AC2 |
| `TestPreflight_AC3_UndoRetention` | `internal/jobs/preflight_oracle_test.go` | AC3 |
