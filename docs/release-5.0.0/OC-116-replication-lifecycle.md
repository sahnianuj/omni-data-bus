# OC-116 — Replications: a long-running job with its own lifecycle

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E16 Continuous Replication | P0 | 8 | OC-115 | Not started |

## User story
As a **data engineer**, I want to start a replication that loads my tables and then keeps running, survives restarts, and can be paused, resumed and stopped, so that my Couchbase target stays current until I cut over.

## Scope
**In:**
- Job type `replication` with the state machine in [architecture D1](00-architecture.md#d1--a-replication-is-a-long-running-job), stored in `replications` and `replication_state` (§7). Provisioning step for the new collections and indexes.
- API: `POST /projects/{id}/replications` (mappings, policies, window options), `GET` list/detail, `POST …/pause`, `…/resume`, `…/stop` (with "drop source objects" default on), `…/resync` (all or chosen mappings). `409 REPLICATION_ACTIVE` for a second one or for a migration job while one runs, and the reverse.
- Worker: claim, heartbeat, fencing and reclaim reuse the jobs machinery; a reclaimed replication resumes from `replication_state` (initial-load partitions from their checkpoints, stream from the low watermark).
- Mapping edits: refused with `409 REPLICATION_STREAMING` while streaming; allowed while paused, and the changed mappings are marked **needs resync**.
- Migrate step: a choice between **Migrate once** and **Replicate continuously** (single-line segmented control; the description in a tooltip). The replicate option is disabled with the reason when the source connector has no `ChangeSource`.
- Audit events for create, pause, resume, stop, resync.

**Out:** the stream itself (OC-121), impact (OC-117), apply (OC-119), monitor UI (OC-123).

## Acceptance criteria & unit tests

### AC1 — State machine
**Given** the transitions in D1, **then** every allowed transition succeeds and every other returns `409 INVALID_TRANSITION` (table-driven).

### AC2 — One at a time
**Given** a streaming replication, **then** starting a migration job or a second replication returns `409 REPLICATION_ACTIVE`.

### AC3 — Failover
**Given** a streaming replication on worker A (fake change source), **when** A stops heartbeating, **then** worker B reclaims it within 45 s and continues from the acked position; A's late writes are fenced.

### AC4 — Mapping edits
**Given** a streaming replication, **then** saving a mapping returns `409 REPLICATION_STREAMING`; after pause, the save succeeds and the mapping shows **needs resync**.

| Test | Location | Verifies |
|---|---|---|
| `TestState_AC1_Transitions` | `internal/replication/state_test.go` | AC1 |
| `TestService_AC2_OneActive` | `internal/replication/service_test.go` | AC2 |
| `TestWorker_AC3_Reclaim` | `internal/replication/worker_test.go` | AC3 |
| `TestMappings_AC4_EditWhileStreaming` | `internal/mappings/replication_guard_test.go` | AC4 |
| `MigrateStep.test.tsx` › `OC-116` | `web/src/features/jobs/MigrateStep.test.tsx` | Migrate step choice |
