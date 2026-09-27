# OC-029 — Checkpointing, pause/resume & crash recovery

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E5 Migration | P0 | 8 | OC-023, OC-024, OC-025 | Implemented |

## User story
As a **data engineer**, I want a migration to continue from where it stopped after I pause it, after a server restart, or after a crash, without losing or corrupting data, so that multi-hour migrations aren't restarted from zero.

## Implementation details

### Checkpoint model (`jobs.checkpoint`)
```json
{
  "version": 1,
  "mappings": {
    "<mappingId>": {
      "state": "in_progress",               // pending | in_progress | done
      "strategy": "keyset",                  // keyset | cursor (no resume)
      "partitions": [
        { "id": 0, "lo": [1], "hi": [1250001], "lastKey": [402113], "done": false },
        { "id": 1, "lo": [1250001], "hi": [2500001], "lastKey": null, "done": false }
      ],
      "counters": { "written": 401990, "skipped": 0, "failed": 123 }
    }
  },
  "updatedAt": "2026-09-25T15:04:05Z"
}
```

### Contiguous acknowledgement (`internal/pipeline/checkpoint.go`)
- Batches complete out of order, because writers run concurrently. For each partition, the checkpointer keeps a `pending map[seq]Ack` and a `nextSeq`. When `Ack(seq == nextSeq)` arrives, it advances through the consecutive acked seqs and sets `lastKey` to the `LastKey` of the highest contiguous seq.
- **Invariant:** every row with key ≤ `lastKey` in that partition has a final outcome (written, skipped, or failed-and-recorded).
- **Persisting:** every 5 s or every 50 contiguous batches, whichever comes first, it persists with `UPDATE jobs SET checkpoint=$1 WHERE id=$2 AND claimed_by=$3` (fenced by ownership). Error-sink flushes happen **before** the checkpoint persist, so failed rows up to `lastKey` are durable in `job_errors`.
- On partition EOF plus all acked, the partition is `done`. When all partitions are done, the mapping is `done`.

### Resume
- When a worker claims a job that has a checkpoint:
  - `done` mappings are skipped.
  - For in-progress mappings, the partition plan (`lo`/`hi`) is **reused as-is**. Each reader starts at `WHERE key > lastKey AND key < hi`.
  - The counters are restored, so the progress bar continues rather than restarting.
- A fresh snapshot is exported for the resumed run, and a job log event is written: "Resumed from checkpoint (402,113 rows already processed)".
- **At-least-once:** rows between the persisted `lastKey` and the crash point may be written again.
  - `upsert`: idempotent (same key, same content).
  - `insert`: re-writes return `ErrDocumentExists`, which counts as `skipped`. To keep the counters honest, the resume path marks the first 2 batches' "exists" outcomes in `insert` mode as `written (replayed)`.
  - `replace`: idempotent.
- **Cursor strategy** (no PK): resume is impossible. A resumed job restarts that mapping from the beginning, after a UI confirmation for manual resume ("This table has no primary key; resuming will re-read all rows"). Automatic crash recovery also restarts it, with the job log warning `MAPPING_RESTARTED_NO_PK`.

### Pause
Pause sets the pipeline's `drainCtx` cancel:
1. Readers stop at the next batch boundary.
2. Transformers and writers finish in-flight batches (up to 30 s; after the timeout they cancel and those batches aren't acked).
3. The checkpointer persists the final contiguous position.
4. The status becomes `paused` and the worker releases the job.

**Mapping changes while paused:**
- The spec version is recorded at job start (`options.mappingVersions`).
- If a mapping changed while paused, resume shows the prompt "Mapping changed since this job started — resume with the *original* mapping or *restart* with the new one?" In 1.0, jobs always run with the **frozen spec copy** stored in `jobs.options.specs`, so resume is always consistent. The prompt offers *Restart with new mapping* as an alternative.

## Acceptance criteria & unit tests

### AC1 — Contiguous checkpoint advancement
**Given** acks for seq 1, 2, 4, 5 (3 still in flight), **then** `lastKey` = LastKey(2). **When** 3 is acked, **then** `lastKey` = LastKey(5).

| Test | Location | Verifies |
|---|---|---|
| `TestCheckpointer_AC1_ContiguousAdvance` | `internal/pipeline/checkpoint_test.go` | |
| `TestCheckpointer_AC1_PropertyRandomAckOrder` | same | Property test with random permutations: the invariant always holds |

### AC2 — Persist cadence & fencing
**Given** acks over 12 simulated seconds, **then** it persists at ~5 s intervals, and immediately at partition completion. **Given** the worker no longer owns the job (0 rows updated), **then** the checkpointer reports `ErrLostOwnership` and the pipeline aborts.

| Test | Location | Verifies |
|---|---|---|
| `TestCheckpointer_AC2_Cadence` | `checkpoint_test.go` | Fake clock |
| `TestCheckpointer_AC2_Fenced` | same | |
| `TestCheckpointer_AC2_ErrorsFlushedBeforePersist` | same | Order of calls asserted |

### AC3 — Resume from checkpoint
**Given** a checkpoint with partition 0 at `lastKey [402113]` and partition 1 done, **then** the resumed readers start partition 0 at `> 402113` with the same `hi`, skip partition 1, and the counters continue from the stored values.

| Test | Location | Verifies |
|---|---|---|
| `TestResume_AC3_ReaderStartsAfterLastKey` | `internal/pipeline/resume_test.go` | |
| `TestResume_AC3_SkipsDonePartitionsAndMappings` | same | |
| `TestResume_AC3_CountersRestored` | same | |

### AC4 — No data loss on crash
**Given** a pipeline that is killed abruptly (context cancelled without drain) at a random point, and then resumed by a new pipeline instance, **then** the union of written keys equals the full source key set (no loss), and the duplicates are ≤ the rows between the last persisted checkpoint and the crash.

| Test | Location | Verifies |
|---|---|---|
| `TestCrashRecovery_AC4_NoLossProperty` | `internal/pipeline/recovery_test.go` | Fake source (100k rows) and fake writer recording keys; 50 random crash points |
| `TestCrashRecovery_AC4_Integration_KillWorker` *(integration)* | `internal/pipeline/recovery_integration_test.go` | Real PG and Couchbase; kill mid-run; resume; count equals source |

### AC5 — Pause drains cleanly
**Given** a pause request, **then** no new batches are read, in-flight batches are completed and acked, the final checkpoint equals the last contiguous ack, and the status is `paused`.

| Test | Location | Verifies |
|---|---|---|
| `TestPause_AC5_DrainAndCheckpoint` | `internal/pipeline/pause_test.go` | |
| `TestPause_AC5_DrainTimeout` | same | Stuck writer → cancelled after 30 s; checkpoint excludes unacked batches |

### AC6 — Insert-mode replay accounting
**Given** insert mode and a resume that replays 1,000 already-written rows, **then** they count as `written (replayed)` in the first 2 batches, and `skipped` is not inflated.

| Test | Location | Verifies |
|---|---|---|
| `TestResume_AC6_InsertReplayAccounting` | `internal/pipeline/resume_test.go` | |

### AC7 — Frozen spec & no-PK behavior
**Given** a mapping edited while the job is paused, **then** resume uses the frozen spec from `jobs.options.specs`. **Given** a no-PK mapping, **then** resume restarts that mapping and logs `MAPPING_RESTARTED_NO_PK`.

| Test | Location | Verifies |
|---|---|---|
| `TestResume_AC7_UsesFrozenSpec` | `internal/jobs/resume_test.go` | |
| `TestResume_AC7_NoPKRestarts` | same | |
| `ResumeDialog.test.tsx` › `AC7: warns on changed mapping and no-PK` | `src/features/jobs/ResumeDialog.test.tsx` | |
