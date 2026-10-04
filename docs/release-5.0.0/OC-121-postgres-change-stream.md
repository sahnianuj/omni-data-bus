# OC-121 — PostgreSQL change stream: slot snapshot handoff and LSN checkpoints

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E17 PostgreSQL Change Capture | P0 | 8 | OC-119, OC-120 | Not started |

## User story
As a **data engineer**, I want my PostgreSQL changes streamed into the target from the exact point the initial load read, so that nothing is missed or doubled and the target follows the source within seconds.

## Scope
**In:** `ChangeSource`/`ChangeStream` for PostgreSQL with `pglogrepl`:
- `Prepare`: create (or adopt, if the DBA created it) publication `odb_<replicationId>` for every table the mappings read (`publish_via_partition_root = true` on 13+); create logical slot `odb_<replicationId>` with `pgoutput`, `EXPORT_SNAPSHOT`, and `failover = true` on 17+. The exported snapshot drives the initial load through the existing OC-024 read session ([D2](00-architecture.md#d2--the-initial-load-and-the-stream-meet-exactly)); the replication connection stays idle until every partition reader has imported it.
- `Open`: `START_REPLICATION` from the checkpoint LSN (`proto_version` 1, `publication_names`); decode `Relation`, `Insert`, `Update`, `Delete`, `Truncate`, `Begin`, `Commit` into `ChangeEvent`s with positions and commit LSNs; TOAST-unchanged values marked as absent.
- Standby status updates every 10 s and on ack: `flush` = the engine's low watermark; when nothing is pending, advance to the server's WAL end from keepalives (idle-database fix, §6).
- Reconnect with backoff on network errors; `replication slot "…" does not exist` → `needs_resync` with the reason.
- `Drop`: drop slot and publication if `createdByTool`; otherwise leave them and say so.

**Out:** drift (OC-122), monitoring UI (OC-123).

## Acceptance criteria & unit tests

### AC1 — Exact handoff
**Given** 10 concurrent writers during the initial load of the golden mappings, **then** the target equals a fresh migration of the final state (convergence check), and the stream's first event has an LSN after the slot's consistent point.

### AC2 — Lag
**Given** 500 changes/s on the golden `orders` mapping, **then** p95 commit-to-write lag stays under 5 s for 10 minutes.

### AC3 — Restart safety
**Given** PostgreSQL restarted mid-stream, and separately the worker killed, **then** the stream resumes from the confirmed LSN and the convergence check passes.

### AC4 — Idle advance
**Given** no changes in published tables for 10 minutes while another table is written heavily, **then** the slot's `confirmed_flush_lsn` keeps advancing.

### AC5 — Clean stop
**Given** a stopped replication, **then** the slot and publication no longer exist.

| Test | Location | Verifies |
|---|---|---|
| `TestStream_AC1_Handoff` | `internal/connectors/source/postgres/cdc/stream_integration_test.go` | AC1 |
| `TestStream_AC2_Lag` | same (tagged `slow`) | AC2 |
| `TestStream_AC3_Restart` | same | AC3 |
| `TestStream_AC4_IdleAdvance` | same | AC4 |
| `TestStream_AC5_Drop` | same | AC5 |
