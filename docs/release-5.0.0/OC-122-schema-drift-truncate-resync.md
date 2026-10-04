# OC-122 — Schema drift, truncate and resync

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E17 PostgreSQL Change Capture | P0 | 5 | OC-121 | Not started |

## User story
As a **data engineer**, I want the replication to notice when a source table's structure changes, stop before writing wrong documents, and help me fix and resync, so that a DDL change on a Friday night doesn't silently corrupt my target.

## Scope
**In:**
- Drift detection from `Relation` messages compared with the discovery snapshot the mappings were validated against:
  - Added column → info event (not mapped; nothing to do).
  - Dropped or renamed mapped column, changed type of a mapped column, changed key → pause with `SCHEMA_DRIFT`, naming the table, columns and affected mappings.
- Guided resolution: re-discover the table, show the diff (reusing the OC-010 diff sheet), open the affected mappings with their validation issues, then **Resync** those mappings.
- **Resync** (also callable any time): per mapping or all; a new initial load of those mappings at a fresh exported snapshot from a temporary slot, while the main stream keeps running for the others. Documents from the resync are upserted and stale ones deleted (keys present in the target but not in the source, found via the target's key listing — Couchbase range scan, directory manifest, Edge listing).
- Truncate policy (from OC-119) configurable per replication.
- `needs_resync` cases from OC-121 (slot lost) offer a full resync with a new slot.

**Out:** Oracle DDL (6.0).

## Acceptance criteria & unit tests

### AC1 — Drift pauses
**Given** a streaming replication, **when** a mapped column is renamed, **then** the replication pauses with `SCHEMA_DRIFT` before writing any change from after the DDL, and names the column and mapping.

### AC2 — Resync one mapping
**Given** two mappings, **when** one is resynced, **then** the other keeps streaming (lag stays under 5 s) and afterwards the convergence check passes for both.

### AC3 — Stale documents removed
**Given** documents in the target whose source rows were deleted while the slot was lost, **when** a full resync runs, **then** those documents are deleted.

| Test | Location | Verifies |
|---|---|---|
| `TestDrift_AC1_Pause` | `internal/replication/drift_integration_test.go` | AC1 |
| `TestResync_AC2_OneMapping` | `internal/replication/resync_integration_test.go` | AC2 |
| `TestResync_AC3_StaleDeleted` | same | AC3 |
