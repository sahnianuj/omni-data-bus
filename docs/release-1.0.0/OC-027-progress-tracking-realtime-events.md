# OC-027 — Progress tracking & real-time events (SSE)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E5 Migration | P0 | 5 | OC-023, OC-025 | Implemented |

## User story
As a **data engineer**, I want accurate, live progress: how many rows have been read and written, how many failed, the current throughput, and an ETA, updated within a couple of seconds in my browser from any server node, so that I know exactly where my migration stands.

## Implementation details

### Totals (`internal/progress/total.go`)
- At mapping start, the total starts as the **estimate** from `pg_class.reltuples` (OC-010), with `totalKind: "estimate"`.
- When `exactCount` is `always`, or `auto` and `sizeBytes ≤ 5 GB`, a background `SELECT count(*)` runs **inside the shared snapshot** (so it matches exactly what will be read), with a 10-minute timeout. When it finishes, `total` is updated with `totalKind: "exact"`.
- For retry jobs (OC-026), the total is the exact number of keys.
- If `processed > total` (a stale estimate), the total is raised to `processed` and stays `estimate`.

### Counters & rates (`internal/progress/tracker.go`)
```go
type Tracker struct {
    rowsRead, docsWritten, docsSkipped, docsFailed, bytesWritten atomic.Int64
    perMapping map[uuid.UUID]*MappingCounters
    ewma       *EWMA   // docs/s, alpha tuned for ~10s half-life, sampled every 1s
    started    time.Time
}
func (t *Tracker) Snapshot(now time.Time) Snapshot
```
- `processed = written + skipped + failed`.
- `percent = processed / total`. It is capped at 99.9% until the final completion, to avoid showing 100% while tails are still flushing.
- `throughput` (EWMA docs/s) and `peakThroughput`.
- `eta = (total − processed) / ewma`. It is null during the first 5 s or when ewma is 0, and smoothed so it never jumps more than 20% per tick unless the rate changed more than 50%.
- `phase` per mapping: `preparing` (snapshot, count), `migrating`, `verifying` (OC-030), `done`.

**Snapshot payload** (kept ≤ 6 KB so the per-second read stays cheap; the top 5 errorsByCode are included):
```json
{ "jobId": "…", "status": "running", "seq": 184, "ts": "2026-09-25T15:04:05.120Z",
  "total": 1203441, "totalKind": "exact", "processed": 402113, "percent": 33.41,
  "written": 401990, "skipped": 0, "failed": 123, "rowsRead": 410000,
  "throughput": 21450.5, "peakThroughput": 25010.2, "etaSeconds": 37, "elapsedSeconds": 19,
  "mappings": [{ "mappingId": "…", "table": "public.orders", "phase": "migrating", "processed": 402113, "total": 1203441, "percent": 33.41 }],
  "errorsByCode": [{ "code": "PARSE_FAILED", "count": 120 }, { "code": "DOC_TOO_LARGE", "count": 3 }] }
```

### Publishing (`internal/events`)
- The worker ticks every **1 s**:
  1. `UPDATE jobs SET progress = $snapshot` (throttled: persisted every 5 s, or immediately on status change).
  2. KV `Upsert` of `job_progress::<jobId>` with the snapshot (`type: "progress"`), including status changes. New error samples go as `type: "error-sample"` (max 5 per second).
- **The hub on every API node** runs one poller per job that has at least one subscriber. It does a KV `Get` of `job_progress::<jobId>` every 1 s, skips the result when the CAS hasn't changed, and fans out to that node's subscribers by jobId, using an in-memory map of channels. A slow subscriber drops intermediate progress events and only the latest is kept: each subscriber has a 1-slot "latest progress" plus a small queue for status and error events.
- **SSE endpoint** `GET /api/v1/jobs/{id}/events` (auth and workspace check; no timeout middleware):
  - Headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, and `X-Accel-Buffering: no`.
  - On connect it immediately sends the **current snapshot**, from the DB, as `event: progress`.
  - Then it streams events formatted as `id: <seq>\nevent: <type>\ndata: <json>\n\n`.
  - Heartbeat comment `: ping` every 15 s.
  - When the job is terminal, it sends `event: completed` with the final snapshot and closes.
  - `Last-Event-ID` is honored: if it is older than the current seq, the latest snapshot is sent (progress is state, not a log).
- **Project/dashboard multiplex:** `GET /api/v1/events?jobs=a,b,c` streams several jobs over one connection (max 20) for the Dashboard and Jobs list.

### Frontend (`src/lib/sse/useJobEvents.ts`)
- Wraps `EventSource` with `withCredentials`, and merges events into the TanStack Query cache (`['job', id]`), so every component reads one source of truth.
- Reconnects with backoff (EventSource does this natively; the hook adds visibility handling: pause when the tab is hidden for more than 5 min, and on return reconnect and refetch).
- Exposes `{snapshot, status, connection: 'live'|'reconnecting'|'offline'}`.

## Acceptance criteria & unit tests

### AC1 — Counter math
**Given** written 900, skipped 50, failed 50, and total 2,000, **then** processed = 1000 and percent = 50.0. **Given** processed = total but the job isn't finalized, **then** percent = 99.9. **Given** processed > estimate total, **then** total is raised.

| Test | Location | Verifies |
|---|---|---|
| `TestSnapshot_AC1_Percent` | `internal/progress/tracker_test.go` | |
| `TestSnapshot_AC1_CapBeforeFinal` | `internal/progress/tracker_test.go` | |
| `TestSnapshot_AC1_TotalRaisedWhenExceeded` | `internal/progress/tracker_test.go` | |
| `TestTracker_AC1_ConcurrentIncrementsRace` | `internal/progress/tracker_test.go` | `-race`, 64 goroutines |

### AC2 — Throughput & ETA
**Given** a steady 20k docs/s for 30 simulated seconds, **then** the EWMA is within 2% of 20k, and ETA = remaining / 20k. **Given** the first 5 s, **then** ETA is null. **Given** the rate drops 10%, **then** ETA moves at most 20% per tick.

| Test | Location | Verifies |
|---|---|---|
| `TestEWMA_AC2_ConvergesSteadyRate` | `internal/progress/ewma_test.go` | |
| `TestETA_AC2_NullWarmupAndSmoothing` | `internal/progress/eta_test.go` | |

### AC3 — Exact count in snapshot
**Given** `exactCount:auto` and a 1 GB table, **then** count(*) runs in the shared snapshot transaction and `totalKind` switches to `exact`. **Given** 50 GB, **then** it stays an estimate.

| Test | Location | Verifies |
|---|---|---|
| `TestTotals_AC3_AutoPolicy` | `internal/progress/total_test.go` | |
| `TestTotals_AC3_CountUsesSnapshot` | `internal/connectors/source/postgres/readsession_test.go` | Fake conn records `SET TRANSACTION SNAPSHOT` before the count |

### AC4 — Payload size
**Given** a job with 4 mappings and 30 error codes, **then** the serialized snapshot is < 6 KB, with only the top-5 codes included.

| Test | Location | Verifies |
|---|---|---|
| `TestSnapshot_AC4_PayloadBounded` | `internal/progress/tracker_test.go` | |

### AC5 — Cross-node fan-out via the progress document
**Given** the worker on node A publishes, **then** an SSE subscriber on node B receives the event within 2 s. The poller survives transient KV errors (it keeps retrying) and stops when the last subscriber leaves.

| Test | Location | Verifies |
|---|---|---|
| `TestHub_AC5_FanOutByJob` | `internal/events/hub_test.go` | Fake progress reader |
| `TestHub_AC5_PollerRetriesAndStops` | `internal/events/hub_test.go` | |
| `TestHub_AC5_SlowSubscriberKeepsLatestOnly` | `internal/events/hub_test.go` | |
| `TestEvents_AC5_Integration_CrossConnection` *(integration)* | `internal/events/hub_integration_test.go` | Two pools on the same PG |

### AC6 — SSE endpoint contract
**Given** a connection, **then** the first event is the current snapshot, then progress events with increasing `id`, a heartbeat every 15 s, and `completed` followed by close when the job ends. **Given** a user from another workspace, **then** 404.

| Test | Location | Verifies |
|---|---|---|
| `TestSSEHandler_AC6_InitialSnapshotThenStream` | `internal/events/handlers_test.go` | httptest with a flusher |
| `TestSSEHandler_AC6_HeartbeatAndClose` | `internal/events/handlers_test.go` | Fake clock |
| `TestSSEHandler_AC6_TenantIsolation` | `internal/events/handlers_test.go` | |
| `TestSSEFormat_AC6_Framing` | `internal/events/sse_test.go` | Multi-line data escaping |

### AC7 — Client hook
**Given** mocked EventSource events, **then** the hook updates the query cache, exposes the connection state (`reconnecting` on error, `live` on open), and on `completed` closes and invalidates the job queries.

| Test | Location | Verifies |
|---|---|---|
| `useJobEvents.test.ts` › `AC7: merges progress into cache` | `src/lib/sse/useJobEvents.test.ts` | |
| `useJobEvents.test.ts` › `AC7: connection state transitions` | `src/lib/sse/useJobEvents.test.ts` | |
| `useJobEvents.test.ts` › `AC7: completed closes and invalidates` | `src/lib/sse/useJobEvents.test.ts` | |

## Implementation notes
- The progress document lives in the metadata store's `job_progress` collection (Couchbase KV), not PostgreSQL: the worker's runner upserts it every second and the Hub on each API node polls it by revision (CAS). The cross-node integration test uses two independent Couchbase connections (measured: ~200 ms from write to delivery with a 200 ms poll).
- `count(*)` runs on the coordinator connection inside the transaction that exported the snapshot, so the exact total matches what the readers see (tested in `internal/connectors/source/postgres/readsession_test.go`).
- On connect the SSE handler sends the current snapshot, then skips progress it already sent (the first poll repeats it); "completed" is always the last event before the server closes the stream.
