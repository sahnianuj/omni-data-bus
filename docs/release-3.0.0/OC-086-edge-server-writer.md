# OC-086 — Edge Server writer: revision-aware batches, verification

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Edge Server Target | P0 | 8 | OC-084, OC-085 | Not started |

## User story
As a **data engineer**, I want a migration job to write every document into the Edge Server collection reliably and fast, including re-runs over documents that are already there, so that the edge database ends up with exactly the documents the mapping describes.

## Scope
**In:**
- `WriterOpener`/`DocWriter` for `edge-server`: batches of `batchSize` documents, `concurrency` batches in flight, using the bulk write endpoint from OC-084 (single PUTs as a fallback if there's none).
- Write modes per [architecture D3](00-architecture.md#d3--overwrite-is-revision-aware): `insert`, `upsert` (default), `replace`. Revisions for a batch are fetched in one bulk read; a conflict refetches and retries once, then reports `TARGET_WRITE_CONFLICT` for that document.
- Per-document results feed the 1.0 error handling (OC-026): DLQ entries, error codes, progress counters.
- Retries with backoff on timeouts, 429 and 5xx; 401/403 stop the job with `TARGET_AUTH_FAILED`.
- Validation before a job starts: a top-level field named like an Edge reserved property → `EDGE_RESERVED_FIELD` (an issue on the mapping, shown in Review).
- `DocLimiter` with Edge's maximum size, so the size guard (OC-046) splits or fails documents before Edge rejects them.
- `DocReader` for post-migration verification (OC-030): bulk read by key; Edge's `_rev`/`_id` are stripped before comparing bodies.
- Resume after a crash (OC-029): a resumed batch is written in `upsert` semantics so already written documents don't fail as conflicts.

**Out:** deleting documents that the source no longer has; TTL/expiry (Edge has no per-document expiry through this path).

## Acceptance criteria & unit tests

### AC1 — Fresh write
**Given** 10,000 rows and an empty collection, **when** the job runs, **then** 10,000 documents exist with the expected IDs and bodies, and the job reports 10,000 written, 0 failed.

### AC2 — Re-run in each mode
**Given** the AC1 collection, **when** the job runs again in `upsert`, **then** all succeed and each document's revision advances; in `insert`, all are reported `TARGET_DOC_EXISTS`; in `replace` with 10 extra source rows, those 10 are `TARGET_DOC_MISSING`.

### AC3 — Conflict retry
**Given** a document changed by another writer between the revision fetch and the write, **then** the writer refetches and succeeds; **given** it changes again, **then** that one document is `TARGET_WRITE_CONFLICT` and the rest of the batch succeeds.

### AC4 — Reserved fields and size
**Given** a mapping that writes a top-level `_id`, **then** Review shows `EDGE_RESERVED_FIELD` and Migrate is blocked. **Given** a document above Edge's limit, **then** the size guard handles it before any request.

### AC5 — Verification and resume
**Given** a finished job, **then** verification matches every sampled document. **Given** a job killed mid-run and resumed, **then** every document exists exactly once and no conflicts are reported.

| Test | Location | Verifies |
|---|---|---|
| `TestWriter_AC1_FreshWrite` | `internal/connectors/target/edge/writer_test.go` (integration) | AC1 |
| `TestWriter_AC2_Modes` | same | AC2 |
| `TestWriter_AC3_ConflictRetry` | same (fake server for the race) | AC3 |
| `TestValidate_AC4_ReservedField` | `internal/mapping/validate_test.go` | AC4 |
| `TestVerify_AC5`, `TestResume_AC5` | `internal/connectors/target/edge/verify_test.go`, `internal/pipeline/resume_edge_test.go` | AC5 |
| `BenchmarkWriter_Batch` | `internal/connectors/target/edge/writer_bench_test.go` | Input to OC-089 |
