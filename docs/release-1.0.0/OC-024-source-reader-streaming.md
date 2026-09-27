# OC-024 — Source reader: snapshot-consistent, parallel streaming

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E5 Migration | P0 | 8 | OC-008, OC-010, OC-023 | Implemented |

## User story
As a **data engineer**, I want rows streamed from PostgreSQL in consistent batches with bounded memory, in parallel for large tables, so that migrating tables of any size is fast and doesn't overload either system.

## Implementation details

### Pipeline overview (`internal/pipeline`)
```
             ┌──────────── per mapping ─────────────────────────────────────────────────────┐
 Snapshot    │ Reader[p0] ─┐                                                                │
 coordinator │ Reader[p1] ─┼─▶ rowsCh (cap 4 batches) ─▶ Transformer pool (N=GOMAXPROCS) ─▶ │
 (1 tx held) │ Reader[pK] ─┘      [+ EmbedResolver OC-021]                  docsCh (cap 8) │
             │                                  ─▶ Writer pool (OC-025) ─▶ acks ─▶ Checkpointer (OC-029)
             └──────────────────────────────────────────────────────────────────────────────┘
```
Bounded channels provide backpressure: a slow Couchbase slows transformation, which slows reading. Memory is bounded by roughly `(rowsCh cap + docsCh cap + in-flight) × batchSize × avgRowSize`, and the worker computes this and reduces `batchSize` if the estimate exceeds `OC_WORKER_MEMORY_BUDGET_MB` (default 256).

### Snapshot coordinator
1. Opens a coordinator connection: `BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;` then `SELECT pg_export_snapshot();`, and keeps this transaction open for the duration of the mapping's read phase.
2. Each partition reader opens its own connection: `BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET TRANSACTION SNAPSHOT '<id>';`. Embed lookups (OC-021) do the same.
3. If snapshot export isn't available (replica restrictions), the pipeline falls back to a single reader in one REPEATABLE READ transaction and emits the warning `SNAPSHOT_EXPORT_UNAVAILABLE`.
4. **On resume** (OC-029) the original snapshot is gone. The new run uses a new snapshot and continues from the keyset checkpoint (at-least-once semantics), and the job report notes "resumed; rows modified after the pause may reflect newer values".

Session settings for reader connections:
- `statement_timeout = 0` (it applies per batch query, see below);
- `idle_in_transaction_session_timeout = 0` for the coordinator;
- `application_name = 'omni-data-bus job <id> p<k>'`;
- `work_mem` untouched.

### Read strategies
1. **Keyset pagination** (the default when the table has a PK or a not-null unique key):
   ```sql
   SELECT <cols> FROM <tbl>
   WHERE (<k1>,<k2>) > ($1,$2) [AND (<k1>) <= ($hi)]   -- row-value comparison uses the PK index
   ORDER BY <k1>,<k2> LIMIT $batch
   ```
   Each batch query runs with `SET LOCAL statement_timeout = '5min'`, and `LastKey` is the last row's key (the checkpoint position).
2. **Range partitioning** for parallelism: only when the PK is a single integer, bigint, or uuid column and `rowEstimate ≥ 500k`.
   - Get `min(pk)` and `max(pk)` (index-only, instant).
   - Integer keys: split into `K = min(readParallelism|auto, 8)` equal ranges. *auto* = `clamp(rowEstimate / 1M, 1, 8)`.
   - UUID keys: split the 128-bit space evenly (valid for random uuid v4 distribution; for UUID v7 or skewed keys, use `percentile_disc` sampling via `TABLESAMPLE SYSTEM(1)` to find boundaries).
   - Each partition keysets within `[lo, hi)`.
3. **Cursor fallback** (no PK or unique key): `DECLARE c NO SCROLL CURSOR FOR SELECT <cols> FROM <tbl>` then `FETCH FORWARD $batch FROM c`. A single reader, **no resume** (the checkpoint is null; restart re-reads from the beginning), with the pre-flight warning (OC-023).

- **Column projection:** only the columns referenced by mappings, the key template, and embed joins are selected. Identifiers are quoted with `pgx.Identifier`.
- **Decoding:** pgx binary protocol, `rows.Values()` avoided in favour of `rows.RawValues()` plus the per-OID decoders prepared by OC-020, which reuses `[]any` slices from a `sync.Pool` per batch.
- **Batch struct** (OC-008): `{Seq, Partition, Rows, LastKey, ReadAt}`. `Seq` is per partition and monotonic.
- **Throttle:** when `maxDocsPerSecond` is set, readers `rate.Limiter.WaitN(ctx, len(batch))` before emitting.
- **Transient errors** (connection reset, `57P01` admin shutdown, `40001`) → reconnect, `SET TRANSACTION SNAPSHOT` again when the coordinator is still alive (otherwise take a new snapshot and log a warning), and retry the same keyset query up to 5 times with exponential backoff. Non-transient errors fail the mapping.

### Metrics
`oc_reader_rows_total{job,mapping,partition}`, `oc_reader_batch_seconds` (histogram), and `oc_reader_retries_total`.

## Acceptance criteria & unit tests

### AC1 — Keyset SQL generation
**Given** a single-column PK, a composite PK, and a range partition, **then** the generated SQL uses row-value comparison, the correct ORDER BY, the partition upper bound, and quoted identifiers, and projects only the needed columns.

| Test | Location | Verifies |
|---|---|---|
| `TestKeysetSQL_AC1_SinglePK` | `internal/connectors/source/postgres/readsql_test.go` | |
| `TestKeysetSQL_AC1_CompositePK` | `internal/connectors/source/postgres/readsql_test.go` | |
| `TestKeysetSQL_AC1_PartitionBounds` | `internal/connectors/source/postgres/readsql_test.go` | |
| `TestKeysetSQL_AC1_ProjectionAndQuoting` | `internal/connectors/source/postgres/readsql_test.go` | Table `"Order Details"`, column with a space |

### AC2 — Partition planning
**Given** an int PK from 1 to 10,000,000 with auto parallelism and 10M rows, **then** 8 contiguous, non-overlapping ranges cover `[min, max]` exactly. **Given** 200k rows, **then** 1 partition. **Given** a composite PK, **then** 1 partition. **Given** a uuid PK, **then** the ranges cover the full 128-bit space.

| Test | Location | Verifies |
|---|---|---|
| `TestPlanPartitions_AC2_IntRanges` | `internal/pipeline/partition_test.go` | Property test: union = full range, no overlap |
| `TestPlanPartitions_AC2_SmallTableSingle` | `internal/pipeline/partition_test.go` | |
| `TestPlanPartitions_AC2_CompositeSingle` | `internal/pipeline/partition_test.go` | |
| `TestPlanPartitions_AC2_UUIDSpace` | `internal/pipeline/partition_test.go` | |

### AC3 — Consistent snapshot across partitions
**Given** 4 partitions, **then** the coordinator exports one snapshot and every reader transaction issues `SET TRANSACTION SNAPSHOT` with that ID before its first query. **Integration:** rows inserted or updated in the source after the job starts are not visible in the migrated output.

| Test | Location | Verifies |
|---|---|---|
| `TestSnapshotCoordinator_AC3_SharesSnapshot` | `internal/connectors/source/postgres/readsession_test.go` | Fake conn records statements |
| `TestSnapshot_AC3_Integration_ConcurrentWritesInvisible` *(integration)* | `internal/pipeline/snapshot_integration_test.go` | |
| `TestSnapshotCoordinator_AC3_FallbackWhenExportUnavailable` | `internal/connectors/source/postgres/readsession_test.go` | Warning emitted, parallelism 1 |

### AC4 — Streaming completeness & ordering
**Given** a fake source with 25,003 rows, batch size 1,000, and 3 partitions, **then** the readers emit exactly 25,003 rows with no duplicates, and each partition's `Seq` is contiguous from 1 with increasing `LastKey`.

| Test | Location | Verifies |
|---|---|---|
| `TestReaders_AC4_CompleteNoDuplicates` | `internal/pipeline/reader_test.go` | |
| `TestReaders_AC4_SeqContiguousPerPartition` | `internal/pipeline/reader_test.go` | |
| `TestReader_AC4_Integration_1MRows` *(integration)* | `internal/pipeline/reader_integration_test.go` | Seed `big_events` |

### AC5 — Backpressure & bounded memory
**Given** a writer stage that blocks, **then** readers stop after filling the channel buffers (≤ cap + 1 batch per reader in flight) and resume when the writer unblocks. **Given** a large average row size, **then** the batch size is reduced to fit the memory budget.

| Test | Location | Verifies |
|---|---|---|
| `TestPipeline_AC5_BackpressureStopsReaders` | `internal/pipeline/backpressure_test.go` | Counts reads while the sink is blocked |
| `TestMemoryBudget_AC5_AdjustsBatchSize` | `internal/pipeline/reader_test.go` | |

### AC6 — Cursor fallback for tables without keys
**Given** a table without a PK or unique key, **then** a single cursor reader is used with `FETCH FORWARD`, all rows are read, and `LastKey` is nil (no checkpoint).

| Test | Location | Verifies |
|---|---|---|
| `TestCursorReader_AC6_ReadsAllNoCheckpoint` | `internal/pipeline/reader_test.go` | Fake conn |

### AC7 — Transient error retry
**Given** batch 3 fails with a connection reset twice and then succeeds, **then** the same keyset query is retried (same `$1` args), with no gap or duplicate in the output. **Given** 6 failures, **then** the mapping fails with `SOURCE_READ_FAILED`.

| Test | Location | Verifies |
|---|---|---|
| `TestReader_AC7_RetriesSameKeyset` | `internal/pipeline/reader_test.go` | Fake with injected errors |
| `TestReader_AC7_GivesUpAfterMaxRetries` | `internal/pipeline/reader_test.go` | |
| `TestClassifyPgTransient_AC7` | `internal/connectors/source/postgres/errors_test.go` | |

### AC8 — Throttling
**Given** `maxDocsPerSecond=1000` and batch 100, **then** over a simulated 5 s the readers emit ≤ 5,100 rows.

| Test | Location | Verifies |
|---|---|---|
| `TestReader_AC8_RateLimited` | `internal/pipeline/reader_test.go` | Fake clock limiter |

## Implementation notes
- The snapshot coordinator lives in `internal/connectors/source/postgres/readsession.go` (behind the `source.SnapshotReader` / `source.ReadSession` contract), so its tests and the PostgreSQL integration tests sit in that package; the pipeline only sees `source.PartitionReader`.
- Rows are decoded with `rows.Values()` exactly like the mapping preview, so a migrated row and a previewed row produce the same document. Measured on the dev stack: 1,000,000 `big_events` rows in 1.2 s over 8 partitions.
- Reader metrics (`oc_reader_*`) are emitted through the `ReadConfig.OnRetry` hook and the progress tracker; Prometheus export arrives with OC-031.
