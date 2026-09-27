# OC-022 — Mapping validation & document preview (dry run)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E4 Design & Map | P0 | 5 | OC-016, OC-019, OC-020 | Implemented |

## User story
As a **data engineer**, I want to validate my mapping and see exactly what the Couchbase documents will look like for real sample rows before migrating, so that I catch problems before writing millions of documents.

## Implementation details

### Validation (`internal/mapping/validate.go`)
`Validate(spec, snapshotTable, targetCaps) []Issue`, where `Issue{severity: error|warning|info, code, path, message, fix?}`. It aggregates:
- DocSchema validation (OC-015 AC5).
- Key template validation and uniqueness analysis (OC-016).
- Transform compile issues (OC-020).
- Reference integrity: columns and field IDs exist.
- **Required target fields unmapped** → error `REQUIRED_FIELD_UNMAPPED` (fix: "Map a source, set a constant, or mark optional").
- **Suggestions pending** → warning `SUGGESTIONS_PENDING` (not blocking).
- Lossy conversions without an explicit transform → warning `LOSSY_CONVERSION`.
- **Source drift:** mapped columns missing from the latest snapshot → error `SOURCE_COLUMN_MISSING`; changed type → warning (OC-010 AC7).
- Target: the keyspace exists or `createIfMissing`; otherwise error `TARGET_KEYSPACE_MISSING`.
- Embeds (OC-021).
- Unmapped source columns → info (they are simply not migrated).

`ready` = no errors. Validation runs on every save (the response carries the issues) and on demand via `POST /projects/{p}/mappings/{mid}/validate`.

### Preview (`internal/preview`)
`POST /projects/{p}/mappings/{mid}/preview {limit: 5..50 (default 10), sampleMode: "first"|"random", fieldMappingId?, specOverride?}`
- Reads sample rows using the project's stored source connection:
  - `first`: ordered by PK, `LIMIT n`.
  - `random`: `TABLESAMPLE SYSTEM (p)` with p computed from rowEstimate, falling back to `ORDER BY random() LIMIT n` for tables < 100k rows.
  - `statement_timeout` is 10 s.
- Runs embed lookups (OC-021) and the **same compiled program** as the pipeline (OC-020).
- `specOverride` lets the UI preview unsaved edits.
- Returns:
  ```json
  { "items": [{ "sourceRow": {...}, "key": "customer::1", "document": {...}, "sizeBytes": 812,
                "warnings": [...], "error": null }],
    "summary": { "ok": 9, "errors": 1, "avgSizeBytes": 790, "maxSizeBytes": 1310 } }
  ```
- With `fieldMappingId`, it returns only `[{before, after}]` for the Inspector mini-preview.
- **Never writes to Couchbase.**
- Optional *Check key collisions in target*: for the preview keys, `collection.Exists` (bounded, ≤ 50 keys) reports which keys already exist.

### UI — Preview drawer (*Preview ▸* in the workbench header; also embedded in the Review step)
- A split view: on the left, a list of sample rows (key, status icon, size); on the right, tabs for **Document** (syntax-highlighted JSON with collapsible nodes and a copy button), **Source row** (key/value table), and **Diff-style trace** (each target field → the source value → the step outputs, which helps debug transforms).
- Controls: Sample 10/25/50 · First / Random · *Refresh* · *Check key collisions*.
- Errored rows show the row error with the field path and a *Jump to mapping* link (selects the edge in the canvas).
- Summary chips: ✓ 9 valid · ✗ 1 error · avg 790 B · max 1.3 KB.

### UI — Validation panel (*Validate* button and a status pill in the header)
Issues are grouped by severity. Each issue has *Go to* (focuses the field or edge) and, when available, a one-click fix (e.g., *Mark optional*, *Accept all suggestions*, *Add parseInteger*).

## Acceptance criteria & unit tests

### AC1 — Aggregated validation
**Given** a spec with an unmapped required field, a pending suggestion, a lossy conversion without a transform, a missing source column, and a missing keyspace without createIfMissing, **then** `Validate` returns exactly these issues with the specified severities, codes, and paths, and `ready=false`.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC1_AggregatesAllRules` | `internal/mapping/validate_test.go` | |
| `TestValidate_AC1_ReadyWhenOnlyWarnings` | `internal/mapping/validate_test.go` | |
| `TestValidate_AC1_DeterministicOrder` | `internal/mapping/validate_test.go` | Sorted by severity, then path |

### AC2 — Validation panel & fixes
**Given** issues, **then** the panel groups them by severity, *Go to* focuses the element, and *Mark optional* fixes `REQUIRED_FIELD_UNMAPPED`, after which the issue disappears after the save round-trip.

| Test | Location | Verifies |
|---|---|---|
| `ValidationPanel.test.tsx` › `AC2: groups and go-to` | `src/features/mapping/ValidationPanel.test.tsx` | |
| `ValidationPanel.test.tsx` › `AC2: quick fix mutates store` | `src/features/mapping/ValidationPanel.test.tsx` | |

### AC3 — Preview uses the pipeline program
**Given** 10 sample rows, **then** the preview documents are byte-identical to what `transform.Program.Apply` produces for the same rows (the same compile path), and keys follow the template. Nothing is written to the target (the writer fake records zero calls).

| Test | Location | Verifies |
|---|---|---|
| `TestPreview_AC3_UsesCompiledProgram` | `internal/mappings/service_test.go` | Fake source |
| `TestPreview_AC3_NoTargetWrites` | `internal/mappings/service_test.go` | |
| `TestPreview_AC3_SpecOverride` | `internal/mappings/service_test.go` | Unsaved spec is used |

### AC4 — Sampling modes & limits
**Given** `limit=500`, **then** it is capped at 50. **Given** `random` on a 10M-row table, **then** the TABLESAMPLE SQL is used. On a 5k-row table, `ORDER BY random()`. A statement timeout surfaces as `PREVIEW_TIMEOUT`.

| Test | Location | Verifies |
|---|---|---|
| `TestSampleSQL_AC4_Modes` | `internal/connectors/source/postgres/preview_test.go` | |
| `TestPreview_AC4_LimitCap` | `internal/mappings/service_test.go` | |

### AC5 — Row errors are isolated
**Given** one row fails `parseInteger` (strict), **then** that item has `error` with the field path and the other items succeed; summary `{ok:9, errors:1}`.

| Test | Location | Verifies |
|---|---|---|
| `TestPreview_AC5_RowErrorIsolated` | `internal/mappings/service_test.go` | |
| `PreviewDrawer.test.tsx` › `AC5: shows error and jump-to-mapping` | `src/features/mapping/PreviewDrawer.test.tsx` | |

### AC6 — Trace view
**Given** a mapping `trim → upper` on `" ann "`, **then** the trace shows `" ann "` → `"ann"` → `"ANN"` for that field.

| Test | Location | Verifies |
|---|---|---|
| `TestProgram_AC6_TraceMode` | `internal/transform/program_test.go` | Apply with tracing records per-step outputs |
| `PreviewDrawer.test.tsx` › `AC6: renders step trace` | `src/features/mapping/PreviewDrawer.test.tsx` | |

### AC7 — Key collision check
**Given** 10 preview keys of which 3 exist in the target, **then** those 3 are flagged "exists — will be overwritten (upsert)" or "will be skipped (insert)", depending on the planned write mode.

| Test | Location | Verifies |
|---|---|---|
| `TestPreview_AC7_KeyCollisions` | `internal/mappings/service_test.go` | Fake Exists |
