# OC-132 — Oracle replication monitoring

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E18 Oracle Change Capture | P0 | 3 | OC-130, OC-123, OC-124 | Not started |

## User story
As a **data engineer and DBA**, I want the replication page to show Oracle-specific health (SCN lag, archive-log headroom, mining cost), so that we can run Oracle replication with the same confidence as PostgreSQL.

## Scope
**In:**
- Source health card for Oracle ([D4](00-architecture.md#d4--lag-is-measured-in-time-and-scn)): lag in time and SCN distance, oldest unmined archived log and retention headroom in hours, current window size, open transactions buffered (count, oldest age, spilled bytes), key-parse fallbacks to `ROWID`.
- Mining cost: CPU and PGA used by the mining session (`V$SESSTAT`/`V$PROCESS` when readable), shown and exported as metrics.
- Safety guard (5.0 OC-124) adapted: thresholds on retention headroom (warn at 4 h, pause at 1 h) instead of WAL bytes; a long-open transaction older than a threshold (default 1 h) raises a warning, because it holds mining back.
- Prometheus metrics: `odb_replication_scn_lag`, `odb_replication_archive_headroom_seconds`, `odb_replication_open_transactions`.

**Out:** —

## Acceptance criteria & unit tests

### AC1 — Health card
**Given** a streaming Oracle replication, **then** the card shows SCN lag, headroom and buffered transactions, and they update live.

### AC2 — Long transaction warning
**Given** a transaction left open for longer than the threshold, **then** a warning names its `XID`, start time and session (when visible).

| Test | Location | Verifies |
|---|---|---|
| `ReplicationPage.test.tsx` › `OC-132` | `web/src/features/replication/ReplicationPage.test.tsx` | AC1 |
| `TestOracleGuard_AC2_LongTx` | `internal/replication/guard/oracle_integration_test.go` | AC2 |
