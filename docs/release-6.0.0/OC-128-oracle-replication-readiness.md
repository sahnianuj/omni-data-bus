# OC-128 — Oracle replication readiness: checks and setup assistant

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E18 Oracle Change Capture | P0 | 5 | OC-127, OC-094, OC-120 | Not started |

## User story
As a **data engineer**, I want the tool to tell me exactly what my Oracle database needs for continuous replication, and give the DBA the statements, so that we get it ready in one pass.

## Scope
**In:** `ChangeSource.Readiness` for Oracle, reusing the 5.0 setup assistant UI:
1. Archive-log mode (`V$DATABASE.LOG_MODE = 'ARCHIVELOG'`) — fail with the procedure (needs a restart; never done by the tool).
2. Minimal supplemental logging (`SUPPLEMENTAL_LOG_DATA_MIN`).
3. Per mapped table: primary-key supplemental logging and a log group with the impact plan's join columns ([D2](00-architecture.md#d2--supplemental-logging-minimal--primary-key--mapped-join-columns)); tables without a key need `(ALL) COLUMNS` (warn: more redo).
4. Privileges: `LOGMINING` (12c+), `EXECUTE ON DBMS_LOGMNR`, `SELECT` on `V_$DATABASE`, `V_$LOG`, `V_$LOGFILE`, `V_$ARCHIVED_LOG`, `V_$LOGMNR_CONTENTS`, `V_$LOGMNR_LOGS`, `V_$TRANSACTION`, and `FLASHBACK` on the mapped tables.
5. Archived-log retention vs the configured maximum downtime (RMAN deletion policy unknown → warn with explanation; RDS `archivelog retention hours`).
6. Multitenant: connected to the PDB; mining runs in the PDB on 19c+ (confirmed by the spike) or needs a common user in `CDB$ROOT` (snippet).
- Provider detection (RDS for Oracle via `rdsadmin` presence) with `rdsadmin.rdsadmin_util.alter_supplemental_logging` and `set_configuration('archivelog retention hours', …)` snippets.
- One combined script for the DBA; Re-check.

**Out:** mining (OC-129).

## Acceptance criteria & unit tests

### AC1 — Checks
**Given** the Oracle Free container in `NOARCHIVELOG`, then with archive-log mode but no supplemental logging, then ready, **then** checks fail on 1, fail on 2–3, then pass.

### AC2 — Exact script
**Given** two mapped tables, one with a foreign key used by an embed, **then** the script adds primary-key logging to both and a log group with exactly that foreign key column.

| Test | Location | Verifies |
|---|---|---|
| `TestReadiness_AC1` | `internal/connectors/source/oracle/cdc/readiness_integration_test.go` | AC1 |
| `TestReadiness_AC2_Script` | same | AC2 |
