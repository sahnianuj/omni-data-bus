# Omni-Data-Bus 6.0.0 — Architecture & Technical Decisions

This document is the shared reference for every story in `docs/release-6.0.0`. It is a **delta** on [the 5.0.0 architecture](../release-5.0.0/00-architecture.md) (continuous replication engine) and [the 4.0.0 architecture](../release-4.0.0/00-architecture.md) (Oracle source). Story IDs continue from 5.0 (OC-127 onwards).

---

## 1. Release summary

| # | Capability | What the user gets | Stories |
|---|---|---|---|
| 1 | **Oracle continuous replication** | The 5.0 replication (initial load, then changes as they commit, monitor, safety, resync, headless runner) with an Oracle source, using Oracle LogMiner: no extra Oracle license, no agent on the database host | OC-127 – OC-133, OC-135 |
| 2 | **External change feed** | Customers who already run Debezium or Oracle GoldenGate can feed their change topics into the same engine | OC-134 |

Nothing in the engine changes: Oracle implements the 5.0 `ChangeSource`/`ChangeStream` ports. Re-materialization (5.0 D3) is what makes Oracle CDC practical: we only need **which keys changed**, never the column values, so LogMiner's weak spots (LOBs, `LONG`, object types, XML) don't matter.

---

## 2. Approaches considered

| Option | What it needs | Verdict |
|---|---|---|
| **LogMiner** (`DBMS_LOGMNR` + `V$LOGMNR_CONTENTS`) via SQL ✅ | Archive-log mode, supplemental logging, `LOGMINING` privilege. Part of every edition, including SE2 and RDS for Oracle | **Chosen.** Pure SQL over go-ora, so it fits the static build (4.0 D1). What Debezium uses by default |
| XStream Out | OCI client libraries (cgo) and a GoldenGate license | Rejected: breaks `CGO_ENABLED=0` and needs a license most customers lack |
| Oracle GoldenGate | Licensed product the customer operates | Not ours to run; supported through the **external feed** (OC-134) |
| Debezium + Kafka | JVM, Kafka Connect, Kafka | Not the default (5.0 §3 D); supported through the external feed |
| Triggers writing a change table | DDL on customer tables | Rejected: violates "we never change the customer's database" (4.0 D4), with write overhead on every transaction |
| Flashback version queries (`VERSIONS BETWEEN SCN`) polling | Undo retention covering the poll interval; per-table queries | Rejected as primary: misses changes beyond undo retention and costs a query per table per poll. Possible fallback for databases without archive-log mode, decided in the spike |

---

## 3. LogMiner facts that shape the design

| Topic | Fact | Consequence |
|---|---|---|
| Continuous mine | `DBMS_LOGMNR.CONTINUOUS_MINE` was removed in 19c | We manage the log list ourselves: for each mining window, add the online redo and archived logs covering `[startScn, endScn]` from `V$LOG`/`V$ARCHIVED_LOG`, `START_LOGMNR`, read, `END_LOGMNR` |
| Dictionary | `DICT_FROM_ONLINE_CATALOG` is cheap but reflects the **current** table definitions | DDL is detected from `OPERATION = 'DDL'` rows and the mapped tables' `LAST_DDL_TIME`; a mapped table's DDL pauses with `SCHEMA_DRIFT` (as in 5.0 OC-122) |
| Uncommitted data | `V$LOGMNR_CONTENTS` returns changes as they are logged, interleaved across transactions, plus `COMMIT`/`ROLLBACK` rows. `COMMITTED_DATA_ONLY` buffers in the database's PGA | We buffer **keys only** per `XID` on our side (spilling to disk past a limit) and emit them at `COMMIT`; `ROLLBACK` discards. Partial rollbacks (`ROLLBACK = 1` rows) are handled by `RS_ID`/`SSN` |
| Key values | `SQL_REDO`/`SQL_UNDO` hold literal statements; with supplemental logging of primary-key columns, an update or delete's `WHERE` clause always includes the key | Extract key (and foreign-key) values by parsing the `WHERE`/`VALUES` clauses for the needed columns only. For the impact resolver (5.0 OC-117) we also need join columns, so table-level supplemental logging covers them (§5 D2) |
| Row identity | `ROW_ID` is present for heap tables | Fallback refetch by `ROWID` for inserts/updates when key parsing fails; not usable for deletes or IOTs |
| Redo threads (RAC) | One redo thread per instance; SCNs are global | Mine all threads, merged by SCN (P1, OC-133) |
| Archived-log retention | Logs deleted by RMAN or RDS retention before we read them create a **gap** | Gap = `needs_resync` (OC-131); readiness checks the retention setting against expected downtime |
| Load on the source | Mining uses the source's CPU and PGA; long windows cost more | Window size adapts to lag; mining from a standby is P1 (OC-133) |
| Amazon RDS for Oracle | Supplemental logging and archive retention through `rdsadmin.rdsadmin_util` procedures; LogMiner available to the master user's grants | Provider-specific snippets (OC-128) |
| Autonomous Database | LogMiner availability to be confirmed | Spike (OC-127); if unavailable, offered through the external feed only |

---

## 4. Position and handoff

- A position is `(commitScn, scn, rsId, ssn)`, totally ordered; the checkpoint low watermark (5.0 D4) is a commit SCN.
- Handoff (5.0 D2): the 4.0 initial load already reads `AS OF SCN s`. Mining starts at `s`, and only transactions committing **after** `s` are emitted. A transaction that started before `s` and commits after it is still found by starting the first window at the oldest open transaction's start SCN (`V$TRANSACTION.START_SCN`, captured with `s`).
- Restart: resume mining from `min(start SCN of transactions open at the checkpoint, checkpoint SCN)`; transactions committed at or below the checkpoint are skipped.

---

## 5. Key decisions

### D1 — LogMiner reader inside the Oracle connector
`internal/connectors/source/oracle/cdc` implements `ChangeSource` and `ChangeStream` (5.0 §4). No separate process, no agent on the database host.

### D2 — Supplemental logging: minimal + primary key + mapped join columns
The database needs minimal supplemental logging. Each mapped table needs `SUPPLEMENTAL LOG DATA (PRIMARY KEY) COLUMNS`, plus a supplemental log group with the columns the impact plan reads from changes (foreign keys and group-by columns). That is narrower than `(ALL) COLUMNS`, so redo volume grows less. The readiness assistant generates exactly these statements; the tool never runs them (4.0 D4).

### D3 — Keys, not values
Events carry the old and new key and join columns only; values are always re-read (5.0 D3 re-materialization, inside one `AS OF SCN` read per window, at the window's last commit SCN, so each window sees a consistent state). The flat fast path is not used for Oracle in 6.0.

### D4 — Lag is measured in time and SCN
Lag = now − commit time of the last applied transaction (from `V$LOGMNR_CONTENTS.COMMIT_TIMESTAMP`), and SCN distance to `CURRENT_SCN`. The monitor shows archived-log retention headroom in hours, where PostgreSQL shows retained WAL bytes.

---

## 6. Non-functional requirements
- ≥ 1,000 row changes/s sustained for the golden mappings on the reference Oracle environment, p95 lag under 10 s.
- Source overhead: mining CPU under one core at that rate (measured and reported, gate set by the spike).
- No lost changes across worker `kill -9`, database restart, log switch storms and large transactions (1M rows in one transaction, buffered within the memory limit plus spill).

## 7. Definition of Done additions
- Integration tests run on `gvenzl/oracle-free` with archive-log mode and supplemental logging enabled by the test setup (`make test-integration-oracle-cdc`).
- The 5.0 convergence check runs for every golden mapping with an Oracle source.
