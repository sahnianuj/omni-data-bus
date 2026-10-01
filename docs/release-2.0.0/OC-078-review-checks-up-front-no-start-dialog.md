# OC-078 — Review step: checks up front, Migrate starts without a dialog

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E12 Harden & Ship 2.0 | P1 | 2 | OC-023 | Implemented |

## User story
As a **data engineer** on the Review & migrate step, I want anything that needs my attention (pre-flight warnings and failures, the row estimate) shown on the page before I click **Migrate**, and Migrate to start the job straight away, so that I make my decision with the facts in front of me instead of being asked again in a popup at the last moment.

## Background (2026-10-01)
Clicking **Migrate** ran the pre-flight checks and only then opened *"Start the migration?"* with the row estimate, targets and the warnings (e.g. *No SELECT on 4 table(s)…*, *the key template may produce the same key for different rows*). The user had already decided to migrate; warnings that need attention belong before that decision.

## Scope
**In:**
- Pre-flight runs on its own when the Review step opens and again 400 ms after the advanced options settle (a typed batch size doesn't run one check per key). **Run checks** stays, for re-checking after something changes outside the tool.
- The checklist (with warnings and failures, their details and hints) shows on the page. When the checks pass, the footer shows *≈ 42k rows · Upsert* next to the buttons.
- **Migrate** is enabled only when the latest checks are for the current options and passed; it creates the job and opens its monitor, with no dialog. The server checks again on create; if a check started failing in between, the error is shown and the checklist refreshes.
- Viewers don't run checks (the endpoint needs the editor role).

**Out:** moving individual warnings to earlier steps (source permissions to the Source step, key collisions to Design & map); acknowledging warnings one by one.

## Acceptance criteria & unit tests

### AC1 — Checks first, no dialog
**Given** the Review step opens, **then** the checks run once and their warnings and the row estimate are on the page. **When** Migrate is clicked, **then** the job starts and its monitor opens, with no dialog and no second check from the client.

| Test | Location | Verifies |
|---|---|---|
| `PreflightChecklist.test.tsx` › `OC-078 AC1: checks run on their own and Migrate starts without a dialog` | `web/src/features/jobs/PreflightChecklist.test.tsx` | |
| `PreflightChecklist.test.tsx` › `AC2: renders checks and blocks Migrate on failure` (no click on Run checks) | same | |

### AC2 — Options change, checks follow
**When** an option changes, **then** Migrate waits for a new check, which runs once after typing settles. **Given** a viewer, **then** no check runs.

| Test | Location | Verifies |
|---|---|---|
| `PreflightChecklist.test.tsx` › `OC-078 AC2: changing an option checks again before Migrate is enabled` | same | |
| `PreflightChecklist.test.tsx` › `OC-078: a viewer doesn’t run checks` | same | |
| E2E `Review.migrate` (no dialog step) | `web/tests/e2e/support/pages.ts` | |
