# OC-123 — Replication monitor

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E16 Continuous Replication | P0 | 8 | OC-121 | Not started |

## User story
As a **data engineer**, I want one page that tells me whether my replication is healthy, how far behind it is, what it's doing and what went wrong, with the controls next to it, so that I can run it for weeks with confidence and plan the cut-over.

## Scope
**In:**
- **Replication page** (a project's Migrate area shows it when a replication exists; also listed on the Jobs page with a "Replication" badge):
  - Status header (single line): state badge, uptime, controls (Pause, Resume, Resync…, Stop…), lag summary.
  - Stat tiles: lag (time, and source position distance: bytes for PostgreSQL), changes/s, documents written/deleted/s, refetches/s, fast-path share, errors.
  - Lag and throughput sparklines (last hour; reusing `ThroughputSparkline`).
  - Source health card: slot WAL retained vs budget (gauge with warn/pause thresholds), slot active, last confirmed position.
  - Per-mapping table: initial-load progress, then streaming state, last change applied, stale relations (fan-out cap), needs-resync flags.
  - **Recent changes** feed (sampled from `replication_events`): table, operation, key, documents affected, latency; filter by table.
  - Errors tab reusing the job errors view, with Retry failed.
- Live updates over the existing SSE channel (OC-027).
- **Cut-over helper**: a "ready to cut over" indicator (all mappings streaming, lag under threshold, no errors) and a **Stop after catching up** action: once the customer has stopped writing to the source, it waits until lag has been zero for N seconds, then stops.

**Out:** alerts and safety automation (OC-124).

## Acceptance criteria & unit tests

### AC1 — Live status
**Given** a streaming replication with a change generator, **then** the page's tiles and sparklines update at least every 2 s and the lag value matches `replication_state` within 1 s.

### AC2 — Initial load then stream
**Given** a new replication, **then** the per-mapping table shows load progress during `initial_load` and switches to streaming per mapping.

### AC3 — Stop after catching up
**Given** writers stopped and lag above zero, **when** the user chooses Stop after catching up, **then** the replication stops only after lag has been zero for the configured time, and the final report states the last applied position.

| Test | Location | Verifies |
|---|---|---|
| `ReplicationPage.test.tsx` › `OC-123 AC1/AC2` | `web/src/features/replication/ReplicationPage.test.tsx` | AC1, AC2 |
| `TestCutover_AC3` | `internal/replication/cutover_test.go` | AC3 |
| `replication.spec.ts` | `web/tests/e2e/replication.spec.ts` | AC1–AC3 end to end |
