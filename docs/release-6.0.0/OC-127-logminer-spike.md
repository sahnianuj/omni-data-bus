# OC-127 — Oracle LogMiner spike

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E18 Oracle Change Capture | P0 | 3 | OC-091 | Not started |

## User story
As an **engineer**, I want LogMiner proven from Go against real Oracle databases, so that the Oracle change reader is built on confirmed behavior rather than on documentation.

## Scope
**In:** a prototype and findings note (`docs/release-6.0.0/spikes/logminer.md`) that updates [§3](00-architecture.md#3-logminer-facts-that-shape-the-design):

| # | Question |
|---|---|
| 1 | Oracle Free container: enable archive-log mode and supplemental logging in a test setup script; startup time |
| 2 | Mining loop from go-ora: add logs for an SCN range, `START_LOGMNR` with `DICT_FROM_ONLINE_CATALOG`, read `V$LOGMNR_CONTENTS` filtered by owner/table, `END_LOGMNR`; window cost vs size |
| 3 | Key extraction: parse keys and join columns from `SQL_REDO`/`SQL_UNDO` for every data type used in keys (`NUMBER`, `VARCHAR2` with quotes, `DATE`/`TIMESTAMP` literals, `RAW`) |
| 4 | Transactions: interleaving, partial rollbacks, a 1M-row transaction (memory, spill), transactions open across the start SCN |
| 5 | DDL rows and `LAST_DDL_TIME` as drift signals |
| 6 | Amazon RDS for Oracle: `rdsadmin` procedures, privileges, retention (if an account is available) |
| 7 | Autonomous Database: is `DBMS_LOGMNR` available? If not, is flashback-version polling viable as a fallback? |
| 8 | Throughput and source CPU at 1,000 changes/s |

**Out:** production code.

## Acceptance criteria

### AC1 — Handoff proven
**Given** an initial load `AS OF SCN s` with concurrent writers including a transaction open across `s`, **then** the prototype's target equals a fresh migration of the final state.

### AC2 — Every question answered
**Given** the note, **then** each question has an answer, evidence, and the story it changes; NFRs in §6 are confirmed or revised with the user.

| Test | Location | Verifies |
|---|---|---|
| Spike findings review | `docs/release-6.0.0/spikes/logminer.md` | AC1, AC2 |
