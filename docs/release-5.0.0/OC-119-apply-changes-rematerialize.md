# OC-119 — Apply changes: coalesce, re-materialize, write or delete

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E16 Continuous Replication | P0 | 8 | OC-116, OC-117, OC-118 | Not started |

## User story
As a **data engineer**, I want changes applied quickly and correctly (documents rebuilt from the source exactly as the migration would build them), so that the target matches the source whatever my mappings do.

## Scope
**In:** the apply loop per [architecture D3/D4](00-architecture.md#d3--re-materialize-by-default-flat-fast-path-when-exact):
- **Coalescing window**: events are resolved to keys (OC-117) and collected for up to `windowMs` (default 500) or `windowEvents` (default 5,000); keys are deduplicated per mapping. Windows are processed in order; inside a window, keys are partitioned by hash across workers (`concurrency`, default 4), so one key is never built twice at once.
- **Re-materialize**: for each key batch, open a read session (current snapshot), run the mapping's `ExecutionPlan` with `ReadKeys`, related reads and transform: the same code as the migration. Root row present → upsert (and delete surplus pages); root row absent → delete (OC-118). Old keys from key changes → delete.
- **Fast path** for flat mappings when the change carries every projected column in full: transform from the change values directly. Counted separately; a parity test proves identical output.
- **Checkpoint low watermark**: the stream is acked up to the highest commit position whose windows are fully written. Persisted with `replication_state` every second.
- Errors: a document that fails to build or write is dead-lettered (`job_errors` with `replicationId`) and retried on its next change and by **Retry failed**; fatal target errors (permission, missing keyspace) pause the replication with the error. Source read failures retry with backoff; the stream is not acked past them.
- Throttling: the existing AIMD writer concurrency, plus a source-read budget (max refetch queries/s, default unlimited) so replication can be kept gentle on a busy primary.
- **Truncate** events: apply the replication's truncate policy (pause by default; or delete every document of the mappings whose root table was truncated).

**Out:** the PostgreSQL stream (OC-121), UI (OC-123).

## Acceptance criteria & unit tests

### AC1 — Convergence check
**Given** the fake change source driving the golden mappings with 50,000 randomized changes, **then** after the stream drains, the target equals a fresh migration of the final source state, on every target kind.

### AC2 — Fast path parity
**Given** a flat mapping, **then** documents written by the fast path are byte-identical to re-materialized ones, and changes with TOAST-unchanged markers take the re-materialize path.

### AC3 — Crash replay
**Given** a `kill -9` at random points during apply (10 runs), **then** each restart replays from the low watermark and the convergence check (AC1) still passes.

### AC4 — Coalescing
**Given** 1,000 updates to the same row within one window, **then** the document is built once for that window.

| Test | Location | Verifies |
|---|---|---|
| `TestApply_AC1_Convergence` | `internal/replication/apply/convergence_integration_test.go` | AC1 |
| `TestApply_AC2_FastPathParity` | `internal/replication/apply/fastpath_test.go` | AC2 |
| `TestApply_AC3_CrashReplay` | `internal/replication/apply/recovery_integration_test.go` | AC3 |
| `TestWindow_AC4_Coalesce` | `internal/replication/apply/window_test.go` | AC4 |
