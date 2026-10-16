# OC-146 — Exact row counts for small tables

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E12 Harden & Ship 2.0 | P1 | 3 | OC-010, OC-011 | Implemented |

## User story
As a **data engineer** exploring a source database, I want small tables to show their real row count instead of `~0`, so that I can trust the numbers in the schema tree and the Tables step.

## Background (2026-10-15)
Discovery showed PostgreSQL's planner estimate (`pg_class.reltuples`). That estimate only changes when a table is analyzed, and autovacuum re-analyzes a table only after about 50 changed rows. A table analyzed while empty and then loaded with a few rows kept showing `~0` indefinitely, which reads as "empty".

## Scope
**In:**
- Discovery counts tables of up to 1 MiB of heap exactly (`count(*)`), smallest first, within a 64 MiB budget per discovery. Counts run in batches of up to 200 tables per statement, in the same read-only snapshot as the catalog reads.
- Counting waits at most 2 s for a table lock. On any error the remaining tables keep their estimates, and discovery still succeeds.
- Larger tables get the planner's own estimate: reltuples scaled to the heap's current size. When the statistics predate the data (reltuples ≤ 0 but the heap has pages), the statistics collector's live-row count is used.
- A new `rowCountExact` flag on tables (API and snapshot). The schema tree, the table header and the Tables step drop the `~` when it is set.
- A partitioned table's count is exact only when every partition was counted.

**Out:** exact counts for large tables (they stay estimates); refreshing counts without re-running discovery.

## Acceptance criteria & unit tests

### AC1 — Small tables are counted
**Given** a table analyzed while empty that now holds 20 rows, **when** discovery runs, **then** it shows `20` with no `~`.

### AC2 — Estimates follow the table's size
**Given** a table whose statistics were taken at 1,000 rows over 10 pages and that now has 40 pages, **then** its estimate is ~4,000. **Given** statistics that predate the data, **then** the live-row count is used.

### AC3 — Counting never breaks discovery
**Given** a count that fails (a lock timeout, say), **then** discovery succeeds and the uncounted tables keep their estimates.

| Test | Location | Verifies |
|---|---|---|
| `TestSetEstimate_StaleStatisticsAndExactCounts` | `backend/internal/connectors/source/postgres/discover_test.go` | AC1, AC2, partitions |
| `TestCountSmallTables` | same | Batching, skipped tables, AC3 |
| `SchemaTree.test.tsx` › `shows exact counts without the estimate marker` | `web/src/features/discovery/SchemaTree.test.tsx` | UI |
| `TableDetail.test.tsx` › `shows an exact count without the estimate marker` | `web/src/features/discovery/TableDetail.test.tsx` | UI |
