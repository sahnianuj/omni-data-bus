# OC-160 — An export is one folder: `migrate.sh`, `config.json`, `mappings.json`

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 3 | OC-055, OC-158, OC-159 | Implemented |

## User story
As a **data engineer** exporting a migration, I want one kind of download, always the same folder of files, with one choice (include odb or not), so that I don't have to understand "bundle", "script", runtimes and embedding to hand a migration over.

## Background (2026-10-16)
The export dialog's second step asks for **Script** or **Bundle**; Script then asks for a runtime (auto, binary, docker, embedded), a platform for embedded, and whether to add the bundle file. With OC-158 and OC-159 every one of those produces the same files except for an included `odb`, and the generic script finds odb on its own.

## Scope
**In:**
- **One download**, `odb-export-<project>-<yyyymmdd>.zip`, unpacking to one folder with `migrate.sh` (0755), `config.json` (0600), `mappings.json` (0644), `README.md`, and `odb` (0755) when included.
- **The dialog's second step is "Options"**: a checkbox **Include odb for a machine without internet access**, with the platform picker when checked (platforms this server has), and the run options (readers, writers, batch size, write mode, max errors) as before. The Bundle choice, runtime list and "include bundle" option are gone. The done screen lists the files.
- **API:** `POST /projects/{id}/exports` takes `{ "includeOdb": {"os": …, "arch": …} | null, "jobOptions": … }`. The old `runtime` and `script` fields are still accepted (marked deprecated: any `runtime`; `script.runtime: "embedded"` with `os`/`arch` means include odb) so older clients keep working. The response's `X-Odb-Included` header names the platform of an included odb (also when the server includes its own because nothing can be downloaded), for the done screen. Export history records runtime `script`.
- **README.md** lists the files, the commands, and how to run the same `migrate.sh` with other `config.json` / `mappings.json` files.

**Out:** the Kubernetes kit (descoped, OC-054); several environments in one export.

## Acceptance criteria & unit tests

### AC1 — One kind of download
**Given** a project with ready mappings, **when** I export with the defaults, **then** the zip holds the four files in one folder; **when** I tick **Include odb** for linux/amd64, **then** it also holds `odb`.

| Test | Location | Verifies |
|---|---|---|
| `ExportDialog.test.tsx` › `OC-160 AC1: one download with an optional odb` | `web/src/features/export/ExportDialog.test.tsx` | Dialog and request |
| `TestExportsAPI_AC1_ZipContents` | `backend/internal/export/handlers_test.go` | Files, modes, with and without odb, `X-Odb-Included` |
| `TestScript_ZipContents` | `backend/internal/export/script_test.go` | One folder; odb is the binary itself |
| `TestScript_IncludesOdbWhenNothingDownloads` | same | A server without release downloads includes its own odb |
| `export-script.spec.ts` | `web/tests/e2e/export-script.spec.ts` | Export from the UI, unzip, run `migrate.sh` in docker, import the report (e2e stack) |

### AC2 — Older requests
**Given** a request with `runtime: "bundle"`, or `runtime: "script"` with `script.runtime: "embedded"`, **then** it returns the same folder (with odb for the embedded one).

| Test | Location | Verifies |
|---|---|---|
| `TestExportsAPI_OC160_AC2_OlderRequests` | `backend/internal/export/handlers_test.go` | |
