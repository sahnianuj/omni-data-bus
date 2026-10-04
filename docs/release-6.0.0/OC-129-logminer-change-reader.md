# OC-129 — LogMiner change reader: mining windows, transactions and key extraction

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E18 Oracle Change Capture | P0 | 8 | OC-128 | Not started |

## User story
As a **data engineer**, I want committed Oracle changes read reliably from the redo logs, in commit order, with the keys the engine needs, so that my Couchbase target follows my Oracle database.

## Scope
**In:** `ChangeStream` for Oracle:
- **Mining loop**: windows of `[startScn, min(startScn + windowScn, CURRENT_SCN)]`; log list from `V$LOG` (online, current excluded until switched or mined with care per the spike) and `V$ARCHIVED_LOG`; `START_LOGMNR` with `DICT_FROM_ONLINE_CATALOG`; query `V$LOGMNR_CONTENTS` filtered on the mapped owners and tables plus `COMMIT`/`ROLLBACK`/`DDL` operations; `END_LOGMNR`. Window size adapts to lag (larger when behind, smaller when caught up). Poll interval when caught up: 1 s.
- **Transaction buffer** by `XID`: key events kept until `COMMIT` (emit in commit-SCN order) or `ROLLBACK` (discard); partial rollbacks undo the matching events; buffers above a memory limit (default 256 MB total) spill to the state directory.
- **Key extraction**: a small parser for LogMiner's `SQL_REDO`/`SQL_UNDO` statement shapes (`insert into "O"."T"("C1",…) values (…)`, `update … set … where …`, `delete from … where …`) extracting only the key and join columns, decoding literals per column type (including `TO_DATE`/`TO_TIMESTAMP`/`HEXTORAW` forms). Fallback to `ROW_ID` refetch for inserts/updates when a value can't be parsed (counted, surfaced in the monitor).
- `Ack` stores the low-watermark commit SCN; restart uses [§4](00-architecture.md#4-position-and-handoff).
- Mining session errors (`ORA-01291 missing logfile`, `ORA-01284`) → retry once with a refreshed log list, then `needs_resync` with `ARCHIVE_LOG_GAP`.

**Out:** RAC (OC-133).

## Acceptance criteria & unit tests

### AC1 — Committed changes only
**Given** interleaved transactions, one rolled back and one with a partial rollback to a savepoint, **then** emitted events are exactly the committed rows, in commit order.

### AC2 — Large transaction
**Given** one transaction updating 1M rows, **then** memory stays under the limit plus 64 MB, events spill and are emitted at commit, and the convergence check passes.

### AC3 — Key parsing
**Given** the seed tables with keys of every supported type (including quotes, Unicode and timestamps), **then** the parsed keys equal the rows' keys for 100% of a 100k-change sample.

### AC4 — Gap
**Given** archived logs deleted before they were mined, **then** the replication goes to `needs_resync` with `ARCHIVE_LOG_GAP` naming the missing sequence.

| Test | Location | Verifies |
|---|---|---|
| `TestMiner_AC1_CommittedOnly` | `internal/connectors/source/oracle/cdc/miner_integration_test.go` | AC1 |
| `TestMiner_AC2_LargeTx` | same (tagged `slow`) | AC2 |
| `TestRedoParse_AC3` | `internal/connectors/source/oracle/cdc/redoparse_test.go` + integration sample | AC3 |
| `TestMiner_AC4_Gap` | `internal/connectors/source/oracle/cdc/miner_integration_test.go` | AC4 |
