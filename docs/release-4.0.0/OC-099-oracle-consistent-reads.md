# OC-099 — Oracle consistent reads: one SCN, keyset partitions, ROWID chunks

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 8 | OC-093, OC-096 | Not started |

## User story
As a **data engineer**, I want large Oracle tables read in parallel, consistently and resumably, so that a migration of hundreds of millions of rows finishes fast and survives a crash without losing or duplicating documents.

## Scope
**In:** `SnapshotReader`/`ReadSession` for Oracle per [architecture D2](00-architecture.md#d2--consistency-comes-from-one-scn):
- `BeginRead` takes `CURRENT_SCN` (from `V$DATABASE`, or `DBMS_FLASHBACK.GET_SYSTEM_CHANGE_NUMBER` when `V$` isn't readable). `SnapshotID()` is the SCN. Without flashback rights, a single reader in a `SET TRANSACTION READ ONLY` transaction.
- Keyset partitions: `KeyBounds`, `Count`, `OpenPartition`, `ReadAfter` with expanded row comparisons for multi-column keys, `ORDER BY` on the key with `NLS_SORT=BINARY`, `FETCH FIRST :n ROWS ONLY`, all `AS OF SCN :scn`.
- **Keyless tables** (no primary key or not-null unique key): partitions by `ROWID` ranges (extent-based, as `DBMS_PARALLEL_EXECUTE` does it, computed from `DBA_EXTENTS` when readable, else a single range), resumable from the last `ROWID`. This is better than PostgreSQL's non-resumable cursor; the checkpoint strategy is `rowid`. Index-organized tables always have a key.
- `ReadKeys` (retry of failed rows) and `Resnapshotter`.
- Errors: `ORA-01555` → job pauses with `SOURCE_SNAPSHOT_TOO_OLD` and a message naming `UNDO_RETENTION`. Lost connections → `TransientError` with reconnect at the same SCN. **(CDC)** The SCN is stored in the mapping checkpoint (`snapshot: {kind: "scn", value}`).

**Out:** related-row reads (OC-100).

## Acceptance criteria & unit tests

### AC1 — Consistent across partitions
**Given** a 1M-row table read with 4 partitions while a background session updates and commits rows continuously, **then** the documents reflect exactly the rows at the session's SCN (checked against a `SELECT … AS OF SCN` dump).

### AC2 — Multi-column keyset
**Given** a table with primary key `(REGION VARCHAR2, ID NUMBER)` including mixed-case and accented region values, **then** every row is read once, in byte order, and `ReadAfter` resumes correctly from any batch boundary.

### AC3 — Keyless table resumes
**Given** a 500k-row heap table without a key, **when** the job is killed halfway and resumed, **then** every row is written exactly once and the checkpoint shows strategy `rowid`.

### AC4 — Snapshot too old
**Given** `UNDO_RETENTION` of 1 s and a slowed-down reader, **then** the job pauses with `SOURCE_SNAPSHOT_TOO_OLD`; resuming takes a new SCN and the job log shows the resnapshot warning.

### AC5 — No flashback privilege
**Given** a user without `FLASHBACK`, **then** preflight warns, the table is read with one reader, and the result is still consistent (AC1 check).

| Test | Location | Verifies |
|---|---|---|
| `TestRead_AC1_ConsistentAtSCN` | `internal/connectors/source/oracle/read_integration_test.go` | AC1 |
| `TestRead_AC2_MultiColumnKeyset` | same | AC2 |
| `TestRecovery_AC3_RowidResume` | `internal/pipeline/oracle_recovery_integration_test.go` | AC3 |
| `TestRead_AC4_SnapshotTooOld` | `internal/connectors/source/oracle/read_integration_test.go` | AC4 |
| `TestRead_AC5_NoFlashback` | same | AC5 |
