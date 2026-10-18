# OC-187 — The wheel separates fields filled with data from fields left empty

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 Mapping Canvas | P1 | 3 | OC-178 | Implemented |

## User story
As a **mapping designer**, I want the score to show how much of the document is actually filled with data, apart from what I deliberately left empty, and how many source columns are used across all tables, so that "98% done" can't hide a document that's half empty.

## Background (2026-10-17)
OC-178 made the wheel count fields that are **done**: filled by a mapping, a total or an embed, **or** marked not populated. Marking an object not populated counts every field under it as done. The rest of the header measures the other side, the source columns:
- *64% coverage (18 of 28 columns)* for the root table;
- *52 of 84 embedded columns mapped* for the embedded tables;
- a bar whose grey segment is labeled *unmapped*.

Together they read as competing scores. In a mapping showing "98 of 100 target fields done", the screen can't tell 98 filled fields from 60 filled and 38 left empty. The grey "10 unmapped" source columns look like a problem, when unused columns are normal.

## Scope
**In:**
- **The stats split *done***:
  - `targetFieldsFilled` counts the fields a mapping, a total or an embed fills;
  - `targetFieldsNotPopulated` counts the fields done only because they, or a container above them, are marked not populated;
  - `filled + notPopulated = done`.

  The server (`mapping.Stats`) and the web (`computeStats`) compute the same counts, and the API returns them.
- **The wheel shows two arcs**: solid for filled, lighter for not populated, and the gap for what's left. The center number stays **% done** (progress to finishing). The label reads "98% done: 73% filled with data, 25% not populated".
- **The header**:
  - headline *98 of 100 target fields done*;
  - a line *73 filled with data · 25 not populated · 2 to go (1 required)* with *Show them*;
  - a single source line *Source columns used: 70 of 112 (18 of 28 from orders, 52 of 84 embedded)*, replacing the separate coverage and embedded figures;
  - the bar legend's grey segment *not used* instead of *unmapped*;
  - average confidence unchanged.
- **The Mappings page** wheel uses the same two arcs and label.

**Out:** changing what *done* means, the auto-map score, and per-node scores.

## Acceptance criteria & unit tests

### AC1 — Done is split into filled and not populated
**Given** a document with 4 fields, one mapped, one marked not populated, and an object of 2 fields marked not populated, **then** done = 4, filled = 1, not populated = 3. The server and the web agree.

| Test | Location | Verifies |
|---|---|---|
| `TestStats_OC187_AC1_FilledAndNotPopulated` | `backend/internal/mapping/stats_test.go` | |
| `computeStats.test.ts` › `OC-187 AC1` | `web/src/features/mapping/` | |

### AC2 — Two arcs
**Then** the ring draws a filled arc and a not-populated arc whose lengths match the ratios, and its label names both.

| Test | Location | Verifies |
|---|---|---|
| `ScoreRing.test.tsx` › `OC-187 AC2` | `web/src/features/mapping/` | |

### AC3 — The header reads as one story
**Then** the header shows the split line with the required count, and one *Source columns used* line that combines the root and embedded tables. The legend says *not used*.

| Test | Location | Verifies |
|---|---|---|
| `MappingScoreHeader.test.tsx` › `OC-187 AC3` | `web/src/features/mapping/` | |

## Implementation notes (2026-10-17)
- **Stats**:
  - `mapping.Stats` gains `targetFieldsFilled` and `targetFieldsNotPopulated`, and `fieldsDone` returns both.
  - A field that something fills counts as filled even under a not-populated container. Otherwise a field marked, or under, not populated counts as not populated. `done = filled + notPopulated`.
  - The web's `computeStats` and `fieldsDone` mirror this.
  - The shared fixture `backend/internal/mapping/testdata/stats-cases.json` has the new expected values for every case, so the Go and TS parity tests cover them.
  - `api/openapi.yaml` (`MappingStats`) is updated and `schema.d.ts` regenerated.
- **`ScoreRing`** takes an optional `filled` ratio. It draws the filled arc solid and the not-populated arc at 40% opacity right after it; the track shows what's left. The center stays the done percentage. Without `filled`, it's one arc as before.
- **`MappingScoreHeader`**:
  - The ring label is "98% of the document's fields done: 73% filled with data, 25% not populated" when anything is left empty.
  - A `done-split` line: *N filled with data · N not populated · N to go (N required)* with *Show them*. Its tooltip is "Map the fields left, or mark them not populated."
  - One `source-used` line: *Source columns used: 70 of 112 (18 of 28 from the table, 52 of 84 embedded)*. It replaces *64% coverage (…)* and *52 of 84 embedded columns mapped*.
  - The grey bar segment says *not used*.
  - The required chip and the average confidence are unchanged.
  - The strings `mapping.toGo`, `mapping.coverage` and `embed.scoreEmbedded` are removed.
- **Mappings page**: the same split ring and label, plus *(18 not populated)* after *38 of 40 fields done*.
- **Tests**:
  - `TestStats_OC187_AC1_FilledAndNotPopulated` and the fixture parity tests (Go);
  - `computeStats.test.ts` › `OC-187 AC1`, `ScoreRing.test.tsx` (AC2), `MappingScoreHeader.test.tsx` › `OC-187 AC3` and the all-filled case, `MappingsStep.test.tsx` › `OC-187`;
  - the e2e `embeds.spec.ts` now matches the new embedded wording.
- **Also fixed**: `ByNode`'s doc comment in `stats.go` had drifted above `fieldsDone`, which revive flagged. It's back on `ByNode`.
- **Added after review (2026-10-17): the way back from *Show them*.**
  - *Show them*, a bar segment and the required chip set the workbench filter, but nothing showed a filter was on, and nothing cleared it short of reopening the mapping.
  - The Canvas/Table toolbar now shows a chip while a filter is on, for example *Showing only what's not mapped*, with **✕ Show all**, which sets the filter back to *all*.
  - Test: `MappingWorkbench.test.tsx` › "a filter set from the score header shows in the toolbar, and Show all clears it".
