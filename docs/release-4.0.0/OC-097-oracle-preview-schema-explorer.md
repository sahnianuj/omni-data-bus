# OC-097 — Oracle data preview in the schema explorer

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 3 | OC-095, OC-096 | Not started |

## User story
As a **data engineer**, I want to browse Oracle tables and see sample rows in the schema explorer, so that I understand the data before I map it.

## Scope
**In:**
- `Preview` for Oracle: first rows in key order (`FETCH FIRST n ROWS ONLY`), random samples (`SAMPLE (p)` for large tables, `ORDER BY DBMS_RANDOM.VALUE` for small ones, chosen by row estimate as for PostgreSQL), `DistinctOn` for grouped driving tables (`ROW_NUMBER() OVER (PARTITION BY …)`).
- Timeouts by context cancellation (default 5 s), returning `ErrQueryTimeout`.
- `KeyCounter` (virtual relationship cardinality, OC-042) and exact counts.
- Explorer UI shows Oracle type names (`VARCHAR2(100)`, `NUMBER(10,2)`) and the flags from OC-095 (identity, virtual, partitioned, stale statistics with a "Count exactly" button).

**Out:** reading for migration (OC-099).

## Acceptance criteria & unit tests

### AC1 — Preview modes
**Given** the seed's `CUSTOMERS` table (10k rows), **then** first-rows preview returns rows in primary-key order, random preview returns 50 rows that differ between two calls, and `DistinctOn` returns one row per group.

### AC2 — Timeout
**Given** a preview on a view that sleeps (`DBMS_SESSION.SLEEP` in a function), **then** the call returns `ErrQueryTimeout` after about 5 s and the session is still usable.

### AC3 — Explorer
**Given** an Oracle project, **then** the explorer lists Oracle type names and flags, and "Count exactly" replaces a stale estimate.

| Test | Location | Verifies |
|---|---|---|
| `TestPreview_AC1_Modes` | `internal/connectors/source/oracle/preview_integration_test.go` | AC1 |
| `TestPreview_AC2_Timeout` | same | AC2 |
| `SchemaExplorerPage.test.tsx` › `OC-097 AC3` | `web/src/features/discovery/SchemaExplorerPage.test.tsx` | AC3 |
