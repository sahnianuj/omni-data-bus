# OC-153 — Mapping edits are kept when leaving Design & Map

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P0 | 2 | OC-019, OC-043 | Implemented |

## User story
As a **data engineer**, I want every change I make in Design & Map, embeds included, to still be there when I come back from another step, so that I never lose work or hit a version conflict for moving around the project.

## Background (2026-10-15)
Two gaps in the workbench:
- Adding an embed is saved by the server, and the workbench takes the server's copy, but the cached mapping wasn't updated (it never refetches by itself). Leaving to explore the source and coming back reopened the copy first loaded: the embed seemed gone, and the next edit was refused as a version conflict.
- An edit made less than 800 ms before leaving was dropped: leaving cancelled the pending autosave.

## Scope
**In:**
- The cached mapping follows every save and every server-side change (embed, auto-map, design replace, reload), including saves that finish after the page was left.
- Leaving the workbench saves a pending edit at once instead of dropping it.

**Out:** a warning when leaving with unsaved edits (no longer needed); offline editing.

## Acceptance criteria & unit tests

### AC1 — Pending edits are saved on leave
**Given** an edit whose autosave hasn't run yet, **when** I leave the page, **then** it is saved, and the saved version is reported.

### AC2 — Coming back shows what was saved
**Given** I embedded a table and left the step, **when** I come back, **then** the embed is there and the next edit saves without a conflict.

| Test | Location | Verifies |
|---|---|---|
| `useMappingAutosave.test.ts` › `leaving the page saves a pending edit at once, and reports the save` | `web/src/features/mapping/useMappingAutosave.test.ts` | AC1 |
| Manual: embed, go to Source, come back | — | AC2 |
