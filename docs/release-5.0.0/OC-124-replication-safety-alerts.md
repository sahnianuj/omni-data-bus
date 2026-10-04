# OC-124 — Replication safety: WAL retention guard, alerts and clean stop

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E16 Continuous Replication | P0 | 5 | OC-121, OC-123 | Not started |

## User story
As a **DBA of the source database**, I want the replication tool to never fill my primary's disk with retained WAL, and to warn people before anything goes wrong, so that allowing continuous replication is safe for production.

## Scope
**In:**
- **WAL retention guard** (runs on the leader node even when no worker holds the replication, so a dead worker can't hide a growing slot): measures retained WAL for every slot the tool created, every 30 s.
  - Warn threshold (default 5 GB) → warning event and alert.
  - Pause threshold (default 20 GB) → the replication is paused and the alert says why. The slot is kept.
  - Drop threshold (default off; owner-configurable, e.g. 50 GB) → the slot is dropped and the replication goes to `needs_resync`.
  - A replication paused longer than `maxPausedHours` (default 24) gets the warning even below thresholds.
- **Orphan slot detection**: slots named `odb_*` with no matching replication (for example after a metadata restore) are listed on the Settings page, with Drop.
- **Alerts**: in-app notifications for the project's editors, Prometheus metrics (`odb_replication_lag_seconds`, `odb_replication_wal_retained_bytes`, `odb_replication_state`, changes/documents counters), example alert rules in the Helm chart, and an optional webhook per workspace (JSON payload, retried, signed with HMAC).
- **Clean stop and delete**: stopping or deleting a project with a replication drops the server-side objects it created (confirm dialog lists them); failures are retried by the guard.

**Out:** email delivery.

## Acceptance criteria & unit tests

### AC1 — Pause on WAL budget
**Given** a paused worker and a writer generating WAL, **when** retained WAL passes the pause threshold, **then** the replication is paused by the guard with reason `WAL_BUDGET_EXCEEDED` and an alert is raised.

### AC2 — Drop policy
**Given** the drop threshold set, **when** it is passed, **then** the slot no longer exists and the replication is `needs_resync`.

### AC3 — Orphans
**Given** an `odb_*` slot without a replication, **then** it is listed, and Drop removes it.

### AC4 — Webhook
**Given** a webhook URL, **then** state changes and threshold alerts are delivered with a valid signature, and retried on 500.

| Test | Location | Verifies |
|---|---|---|
| `TestGuard_AC1_Pause` | `internal/replication/guard/guard_integration_test.go` | AC1 |
| `TestGuard_AC2_Drop` | same | AC2 |
| `TestGuard_AC3_Orphans` | same | AC3 |
| `TestWebhook_AC4` | `internal/replication/alerts/webhook_test.go` | AC4 |
