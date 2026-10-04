# OC-083 — A mapping card says what to fix, not "Draft"

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 2 | OC-035, OC-049 | Implemented |

## User story
As a **data engineer** on the Design & map step, I want each mapping card to tell me what stops it from being ready and take me straight there, so that I don't read "Draft" as "not saved" and wonder how to save it.

## Background (2026-10-02)
Every edit in the workbench is already saved automatically. "Draft" was the server's name for a mapping with at least one **error** in its validation (`mapping.Status`), but the card showed only the word, next to a 100% score ring, with no reason and no way to the problem. Users read it as an unsaved draft and looked for a Save button.

## Scope
**In:**
- The mapping list (`GET /projects/{id}/mappings`) returns, per mapping, `errors` and `warnings` counts and `firstError` (`code`, `message`) from the same live validation that sets its status.
- The card's status badge reads **"N to fix"** (warning tone) instead of "Draft"; "Ready" and "Stale" stay. Its tooltip says edits save automatically and the mapping becomes ready once these are fixed.
- A card with errors shows one more line: the first error's message, "and N more" when there are others, and a **Fix** link that opens the workbench with its Issues panel open (`?panel=issues`; the Composer shows its Problems panel anyway).
- `?panel=issues` opens the Canvas side panel on its Issues tab, whatever the window width.

**Out:** changing what counts as an error; the project-level "draft" status; the score ring.

## Acceptance criteria & unit tests

### AC1 — The list carries the reason
**Given** a mapping whose required field `orderId` is unmapped, **then** its summary has `status: draft`, `errors: 1` and `firstError.code: REQUIRED_FIELD_UNMAPPED`; a ready mapping has `errors: 0` and no `firstError`.

### AC2 — The card says what to fix
**Given** that mapping, **then** its card shows "1 to fix" and the error's message; **when** I click Fix, **then** the workbench opens with the Issues tab showing. A ready mapping shows "Ready" and no extra line.

| Test | Location | Verifies |
|---|---|---|
| `TestList_OC083_ErrorSummary` | `backend/internal/mappings/service_test.go` | AC1 |
| `MappingsStep.test.tsx` › `OC-083` | `web/src/features/mapping/MappingsStep.test.tsx` | AC2 |
