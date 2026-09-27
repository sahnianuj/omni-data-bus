# OC-026 — Error handling, dead-letter log & error thresholds

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E5 Migration | P0 | 5 | OC-025 | Implemented |

## User story
As a **data engineer**, I want bad rows captured with clear reasons instead of failing the whole migration, a safety threshold that stops the job when too much is going wrong, and the ability to retry just the failed rows, so that a few bad records don't block the run and I can fix and replay them.

## Implementation details

### Error sink (`internal/pipeline/errors.go`)
- Receives `RowError`s from the reader (decode), transformer, and writer stages. Each one carries `{jobId, mappingId, stage, code, message, fieldPath?, sourceKey (PK values as JSON), sample (the source row, truncated to 4 KB, with values of columns marked sensitive masked)}`.
- **Batched persistence:** errors are buffered and flushed to the `job_errors` collection every 500 errors or 1 s, with concurrent KV `Insert`s (keys `<jobId>::<seq>`) and an expiry equal to the retention period.
- **Storage cap:** at most `OC_JOB_ERRORS_MAX_ROWS` (default 100k) per job are stored. After that, only counters are incremented, and the report says "N more errors not stored".
- **Aggregation:** counters by `(stage, code)` are kept in memory and included in the progress snapshot (OC-027) as `errorsByCode`.
- A sample of the latest 20 errors is pushed to the SSE stream (throttled) for the live error feed.

### Error policy
- `errorPolicy: {maxErrors: 1000, maxErrorRatio: 0.01, minRowsForRatio: 10000}`:
  - The job is **aborted** (status `failed`, reason `ERROR_THRESHOLD_EXCEEDED`) when `failed > maxErrors`, or when `processed ≥ minRowsForRatio` and `failed / processed > maxErrorRatio`.
  - `maxErrors: -1` disables the absolute threshold.
- **Fatal errors** (auth, missing keyspace, source table dropped) abort immediately regardless of the thresholds.
- **End status:** `completed` when 0 failed; `completed_with_errors` when failed > 0 and under the threshold.

### Error browsing API
- `GET /jobs/{id}/errors?stage=&code=&cursor=&limit=` pages through stored errors.
- `GET /jobs/{id}/errors/summary` returns `[{stage, code, count, example}]`.
- `GET /jobs/{id}/errors/export?format=csv|ndjson` streams all stored errors as a download (`Content-Disposition`).

### Retry failed rows
`POST /jobs/{id}/retry {onlyCodes?: []}` creates a new job with `options.retryOf = id` and `options.keyFilter = "failed"`:
- The reader reads only the stored `sourceKey`s of the original job's failures (grouped in batches of 1,000), using `WHERE (pk) IN (…)` with `unnest`.
- The **current** mapping spec is used, so the user can fix a transform first and then retry.
- Available only when the table has a PK and all failures were stored (not truncated). Otherwise the UI offers a full re-run.

### UI (in the job monitor, OC-028)
- An **Errors** tab with a summary by code (bar list with counts), plus a filterable table (stage, code, source key, message, *View row* drawer showing the source row sample and the field path).
- *Download CSV* and *Retry failed rows* (with the explanation "uses your current mapping — fix the mapping first if needed").
- Friendly messages per code, from a catalog with a description and fix hint, e.g. `PARSE_FAILED`: "The value couldn't be converted to the target type. Consider a non-strict parse or a default value."

## Acceptance criteria & unit tests

### AC1 — Errors captured with context
**Given** a transform failure on row PK 42, **then** a `job_errors` row is stored with stage `transform`, code `PARSE_FAILED`, fieldPath `age`, `sourceKey [42]`, and a row sample ≤ 4 KB, with sensitive columns masked.

| Test | Location | Verifies |
|---|---|---|
| `TestErrorSink_AC1_BuildsRecord` | `internal/pipeline/errors_test.go` | |
| `TestErrorSink_AC1_SampleTruncatedAndMasked` | `internal/pipeline/errors_test.go` | |

### AC2 — Batched persistence with cap
**Given** 1,200 errors, **then** there are 3 flushes (500, 500, 200). **Given** the cap of 1,000 stored rows, **then** 1,000 are stored, the counters show 1,200, and `truncated=true`.

| Test | Location | Verifies |
|---|---|---|
| `TestErrorSink_AC2_FlushBySizeAndTime` | `internal/pipeline/errors_test.go` | Fake clock and store |
| `TestErrorSink_AC2_StorageCap` | `internal/pipeline/errors_test.go` | |
| `TestErrorSink_AC2_Integration_BatchInsert` *(integration)* | `internal/pipeline/errors_integration_test.go` | |

### AC3 — Threshold evaluation
**Given** `maxErrors 1000`, **then** the 1,001st failure aborts the job with `ERROR_THRESHOLD_EXCEEDED`. **Given** a ratio of 0.01 with minRows 10k, **then** 150 failures in 10k processed aborts it, and 50 failures in 1k does not (below minRows). **Given** a fatal error, **then** it aborts immediately.

| Test | Location | Verifies |
|---|---|---|
| `TestErrorPolicy_AC3_Absolute` | `internal/pipeline/errors_test.go` | |
| `TestErrorPolicy_AC3_RatioWithMinRows` | `internal/pipeline/errors_test.go` | |
| `TestErrorPolicy_AC3_DisabledAbsolute` | `internal/pipeline/errors_test.go` | |
| `TestPipeline_AC3_FatalAbortsImmediately` | `internal/pipeline/runner_test.go` | |

### AC4 — Final status
**Given** 0 failures → `completed`. **Given** 7 failures under the thresholds → `completed_with_errors`.

| Test | Location | Verifies |
|---|---|---|
| `TestFinalStatus_AC4_Table` | `internal/jobs/state_test.go` | |

### AC5 — Error APIs & export
**Given** stored errors, **then** the summary aggregates by stage and code with an example each, paging filters by code, and the CSV export streams all rows with a header, correctly quoted.

| Test | Location | Verifies |
|---|---|---|
| `TestErrorsHandler_AC5_SummaryAndFilter` | `internal/jobs/handlers_test.go` | |
| `TestErrorsExport_AC5_CSVQuoting` | `internal/jobs/export_test.go` | Commas, quotes, newlines |

### AC6 — Retry failed rows
**Given** a completed_with_errors job with 7 stored failures on a PK table, **when** retry is requested, **then** a new job reads exactly those 7 keys using the current spec. **Given** truncated errors or no PK, **then** 422 `RETRY_NOT_POSSIBLE`.

| Test | Location | Verifies |
|---|---|---|
| `TestRetry_AC6_KeyFilteredReader` | `internal/pipeline/runner_test.go` | |
| `TestRetrySQL_AC6_UnnestKeys` | `internal/connectors/source/postgres/readsql_test.go` | |
| `TestRetry_AC6_NotPossible` | `internal/jobs/retry_test.go` | |

### AC7 — Errors tab UI
**Given** an errors summary and list, **then** the tab shows counts by code with friendly messages, filters the table on click, opens the row drawer, and the Retry button shows its explanation and calls the API.

| Test | Location | Verifies |
|---|---|---|
| `JobErrorsTab.test.tsx` › `AC7: summary, filter, drawer` | `src/features/jobs/JobErrorsTab.test.tsx` | |
| `JobErrorsTab.test.tsx` › `AC7: retry failed rows` | `src/features/jobs/JobErrorsTab.test.tsx` | |
| `errorCatalog.test.ts` › `AC7: every server error code has a message` | `src/features/jobs/errorCatalog.test.ts` | Compares against the OpenAPI `ErrorCode` enum |

## Implementation notes
- "Sensitive" columns are the ones a mapping masks or hashes (`mask` / `hash` steps); their values are masked in stored samples.
- Stored row errors use the `RowErrorCode` enum in the OpenAPI spec; `errorCatalog.test.ts` checks the web catalog against it.
- The size-triggered flush writes full chunks of 500 only; the remainder goes on the 1 s tick (1,200 errors → 500, 500, 200).
- Retrying failed rows reuses the pre-flight and one-active-job rules of a normal job (`POST /jobs/{id}/retry` → `Create`).
