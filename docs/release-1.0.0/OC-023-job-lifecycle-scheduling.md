# OC-023 — Migration job lifecycle, pre-flight & scheduling

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E5 Migration | P0 | 8 | OC-007, OC-022 | Implemented |

## User story
As a **data engineer**, I want to press **Migrate** and have a durable job start (after automatic pre-flight checks) that I can pause, resume, or cancel, and that keeps running even if a server restarts, so that large migrations are safe to run.

## Implementation details

### Job options (`jobs.options`)
```json
{
  "mappingIds": ["…"],                 // default: all ready mappings in the project
  "writeMode": "upsert",               // upsert | insert (skip existing) | replace (only existing)
  "batchSize": 1000,                   // rows per read batch (100..10000)
  "readParallelism": "auto",           // auto | 1..8 partitions per table (OC-024)
  "writeConcurrency": 64,              // in-flight KV ops (1..512)
  "tableConcurrency": 3,               // mappings processed in parallel (1..6)
  "maxDocsPerSecond": null,            // throttle (token bucket), null = unlimited
  "durability": "none",                // none | majority | majorityAndPersistActive | persistToMajority
  "errorPolicy": { "maxErrors": 1000, "maxErrorRatio": 0.01 },  // OC-026
  "exactCount": "auto",                // auto | always | never (OC-027)
  "verifySampleSize": 100              // OC-030
}
```
Defaults live in a `DefaultOptions()` function, and the Review step shows them in an *Advanced options* accordion with helpful descriptions.

### API
| Method | Path | Notes |
|---|---|---|
| POST | `/projects/{p}/jobs/preflight` | Runs the checks without creating a job; returns the check list |
| POST | `/projects/{p}/jobs` | Runs the pre-flight again, creates the job in `queued`, returns 201 `{job}`. 409 `PROJECT_HAS_RUNNING_JOB` if one is already active (1 active job per project in 1.0) |
| GET | `/projects/{p}/jobs` | History |
| GET | `/jobs/{id}` | Status, options, progress snapshot, per-mapping progress |
| POST | `/jobs/{id}/pause` | `running` → `pausing` → (worker drains) → `paused` |
| POST | `/jobs/{id}/resume` | `paused`/`failed` (resumable) → `queued` with the checkpoint kept |
| POST | `/jobs/{id}/cancel` | Any non-terminal state → `cancelling` → `cancelled` |
| POST | `/jobs/{id}/retry` | Terminal `failed`/`completed_with_errors` → new job with `retryOf`, limited to failed keys (OC-026) |

State transitions are enforced by `jobs.Transition(from, event) (to, error)`, a pure function over the table in [architecture §6.3](00-architecture.md#63-job-state-machine). Invalid transitions return 409 `JOB_INVALID_TRANSITION`.

### Pre-flight checks (`internal/jobs/preflight.go`)
All checks run with the project's **stored** connections (OC-007 AC3), in parallel, with an overall 30 s timeout:
1. Source repository reachable (re-test).
2. Target repository reachable (re-test), KV service available.
3. For each mapping: the latest validation has no errors (OC-022), the source table exists and has SELECT privilege, the key template is valid.
4. For each mapping: the target keyspace exists, or is created now if `createIfMissing` and the capability allows (OC-014). The user has write permission (capability `CanWriteDocs`).
5. Snapshot export supported: `pg_export_snapshot()` is callable (not on a hot standby without `hot_standby_feedback`; otherwise the fallback is a single reader and a warning).
6. Warnings (not blocking): non-idempotent keys, the collection is not empty, insert mode with existing docs, large estimated documents, no PK (so no resume).

The result is a list of `{id, label, status, detail, hint}`, rendered as a checklist identical in style to the connection test.

### Scheduling & workers (`internal/jobs/worker.go`)
- **Claim loop** on worker nodes, every `pollInterval` (1 s, with jitter):
  1. `SELECT META().id, META().cas FROM #{jobs} WHERE status = 'queued' ORDER BY createdAt LIMIT 10` (index `idx_jobs_status`, `request_plus`).
  2. For each candidate: KV `Get`, then `Replace` with that CAS, setting `status='running'`, `claimedBy`, `heartbeatAt`, and `startedAt`.
  3. A CAS mismatch means another worker won, so try the next candidate.
- **Capacity:** each worker runs at most `OC_WORKER_MAX_JOBS` (default 2) jobs at once, using a semaphore.
- **Heartbeat:** every 5 s, `Get` the job and `Replace` it with CAS after checking `claimedBy == me`. If the claim changed, the worker has lost ownership and aborts the local run immediately (fencing).
- **Reaper:** runs every 15 s on whichever node holds the `lease::reaper` document (inserted with a 30 s expiry and renewed). `running`/`pausing`/`cancelling` jobs with `heartbeat_at < now() - 30s` are reset: running → `queued` (resumes from the checkpoint, OC-029), pausing → `paused`, cancelling → `cancelled`.
- **Control signals:** pause and cancel update the job document's status. The owning worker reads the status on every heartbeat (and polls it every 1 s while running) and cancels the pipeline context with a reason (pause → graceful drain; cancel → fast stop).
- **Graceful shutdown (SIGTERM):** the worker stops claiming, signals running pipelines to *pause-drain*, persists checkpoints, and sets the jobs back to `queued` (not `paused`), so another worker picks them up. This happens within the 30 s shutdown budget.

### UI hooks
- The Review step's **Migrate** button (primary, large) opens a confirmation summary: rows (≈ total), target keyspaces, write mode, and warnings. Confirming calls `POST /jobs` and routes to the job monitor (OC-028).
- The Jobs page `/jobs` lists jobs across projects: status, project, started, duration, docs written, errors, and throughput.

## Acceptance criteria & unit tests

### AC1 — State machine
**Given** every (state, event) pair, **then** `Transition` returns the documented next state or `ErrInvalidTransition`. For example, `completed + pause` is invalid, `paused + resume` → `queued`, `queued + cancel` → `cancelled`.

| Test | Location | Verifies |
|---|---|---|
| `TestTransition_AC1_FullMatrix` | `internal/jobs/state_test.go` | Exhaustive table |
| `TestJobsHandler_AC1_InvalidTransition409` | `internal/jobs/handlers_test.go` | |

### AC2 — Pre-flight
**Given** a mapping with validation errors, **then** pre-flight fails with that mapping named. **Given** `createIfMissing` and permission, **then** the collection is created during pre-flight. **Given** a PK-less table, **then** a warning `NO_RESUME_SUPPORT`. **Given** all OK, **then** job creation succeeds.

| Test | Location | Verifies |
|---|---|---|
| `TestPreflight_AC2_ValidationErrorsBlock` | `internal/jobs/preflight_test.go` | Fakes |
| `TestPreflight_AC2_CreatesMissingKeyspace` | `internal/jobs/preflight_test.go` | |
| `TestPreflight_AC2_Warnings` | `internal/jobs/preflight_test.go` | |
| `TestPreflight_AC2_UsesStoredProjectConnections` | `internal/jobs/preflight_test.go` | Resolver fake asserts project-scoped lookup |
| `PreflightChecklist.test.tsx` › `AC2: renders checks and blocks Migrate on failure` | `src/features/jobs/PreflightChecklist.test.tsx` | |

### AC3 — Options validation & defaults
**Given** `batchSize: 50` or `writeConcurrency: 1000`, **then** a 422 with field errors. **Given** omitted options, **then** the defaults are applied and persisted.

| Test | Location | Verifies |
|---|---|---|
| `TestOptions_AC3_ValidateAndDefaults` | `internal/jobs/options_test.go` | |
| `AdvancedOptions.test.tsx` › `AC3: form validation mirrors server` | `src/features/jobs/AdvancedOptions.test.tsx` | |

### AC4 — One active job per project
**Given** a running job, **when** another is created, **then** 409 `PROJECT_HAS_RUNNING_JOB`.

| Test | Location | Verifies |
|---|---|---|
| `TestJobsService_AC4_SingleActivePerProject` | `internal/jobs/service_test.go` | Enforced by inserting a lock document `uniques::activejob::<projectId>` in the job-create transaction, removed when the job reaches a terminal state |

### AC5 — Claiming with CAS
**Given** 2 workers and 3 queued jobs, **then** each job is claimed by exactly one worker, and no worker exceeds its `maxJobs`.

| Test | Location | Verifies |
|---|---|---|
| `TestClaimer_AC5_RespectsCapacity` | `internal/jobs/worker_test.go` | Fake store |
| `TestClaimer_AC5_NoDoubleClaim` *(integration)* | `internal/jobs/worker_integration_test.go` | 2 claimers against real Couchbase |

### AC6 — Heartbeat, fencing & reaper
**Given** a worker whose heartbeat update affects 0 rows, **then** it cancels its pipeline within one heartbeat interval. **Given** a running job with a stale heartbeat (> 30 s), **then** the reaper sets it to `queued`, and the checkpoint is preserved.

| Test | Location | Verifies |
|---|---|---|
| `TestHeartbeat_AC6_LostOwnershipAborts` | `internal/jobs/worker_test.go` | Fake clock |
| `TestReaper_AC6_RequeuesStale` | `internal/jobs/worker_test.go` | |
| `TestReaper_AC6_PausingBecomesPaused` | `internal/jobs/worker_test.go` | |

### AC7 — Pause, resume, cancel signalling
**Given** a running job, **when** pause is requested, **then** the status becomes `pausing`, the worker's pipeline context is cancelled with reason `pause`, and after the drain the status becomes `paused` with a checkpoint. **When** resumed → `queued` → claimed → the run continues from the checkpoint. **When** cancel is requested → `cancelled` within 5 s.

| Test | Location | Verifies |
|---|---|---|
| `TestControl_AC7_PauseDrainsAndCheckpoints` | `internal/jobs/worker_test.go` | Fake pipeline |
| `TestControl_AC7_ResumeRequeues` | `internal/jobs/worker_test.go` | |
| `TestControl_AC7_CancelFast` | `internal/jobs/worker_test.go` | |
| `TestControl_AC7_StatusPollDetectsPause` | `internal/jobs/worker_test.go` | |

### AC8 — Graceful worker shutdown
**Given** SIGTERM while a job runs, **then** the job is checkpointed and set to `queued` (not failed) before the process exits.

| Test | Location | Verifies |
|---|---|---|
| `TestWorker_AC8_ShutdownRequeues` | `internal/jobs/worker_test.go` | |

## Implementation notes
- The worker (`internal/jobs/worker.go`) is started by the server in OC-025, once the pipeline `Runner` exists; until then created jobs stay `queued`.
- Heartbeat and reaper tests use short real intervals (milliseconds) and an injectable clock rather than a fully fake scheduler.
- The job timeline (OC-028 AC7) is stored as one document per job (`events::<jobId>` in `jobs`), so no extra collection is needed.
