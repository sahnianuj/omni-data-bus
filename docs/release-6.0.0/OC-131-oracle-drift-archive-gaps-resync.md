# OC-131 — Oracle schema drift, archive-log gaps and resync

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E18 Oracle Change Capture | P0 | 5 | OC-130, OC-122 | Not started |

## User story
As a **data engineer**, I want an Oracle replication to stop safely when a mapped table's structure changes or redo is missing, and to help me resync, so that DDL and log housekeeping don't silently break the target.

## Scope
**In:**
- Drift from `OPERATION = 'DDL'` rows on mapped tables and from `LAST_DDL_TIME` checks each minute: compare the table's current definition with the mappings' discovery snapshot; same rules and resolution flow as 5.0 OC-122 (added column info; dropped/renamed/retyped mapped column or key change → pause `SCHEMA_DRIFT`).
- Supplemental logging dropped on a mapped table (checked each minute) → pause `SUPPLEMENTAL_LOGGING_MISSING` with the snippet, because the changes since then may lack keys.
- Archive-log gaps (OC-129) and retention headroom: warn when the oldest unmined log is within 2 hours of the retention limit (where known).
- Resync per mapping or all, reusing 5.0 resync with a fresh SCN (stale-document deletion included).
- `TRUNCATE` (a DDL row) → the replication's truncate policy.
- Partition maintenance (`EXCHANGE`, `DROP PARTITION`, `TRUNCATE PARTITION`) on a mapped table → pause with `PARTITION_DDL` and offer resync of the affected mappings, since these change rows without row-level redo.

**Out:** —

## Acceptance criteria & unit tests

### AC1 — Drift
**Given** a mapped column dropped during streaming, **then** the replication pauses with `SCHEMA_DRIFT` before applying any later change to that table.

### AC2 — Partition DDL
**Given** `ALTER TABLE … TRUNCATE PARTITION` on a mapped table, **then** the replication pauses with `PARTITION_DDL`, and a resync of that mapping makes the convergence check pass.

### AC3 — Supplemental logging removed
**Given** a DBA drops primary-key supplemental logging on a mapped table, **then** within 1 minute the replication pauses with `SUPPLEMENTAL_LOGGING_MISSING`.

| Test | Location | Verifies |
|---|---|---|
| `TestOracleDrift_AC1` | `internal/connectors/source/oracle/cdc/drift_integration_test.go` | AC1 |
| `TestOracleDrift_AC2_PartitionDDL` | same | AC2 |
| `TestOracleDrift_AC3_SupplementalLogging` | same | AC3 |
