# OC-025 — Transform stage & Couchbase writer

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E5 Migration | P0 | 8 | OC-012, OC-020, OC-024 | Implemented |

## User story
As a **data engineer**, I want streamed rows transformed into documents and written to Couchbase quickly and reliably, with retries for transient failures and my chosen write semantics, so that the migration finishes fast and every document lands correctly.

## Implementation details

### Transformer pool (`internal/pipeline/transformer.go`)
- `N = GOMAXPROCS` goroutines read `*Batch` from `rowsCh`, resolve embeds (OC-021) where configured, and run `Program.Apply` per row (OC-020).
- The output is a `*DocBatch{Seq, Partition, LastKey, Docs []Doc{Key string; Value []byte; SourceKey []any}, RowErrors []RowError}`. The byte slices come from a pooled arena and are released after write acks.
- Row errors go to the error sink (OC-026) and **still count toward the batch's completion**, so the checkpoint can advance past a bad row.
- Program compile happens once per mapping per job, and each goroutine clones cheap per-goroutine state (buffers).

### Writer (`internal/connectors/target/couchbase/writer.go`)
```go
type DocWriter interface {
    // WriteBatch writes all docs, returning per-doc outcome; never returns partial silently.
    WriteBatch(ctx context.Context, docs []Doc) []WriteResult
    Close() error
}
type WriteResult struct { Key string; Outcome Outcome /* written|skipped|failed */; Err *WriteError; Attempts int }
```
- **Mode mapping:**
  - `upsert` → `collection.Upsert(key, gocb raw JSON, opts)`.
  - `insert` → `collection.Insert`, where `ErrDocumentExists` becomes `skipped`.
  - `replace` → `collection.Replace` without CAS, where `ErrDocumentNotFound` becomes `skipped`.
- **No double encoding:** documents are already serialized JSON bytes, written with a passthrough transcoder (`gocb.NewRawJSONTranscoder()`).
- **Options:** `Timeout` = kvTimeout; `DurabilityLevel` from job options; `Expiry` from the mapping spec (OC-016).
- **Concurrency:** a shared semaphore of `writeConcurrency` in-flight ops across the writer goroutines. Each doc is dispatched with `go` under the semaphore; gocb pipelines requests over its KV connections. A `DocBatch` is acknowledged when all of its docs have a final outcome.
- **Retries** (writer-level, on top of the SDK's own retry strategy, for errors surfaced to the app):
  - Retry on `ErrTemporaryFailure`, `ErrTimeout`/`ErrAmbiguousTimeout` (safe for upsert and replace; for insert, an ambiguous timeout followed by `ErrDocumentExists` on retry becomes `written`), `ErrServerOutOfMemory` (backoff and throttle), and `ErrDurabilitySyncWriteInProgress`.
  - Exponential backoff starting at 50 ms with ×2 and full jitter, capped at 5 s, for up to 6 attempts.
  - **Adaptive concurrency:** on sustained temporary-failure or OOM responses (> 5% of ops in a 5 s window), the effective concurrency is halved (AIMD) and then grows by +4 per 5 s of clean writes, up to the configured maximum. This protects busy clusters.
- **Non-retryable** → `failed` with a code:
  - `ErrValueTooLarge` → `DOC_TOO_LARGE`;
  - key invalid or too long → `KEY_INVALID`;
  - `ErrCollectionNotFound` → mapping fatal `TARGET_KEYSPACE_MISSING`;
  - `ErrAuthenticationFailure` or access denied → job fatal `TARGET_PERMISSION_DENIED`;
  - durability impossible → job fatal `DURABILITY_IMPOSSIBLE` (hint: reduce the durability level).
- The writer uses the project's stored target connection via the session pool (OC-012), with a dedicated `*gocb.Cluster` per job so KV connections are not shared with UI traffic.

### Ack path
The writer emits `Ack{Partition, Seq, LastKey, written, skipped, failed, bytes}`, which goes to the progress counters (OC-027) and the checkpointer (OC-029).

### Metrics
`oc_writer_ops_total{outcome,code}`, `oc_writer_latency_seconds` (histogram), `oc_writer_inflight`, `oc_writer_effective_concurrency`, and `oc_writer_retries_total{code}`.

## Acceptance criteria & unit tests

### AC1 — Transformer stage correctness
**Given** a batch of 1,000 rows where 2 fail transformation, **then** the `DocBatch` contains 998 docs and 2 row errors, preserves `Seq`/`LastKey`, and routes the errors to the error sink.

| Test | Location | Verifies |
|---|---|---|
| `TestTransformer_AC1_SplitsDocsAndErrors` | `internal/pipeline/stages_test.go` | |
| `TestTransformer_AC1_PreservesBatchMeta` | `internal/pipeline/stages_test.go` | |
| `TestTransformer_AC1_ParallelDeterministicOutput` | `internal/pipeline/stages_test.go` | Same input → same docs regardless of worker count |

### AC2 — Write modes
**Given** `upsert`, **then** existing and new keys are both `written`. **Given** `insert`, **then** existing keys are `skipped`. **Given** `replace`, **then** missing keys are `skipped`.

| Test | Location | Verifies |
|---|---|---|
| `TestWriter_AC2_Upsert` | `internal/connectors/target/couchbase/writer_test.go` | `kvAPI` fake over the gocb collection |
| `TestWriter_AC2_InsertSkipsExisting` | `internal/connectors/target/couchbase/writer_test.go` | |
| `TestWriter_AC2_ReplaceSkipsMissing` | `internal/connectors/target/couchbase/writer_test.go` | |
| `TestWriter_AC2_Integration_AllModes` *(integration)* | `…/writer_integration_test.go` | |

### AC3 — Raw JSON passthrough & options
**Given** doc bytes, **then** the bytes stored in Couchbase are identical (no re-encoding), and expiry and durability are set from the options.

| Test | Location | Verifies |
|---|---|---|
| `TestWriter_AC3_RawTranscoderAndOptions` | `internal/connectors/target/couchbase/writer_test.go` | Fake captures the options |
| `TestWriter_AC3_Integration_BytesIdentical` *(integration)* | `…/writer_integration_test.go` | |

### AC4 — Retry transient errors
**Given** a doc that fails with `ErrTemporaryFailure` twice and then succeeds, **then** its outcome is `written` with `Attempts=3`, and backoff delays follow the policy. **Given** 6 consecutive failures, **then** `failed` with code `TEMPORARY_FAILURE_EXHAUSTED`. **Given** insert mode with an ambiguous timeout followed by `ErrDocumentExists`, **then** `written`.

| Test | Location | Verifies |
|---|---|---|
| `TestWriter_AC4_RetryThenSuccess` | `internal/connectors/target/couchbase/writer_test.go` | Fake clock |
| `TestWriter_AC4_RetryExhausted` | `internal/connectors/target/couchbase/writer_test.go` | |
| `TestWriter_AC4_InsertAmbiguousThenExists` | `internal/connectors/target/couchbase/writer_test.go` | |
| `TestBackoff_AC4_JitterBounds` | `internal/pipeline/flowctl/backoff_test.go` | |

### AC5 — Non-retryable & fatal classification
**Given** `ErrValueTooLarge`, **then** that doc is `failed/DOC_TOO_LARGE` and the others continue. **Given** `ErrCollectionNotFound`, **then** the mapping fails. **Given** an authentication failure, **then** the job fails with `TARGET_PERMISSION_DENIED`.

| Test | Location | Verifies |
|---|---|---|
| `TestClassifyWriteError_AC5_Table` | `internal/connectors/target/couchbase/writer_test.go` | |
| `TestPipeline_AC5_FatalErrorStopsJob` | `internal/pipeline/runner_test.go` | |

### AC6 — Bounded, adaptive concurrency
**Given** `writeConcurrency=64`, **then** in-flight ops never exceed 64. **Given** 10% temporary failures over 5 s, **then** the effective concurrency halves. After 5 s clean it increases by 4.

| Test | Location | Verifies |
|---|---|---|
| `TestWriter_AC6_InflightNeverExceedsLimit` | `internal/connectors/target/couchbase/writer_test.go` | Fake records max in-flight |
| `TestAIMD_AC6_DecreaseAndRecover` | `internal/pipeline/flowctl/aimd_test.go` | Fake clock |

### AC7 — Batch acknowledgement
**Given** a `DocBatch` of 1,000 docs, **then** exactly one `Ack` is emitted after all 1,000 have final outcomes, with counts summing to 1,000 plus the batch's row errors attributed as failed.

| Test | Location | Verifies |
|---|---|---|
| `TestWriterStage_AC7_SingleAckWithCounts` | `internal/pipeline/stages_test.go` | |

### AC8 — End-to-end pipeline
**Given** the seed `customer` table (and the 1M-row `big_events`), **when** the pipeline runs against the Couchbase container, **then** the document count equals the source row count, and 20 random docs equal `Program.Apply` of their source rows.

| Test | Location | Verifies |
|---|---|---|
| `TestPipeline_AC8_Integration_EndToEnd` *(integration)* | `internal/pipeline/pipeline_integration_test.go` | |

## Implementation notes
- Backoff and AIMD live in `internal/pipeline/flowctl` so the Couchbase writer can use them without importing the pipeline.
- The writer probes the target collection when it opens: a write to a missing collection doesn't fail with `ErrCollectionNotFound`, the SDK retries it (`KV_COLLECTION_OUTDATED`) until it times out, so without the probe every document would take ~30 s to fail. Timeouts with that retry reason are also classified as `TARGET_KEYSPACE_MISSING`.
- The server starts the worker (OC-023) with this pipeline as its `Runner` on nodes with `OC_ROLE=all|worker`.
- Measured on the dev stack (single-node Couchbase in Docker): `shop.customer` + 1,000,000 `big_events` rows in 19.3 s (~52k docs/s), counts exact, 20 random documents per table equal to `Program.Apply` of their rows.
