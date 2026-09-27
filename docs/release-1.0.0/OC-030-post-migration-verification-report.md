# OC-030 — Post-migration verification & report

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E5 Migration | P1 | 5 | OC-025, OC-027 | Implemented |

## User story
As a **data engineer**, I want the application to verify the migrated data and give me a downloadable report, so that I can prove to stakeholders that the migration is complete and correct.

## Implementation details

### Verification phase (`internal/verify`)
Runs automatically after each mapping reaches `done` (phase `verifying` in progress), unless `verifySampleSize: 0`.

1. **Count reconciliation.**
   - `sourceRows` = rows read for the mapping (exact).
   - `expectedDocs = written + skipped (insert/replace) − duplicateKeys`.
   - The **duplicate-key detector** tracks the distinct keys written, using an HLL++ sketch (`github.com/axiomhq/hyperloglog`) to estimate distinct keys vs writes. If the estimate is more than 0.5% below the written count, it warns `KEY_COLLISIONS_DETECTED` (a key template that isn't unique means documents overwrote each other).
   - Optional target count: `SELECT COUNT(*) FROM keyspace` only when the Query service is available, the collection was empty at job start (recorded during pre-flight), and there is a 10 s timeout. It is reported as `targetCount` with a match or mismatch.
2. **Sample verification.**
   - Pick N = `verifySampleSize` (default 100) random source keys from the processed ones. Keys are sampled during the run with reservoir sampling over successful writes, so no extra source scan is needed.
   - Re-read those source rows by PK in a new transaction. Transform them with the same frozen program, `collection.Get` the documents, and compare them as **canonical JSON** (sorted keys, numbers normalized).
   - Results: `matched`, `mismatched` (with the diff paths, e.g. `$.total: 12.5 ≠ 12.50`), and `missing`. A source row changed since the migration (detected via `xmin` when available, or just reported) is reported as `sourceChanged` rather than a mismatch.
3. **Result** per mapping: `passed` | `passed_with_warnings` | `failed` (any `missing`, or more than 1% mismatched).

### Report (`job_reports.report`, also downloadable)
- `GET /jobs/{id}/report` (JSON).
- `GET /jobs/{id}/report.csv` (one row per mapping).
- `GET /jobs/{id}/report.html`: a print-friendly standalone HTML with the brand styling and a *Save as PDF* hint.

Report content:
- Job metadata: project, source (`db@host`, **no credentials**), target (cluster host, keyspaces), who started it, start and end times, duration, and options.
- Per mapping: rows read, written, skipped, failed, average doc size, throughput avg and peak, verification results, and the key template.
- Error summary by code, including a link to the full CSV (OC-026).
- The mapping specs used (frozen), for auditability.

### UI
- The job summary card (OC-028) shows a **Verification** section: per mapping ✓ Passed · 100/100 samples match · counts reconcile, or ⚠ with details.
- A *Mismatches* drawer shows each mismatch as a side-by-side JSON diff (expected from source vs actual in Couchbase), with differing paths highlighted.
- *Download report* is a split button: HTML (default) / JSON / CSV.

## Acceptance criteria & unit tests

### AC1 — Count reconciliation
**Given** read 10,000, written 9,990, failed 10, and the HLL distinct estimate ≈ 9,990, **then** counts reconcile. **Given** distinct ≈ 9,000 with written 9,990, **then** the warning `KEY_COLLISIONS_DETECTED` is raised with the estimate.

| Test | Location | Verifies |
|---|---|---|
| `TestReconcile_AC1_Balanced` | `internal/verify/reconcile_test.go` | |
| `TestReconcile_AC1_DetectsKeyCollisions` | same | Generates keys with 10% duplicates |
| `TestReconcile_AC1_TargetCountOnlyWhenEmptyAtStart` | same | |

### AC2 — Reservoir sampling of keys
**Given** a stream of 1,000,000 written keys and N=100, **then** exactly 100 keys are retained, and over many runs the selection is uniform (chi-square test passes at p > 0.01).

| Test | Location | Verifies |
|---|---|---|
| `TestReservoir_AC2_SizeAndUniformity` | `internal/verify/reservoir_test.go` | Seeded RNG |

### AC3 — Sample comparison
**Given** 100 sampled keys where 1 doc was modified in Couchbase and 1 was deleted, **then** matched=98, mismatched=1 (diff path listed), missing=1, and result=`failed`. Canonical comparison treats `{"a":1,"b":2}` and `{"b":2,"a":1}` as equal, and `1.0` vs `1` as equal.

| Test | Location | Verifies |
|---|---|---|
| `TestCompare_AC3_CanonicalJSON` | `internal/verify/compare_test.go` | |
| `TestVerifySample_AC3_Outcomes` | `internal/verify/sample_test.go` | Fakes for source and target |
| `TestVerify_AC3_Integration` *(integration)* | `internal/pipeline/verify_integration_test.go` | Real PostgreSQL + Couchbase; one document tampered after write |

### AC4 — Report generation
**Given** a finished job, **then** the JSON report contains every documented section, the CSV has one row per mapping with a header, and the HTML renders a standalone page with inline CSS. **None contain credentials** (asserted by searching for the stored password in all three outputs).

| Test | Location | Verifies |
|---|---|---|
| `TestReport_AC4_JSONSections` | `internal/verify/report_test.go` | Golden |
| `TestReport_AC4_CSV` | same | |
| `TestReport_AC4_HTMLStandalone` | same | No external URLs |
| `TestReport_AC4_NoSecrets` | same | |

### AC5 — Verification UI
**Given** the verification results, **then** the summary card shows the per-mapping status. The Mismatches drawer shows the side-by-side diff with highlighted paths, and the download menu offers three formats.

| Test | Location | Verifies |
|---|---|---|
| `VerificationSection.test.tsx` › `AC5: statuses` | `src/features/jobs/VerificationSection.test.tsx` | |
| `JsonDiff.test.tsx` › `AC5: highlights differing paths` | `src/components/JsonDiff.test.tsx` | |

## Implementation notes
- `internal/verify` holds the pure parts (reconcile, reservoir, canonical compare, sample verification, report rendering); `pipeline/verify.go` runs them after each mapping reaches `done`, because it needs the read session and the frozen transform program. The integration test therefore lives in `internal/pipeline`.
- A resumed run (OC-029) samples only keys written since the resume and warns `PARTIAL` (source rows still count the whole table). A table without a primary key can't be re-read by key, so it gets `passed_with_warnings` with the reason instead of a sample.
- `TestReport_AC4_NoSecrets` lives in `internal/jobs/report_test.go`, where the report is assembled from stored connections.
- UI: the summary card fetches `GET /jobs/{id}/report` and polls (bounded) until the last table's verification lands; *Download report* is a link to `report.html?download=1` with a menu for JSON and CSV.
