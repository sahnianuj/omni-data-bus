# OC-055 — Export dialog, export history & external run reports

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 5 | OC-053, OC-052 | Implemented |

## User story
As a **data engineer**, I want an Export tab in the project where I choose what to export and how, download the script, see who exported what and when, and upload a run's `report.json` to see production results next to my test runs, so that the hand-off to operations is simple and traceable.

## Scope
**In:**
- An **Export** tab in the Migrate step: a guided dialog and the download.
- Export history (metadata only, never the file or secrets).
- Importing an external `report.json` as a read-only run in the project's job list.

**Out:** pushing runs to remote clusters from the tool (the tool never connects to the customer's production cluster), and live progress from external runs.

## Implementation details

### Export dialog (`src/features/export/ExportDialog.tsx`)
A four-step form (react-hook-form + zod):
1. **Mappings**: checkboxes with validation status (invalid ones disabled, with the reason, OC-049).
2. **Target**: *As configured* or *Override*: directory (path on the runner machine, layout, compression) or Couchbase (connection string plus credentials, entered here and never stored).
3. **Credentials**: sealed (passphrase, with *Generate*), external (ref editor for each secret), or plaintext (typed confirmation, OC-052).
4. **Runtime & options**: script (auto, docker, embedded plus os/arch), *Include separate bundle file*, and job options (readers, writers, batch size, write mode, error threshold).

- Then a step-up password prompt if needed, and a download. The summary shows "Passphrase shown only once. Store it in your secret manager." with a copy button.
- The file downloads through a streaming response (`Content-Disposition`). The zip is built in memory with a 50 MB limit (the embedded binary is the largest part).

### Export history
- `exports` collection documents ([architecture §5](00-architecture.md#5-domain-model-changes-metadata-store)). `GET /projects/{projectId}/exports` → a table of who, when, mappings, target, credential mode, runtime, odb version and bundle sha256.
- A **Compare with current** action shows whether the project's mappings changed since that export (plan-hash comparison), with a pointer to `odb bundle diff`.

### External run reports
- `POST /projects/{projectId}/runs/import` (multipart `report.json`, ≤ 5 MB): checked against the report JSON Schema. The report's `bundleSha256` must match an export of this project, otherwise `422 REPORT_UNKNOWN_BUNDLE`.
- Stored as a `jobs` document with `origin: external`, status from the report, counts, duration, and the error summary (the top 100 error samples, keeping the 4 KB limit).
- The job list shows an "External" badge. The job page shows the report, with no monitor or controls.

## Results (2026-10-02)
- Export page (`/projects/:id/export`, from the Review step's *Export…* and under Migrate once a run exists): export history with *Compared with now*, *New export* (4-step dialog), *Import report*. Step-up is asked inline when the server needs it; the passphrase is shown once after the download.
- `exports` collection (provisioning step 5), KV-only like relationships so the import's bundle lookup sees an export at once; external runs are jobs with `origin: external` and `external` (the report summary), created without the active-job lock and read-only (no pause/resume/retry/cancel, no output browser).
- Checked in the browser against the running server: Review, Export page, every dialog step, the step-up prompt, the imported run.

### Changes from the plan
- The page is a project route rather than a tab inside the job monitor: the Migrate step only unlocks after a run, and exporting shouldn't need one.
- `JobList.test.tsx` is `JobsPage.test.tsx` (the job list) plus a case in `JobMonitorPage.test.tsx` for "no controls"; history tests are `ExportHistory.test.tsx` (web) and the store contract `storetest.RunExports` (memstore and cbstore), instead of `internal/store/exports_integration_test.go`.
- History compares plan hashes per mapping and lists what changed (or was deleted) in the badge's tooltip, with a pointer to `odb bundle diff`.

### Change (user decision, 2026-10-02): a read-only summary, then passphrase and runtime
The dialog has three steps: *Project* (source, target, credentials and mappings shown read-only, each with *Change in <step>*), *Passphrase* (with *Generate*), *Runtime & options*. The history drops the credentials column (always sealed) and the override marker.

## Acceptance criteria & unit tests

### AC1 — Guided export
**Given** 2 valid mappings and 1 invalid, **then** the invalid one can't be selected. **When** the user picks directory override, sealed with a generated passphrase, and the script, **then** the download contains `migrate.sh` and `README.md`.

| Test | Location | Verifies |
|---|---|---|
| `ExportDialog.test.tsx` › `AC1: full flow produces request` | `src/features/export/ExportDialog.test.tsx` | MSW |
| `TestExportsAPI_AC1_ZipContents` | `internal/httpapi/exports_test.go` | |

### AC2 — Override credentials never stored
**Given** a Couchbase target override with a password, **then** it appears only inside the sealed bundle, and not in the `exports` document, logs or audit.

| Test | Location | Verifies |
|---|---|---|
| `TestExportsAPI_AC2_OverrideCredsNotPersisted` | `internal/httpapi/exports_test.go` | Secret scanner over store and logs |

### AC3 — History
**Given** three exports, **then** history lists them newest first with the credential mode and sha256. **Given** a mapping edited after an export, **then** *Compare with current* shows "changed since export".

| Test | Location | Verifies |
|---|---|---|
| `TestExportsRepo_AC3_ListAndCompare` *(integration)* | `internal/store/exports_integration_test.go` | |
| `ExportHistory.test.tsx` › `AC3: changed badge` | `src/features/export/ExportHistory.test.tsx` | |

### AC4 — Import external report
**Given** a `report.json` from a run of an export of this project, **then** a job with `origin: external` appears with its counts. **Given** a report from another project's bundle, **then** `422 REPORT_UNKNOWN_BUNDLE`. **Given** an invalid schema, **then** `422` with field errors.

| Test | Location | Verifies |
|---|---|---|
| `TestRunsImport_AC4_Valid` | `internal/httpapi/runs_import_test.go` | |
| `TestRunsImport_AC4_UnknownBundleAndSchema` | same | |
| `JobList.test.tsx` › `AC4: external badge, no controls` | `src/features/jobs/JobList.test.tsx` | |
