# OC-154 — Export without a passphrase or a password confirmation

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 3 | OC-052, OC-055 | Implemented |

## User story
As a **data engineer** exporting a migration script, I want the export to download as soon as I've chosen how it runs, so that I'm not asked to make up a passphrase and then retype my password for a script whose connection details I'll edit anyway (OC-155).

## Background (2026-10-16)
The export dialog asked for a passphrase to seal the credentials (step 2), then for the user's password again ("Confirm your password", step-up) when they clicked Export. In practice, the credentials now travel in an editable `config.json` (OC-155, OC-156), so the passphrase protects nothing and the step-up only adds a prompt.

## Scope
**In:**
- The export dialog has two steps: **Project** and **Runtime & options**. There's no passphrase step, no "Confirm your password" prompt, and the download screen no longer shows a passphrase. Instead it says the connection details are in `config.json`.
- `POST /projects/{id}/exports` takes only `runtime`, `script` and `jobOptions`. A `passphrase` field is rejected as unknown (400). Exporting no longer needs a recent step-up.
- `POST /exports/passphrase` is removed, with the passphrase generator and strength check behind it (`seal.go`, `web/src/features/export/passphrase.ts`). `GET /exports/options` no longer returns `minPassphraseLength` or `stepUpWindowSec`. The `PASSPHRASE_WEAK` and `STEP_UP_REQUIRED` codes stay in the error enum; nothing returns them now.
- Exports stay editor-only, rate-limited (10 an hour) and audited (`bundle.exported`, credential mode `config`).

**Out:** the generic `POST /auth/step-up` endpoint (unchanged; nothing else uses it today); runners keep opening sealed, plaintext and external bundles made by older exports or tooling.

**Security note:** together with OC-156, anyone holding the zip can read the database credentials. base64 is encoding, not encryption. The README and the script header say so, and `config.json` is written with mode 0600.

## Acceptance criteria & unit tests

### AC1 — No password prompts
**Given** a project with ready mappings, **when** I open Export, pick how it runs and click Export, **then** the file downloads with no passphrase or password prompt, and the done screen mentions `config.json`.

### AC2 — API without a passphrase or step-up
**Given** an editor without a recent step-up, **when** they POST an export with only `runtime`, **then** it's 201 and audited with credential mode `config`. **When** the body has `passphrase`, **then** it's 400. A viewer still gets 403.

| Test | Location | Verifies |
|---|---|---|
| `ExportDialog.test.tsx` › `exports in two steps without asking for a passphrase or password` | `web/src/features/export/ExportDialog.test.tsx` | AC1 |
| `TestExportsAPI_AC4_Authorization` | `backend/internal/export/handlers_test.go` | AC2 |
| `TestExportsAPI_AC6_AuditEvent` | same | AC2 |
| `ExportDialog.test.tsx` › `shows a failed export in the dialog` | `web/src/features/export/ExportDialog.test.tsx` | Errors still show without the step-up path |
| `TestExportsAPI_Options` | `backend/internal/export/handlers_test.go` | No passphrase settings in the options |
