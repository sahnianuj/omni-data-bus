# OC-040 — Directory writer: atomic, resumable file output

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E8 Directory Target | P0 | 8 | OC-036, OC-039, OC-025, OC-026, OC-029 | Implemented |

## User story
As a **data engineer**, I want migrations to a directory target to write one complete, valid JSON file per document, quickly and safely, with the same pause, resume, crash-recovery and error handling as a Couchbase migration, so that the output folder is always consistent and complete.

## Scope
**In:**
- A `DocWriter` for directories: atomic writes, the three write modes, durability levels, compression, bounded concurrency.
- Directory preflight: writable, free space and inode estimate, orphan temp-file cleanup.
- Error mapping to the 1.0 dead-letter and threshold model (OC-026), including fatal errors.
- Checkpoint and resume (OC-029) and cancellation with no partial files.

**Out:** deleting files for rows deleted at the source (no sync mode in 2.0), and appending many documents to one file (NDJSON output), which is a candidate for later.

## Implementation details

### Write path (`internal/connectors/target/directory/writer.go`)
1. Serialize (already done by the transform stage). Optionally pretty-print or gzip, then check `maxDocBytes` → row error `DOC_TOO_LARGE`.
2. Compute the path (OC-039), and make sure the parent directory exists. A `sync.Map` cache of created directories avoids a `MkdirAll` per file.
3. Write to `<folder>/.odb-tmp/<writerId>-<seq>.tmp` on the **same file system**, with `O_CREAT|O_EXCL|O_WRONLY` and `fileMode`.
4. `durability ≥ file` → `f.Sync()`. Close.
5. Commit by mode:

| Mode | Directory semantics | Implementation |
|---|---|---|
| `upsert` (default) | Create or overwrite | `os.Rename(tmp, final)` (atomic on POSIX; `MoveFileEx` with replace on Windows) |
| `insert` | Create only; existing file → `Skipped` | Linux `renameat2(RENAME_NOREPLACE)`; elsewhere `os.Link(tmp, final)` then remove the temp file; `EEXIST` → Skipped |
| `replace` | Overwrite only if it exists; missing → `Skipped` | `Lstat(final)` then rename (documented as not race-free against other writers; there are none in a job) |

6. `durability = file+dir` → fsync the parent directory after rename, batched to once per directory per batch.
7. Append the manifest line (OC-039).

- **Concurrency:** a pool of `writers` goroutines (job option, default 16). There is no AIMD: file systems don't push back like a KV service, so a fixed pool is enough.
- **Collision guard on case-insensitive file systems:** in `insert` mode `EEXIST` is normal. In `upsert` mode, when the target was detected as case-insensitive, the writer keeps a per-job set of lower-cased paths it has written, and a second key with the same lower-cased path → row error `KEY_PATH_COLLISION`. Memory is bounded: after 5M entries it switches to a Bloom filter at 1% false positives, and the error message says the hit may be a false positive.

### Error mapping
| Condition | Code | Fatal |
|---|---|---|
| `ENOSPC`, `EDQUOT` | `TARGET_DISK_FULL` | yes: the job fails, and the checkpoint allows resume after space is freed |
| `EACCES`, `EPERM`, `EROFS` | `TARGET_PERMISSION_DENIED` | yes |
| Root folder missing at runtime (unmounted volume) | `TARGET_LOCATION_MISSING` | yes |
| `ENAMETOOLONG` (despite OC-039 hashing, for example a long root path) | `KEY_INVALID` | no, dead-lettered |
| `EIO` and other errors | `WRITE_FAILED` | no. Retried 3× with backoff, then dead-lettered; 50 consecutive failures → fatal |

### Preflight (via `Preflighter`, OC-036)
- Root and folders exist or can be created, and are writable (probe).
- Space estimate: `Σ rowEstimate × avgDocBytes (preview sample) × 1.2`, plus one inode per document plus folders. Below the estimate → **warning** (not blocking) with the numbers; below 10% of it → **error**.
- Orphan cleanup: remove `.odb-tmp/*.tmp` files older than the job's start (left by a crashed earlier run). The count is reported.

### Checkpoint, resume & cancel
- Same at-least-once model as 1.0: the checkpoint advances only after a batch's files are committed. On resume, re-written files are overwritten in `upsert` mode (idempotent), or skipped in `insert` mode.
- Cancel and pause drain in-flight writes. A temp file is never renamed after cancellation is observed, so the output folder never holds a partial document. Temp files left by a hard kill are cleaned by the next preflight.

### Progress & monitor
- Progress counters are unchanged. The monitor shows the output root and a **bytes written** figure alongside docs/s.

## Acceptance criteria & unit tests

### AC1 — Atomic, valid files
**Given** 10,000 documents written with a fault injector that kills writes at random points, **then** every `.json` file in the output parses as JSON and matches its source document; partial data only ever exists under `.odb-tmp/`.

| Test | Location | Verifies |
|---|---|---|
| `TestWriter_AC1_AtomicUnderFaults` | `internal/connectors/target/directory/writer_test.go` | Fault-injecting fs wrapper |
| `TestWriter_AC1_GzipRoundTrip` | same | |

### AC2 — Write modes
**Given** an existing `1001.json`, **then** `upsert` overwrites it (Written), `insert` leaves it (Skipped), and `replace` overwrites it. **Given** no file, **then** `replace` → Skipped and `insert` → Written.

| Test | Location | Verifies |
|---|---|---|
| `TestWriter_AC2_Modes` | `internal/connectors/target/directory/writer_test.go` | Table-driven |
| `TestWriter_AC2_InsertNoReplaceRace` | same | 8 goroutines insert the same key; exactly one Written |

### AC3 — Error mapping & fatality
**Given** a fake file system returning `ENOSPC`, **then** the result is Failed, `TARGET_DISK_FULL`, `Fatal: true`, and the job fails with a resumable checkpoint. **Given** `EIO` twice then success, **then** Written with `Attempts: 3`.

| Test | Location | Verifies |
|---|---|---|
| `TestWriter_AC3_ErrorTable` | `internal/connectors/target/directory/errors_test.go` | |
| `TestWriter_AC3_RetryThenSucceed` | same | |

### AC4 — Resume after crash
**Given** a directory job killed at ~50%, **when** it is resumed on another worker, **then** the output has exactly one file per source row, the manifest is complete, and no `.tmp` files remain after preflight.

| Test | Location | Verifies |
|---|---|---|
| `TestDirectoryJob_AC4_CrashResume` *(integration)* | `internal/pipeline/directory_integration_test.go` | |

### AC5 — Preflight
**Given** an estimate of 10 GB and 4 GB free, **then** a warning with both numbers. **Given** 0.5 GB free, **then** a blocking error. **Given** two stale `.tmp` files, **then** they are removed and reported.

| Test | Location | Verifies |
|---|---|---|
| `TestPreflight_AC5_SpaceWarningsAndErrors` | `internal/connectors/target/directory/preflight_test.go` | |
| `TestPreflight_AC5_OrphanCleanup` | same | |

### AC6 — Case collisions detected
**Given** a case-insensitive fake file system and keys `ABC` and `abc` in `upsert` mode, **then** the second is dead-lettered with `KEY_PATH_COLLISION` and the first file is untouched.

| Test | Location | Verifies |
|---|---|---|
| `TestWriter_AC6_CaseCollision` | `internal/connectors/target/directory/writer_test.go` | |

### AC7 — Throughput
**Given** 1 KB documents on a tmpfs, `durability: file`, 16 writers, **then** `BenchmarkDirectoryWriter` sustains ≥ 15,000 files/s (the reference NVMe gate runs in OC-056).

| Test | Location | Verifies |
|---|---|---|
| `BenchmarkDirectoryWriter` | `internal/connectors/target/directory/writer_bench_test.go` | |

## Implementation notes (as built)
- **Writer:** `directory/writer.go` works over a small `fsys` interface, which tests use to inject faults. A temp file goes in `<folder>/.odb-tmp/<writerId>-<seq>.tmp` (`O_EXCL`), is fsynced (unless `durability: none`) and closed, then committed by mode: `upsert` renames, `insert` hard-links (`EEXIST` means Skipped) then removes the temp file, and `replace` checks `Lstat` then renames. Cancellation is checked just before the commit, so nothing is published after cancel. `file+dir` syncs each touched folder once per batch. It uses a fixed pool of 16 goroutines, not AIMD. `insert` uses `link` instead of `renameat2`: it is portable across Linux and macOS and gives the same no-replace guarantee (8 concurrent inserts, exactly one wins).
- **Errors:** `ENOSPC`/`EDQUOT` → `TARGET_DISK_FULL` and `EACCES`/`EPERM`/`EROFS` → `TARGET_PERMISSION_DENIED`, both fatal. A vanished folder → `TARGET_LOCATION_MISSING` (fatal). `ENAMETOOLONG` → `KEY_INVALID`. Anything else → `WRITE_FAILED`, retried 3 times (10, 40, 160 ms), and 50 consecutive failures become fatal. `KEY_PATH_COLLISION` (a row error, now in the web error catalog) is raised on case-insensitive folders: an exact set of paths, switching to a Bloom filter (1% false positives) after 5M entries.
- **Preflight:** a total space check on the first mapping (`Σ rows × avg row bytes × 1.2`; the average comes from the source table's size statistics, 1 KiB when unknown): a warning below the estimate, a failure below 10% of it, and inodes checked against one per document. Temp files older than the preflight are removed and reported.
- **Throughput (AC7):** `BenchmarkDirectoryWriter` on Linux tmpfs (Docker, `golang:1.26-alpine`) does about **56,000 files/s with `durability: file`** (the gate is ≥ 15,000). On macOS APFS it does about 5,600 files/s with `none` and about 220 with `file`, because Go's `Sync` on darwin is `F_FULLFSYNC`, a full device flush. Use `durability: none` for bulk exports on a Mac; it is the default since OC-079.
- **AC4:** `TestDirectoryJob_AC4_CrashResume` kills a 30,000-row run of `Order Details` a third of the way. After preflight cleanup and a resume on a new runner, there are exactly 30,000 files and 30,000 manifest lines, and no temp files. The monitor already shows bytes written; the Output tab (OC-041) shows the output folder.
