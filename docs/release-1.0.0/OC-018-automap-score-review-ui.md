# OC-018 — Auto-mapping score & suggestion review UI

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E4 Design & Map | P0 | 5 | OC-017, OC-004 | Implemented |

## User story
As a **data engineer**, I want to see at a glance how much of my table was mapped automatically ("10 of 23 fields automatically mapped"), review suggestions quickly, and see what is still left, so that I can trust the automation and finish the mapping fast.

## Implementation details

### Score header (top of the mapping workbench, `MappingScoreHeader`)
```
┌──────────────────────────────────────────────────────────────────────────────────────┐
│  ◔ 43%   10 of 23 fields automatically mapped                    [Re-run auto-map ▾]│
│          ▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇░░░░░░░░░░░░░░░░░░░░░░░                       │
│          ● 10 auto   ◐ 4 suggested   ● 3 manual   ○ 6 unmapped   ⊘ 1 ignored          │
│          Coverage 57% (13/23) · 2 required target fields unmapped ⚠   Avg conf. 94%  │
└──────────────────────────────────────────────────────────────────────────────────────┘
```
- **Score ring.** An SVG donut with an animated count-up on first load and a spring-animated update. The headline number is `AutoMapRatio`, and the ring color steps from zinc (< 40%) to indigo (40–80%) to emerald (> 80%).
- **Segmented bar.** Segments in `--map-auto / --map-suggested / --map-manual / --map-unmapped` proportional to the source columns. Each segment is a button that filters the canvas and table view to that state.
- **Coverage line.** Live values from `ComputeStats` after each edit, plus the required-unmapped warning chip, which scrolls to the first required unmapped target when clicked.
- The *Re-run auto-map ▾* split button offers *Fill gaps* (default) and *Reset auto & suggestions*, the latter with a confirm dialog.
- The header announces changes through `aria-live="polite"` (e.g., "Coverage 61%").

### First-run celebration
When a mapping is opened for the first time after creation, a dismissible callout slides in: "✨ We automatically mapped **10 of 23** fields. **4 suggestions** need your review." with the buttons *Review suggestions* and *Dismiss*. A per-mapping flag `seenAutomapIntro` in `localStorage` prevents it from showing again.

### Suggestion review panel (right sheet, `SuggestionReview`)
- A list of pending suggestions sorted by confidence. Each row shows `source_col  →  target.path`, a confidence pill (e.g., 78%), the reason text from `explain`, type badges, and any transform attached.
- Row actions: **Accept** (A), **Reject** (R), and **Change target…** (opens the target combobox).
- Bulk actions: *Accept all ≥ 75%* and *Accept all*, each with an undo toast.
- Keyboard: J/K move, A accepts, R rejects, Enter opens details.
- Accept sets `state=active, origin=manual` (the reason tag "accepted-suggestion" is kept). Reject sets `state=rejected`, which hides the suggestion and adds it to the blocklist so OC-017 won't suggest it again.
- An empty state after everything is reviewed: "All suggestions reviewed 🎉" with a CTA "Map remaining 6 fields →", which focuses the canvas filter on unmapped fields.

### Unmapped panel
A tab next to Suggestions:
- **Unmapped source columns**, each with *Ignore* (moves it to `ignoredColumns`) and *Create target field* (adds a field to the design with the name converted per naming style, then maps it). *Create fields for all unmapped* appends them all in one go.
- **Unmapped target fields**, with required fields first.

### Explain popover
Hovering or focusing any auto or suggested mapping in the canvas or table view shows the explanation: the score bar, the signals list, and the type compatibility line.

## Acceptance criteria & unit tests

### AC1 — Headline score
**Given** stats `{AutoMapped:10, TotalSourceColumns:23}`, **then** the header reads "10 of 23 fields automatically mapped" and the ring shows 43%. Pluralization is correct for 1 ("1 of 1 field automatically mapped"). **Given** 0 source columns, the header shows "No source columns".

| Test | Location | Verifies |
|---|---|---|
| `MappingScoreHeader.test.tsx` › `AC1: headline text and percent` | `src/features/mapping/MappingScoreHeader.test.tsx` | |
| `MappingScoreHeader.test.tsx` › `AC1: pluralization and zero state` | `src/features/mapping/MappingScoreHeader.test.tsx` | i18n plural keys |
| `scoreColor.test.ts` › `AC1: thresholds 40/80` | `src/features/mapping/scoreColor.test.ts` | |

### AC2 — Segmented breakdown & filtering
**Given** stats, **then** the segment widths are proportional (to within 0.5%), the labels show the counts, and clicking *unmapped* sets the workbench filter to `unmapped` (the canvas shows only unmapped rows).

| Test | Location | Verifies |
|---|---|---|
| `MappingScoreHeader.test.tsx` › `AC2: proportional widths` | `src/features/mapping/MappingScoreHeader.test.tsx` | |
| `MappingScoreHeader.test.tsx` › `AC2: click sets filter in store` | `src/features/mapping/MappingScoreHeader.test.tsx` | |

### AC3 — Live coverage updates
**Given** the user maps one more field manually, **then** the coverage recomputes immediately (client-side `computeStats`, identical to the server's), the aria-live region announces the new percentage, and the headline auto score is unchanged.

| Test | Location | Verifies |
|---|---|---|
| `computeStats.test.ts` › `AC3: parity with server fixtures` | `src/features/mapping/computeStats.test.ts` | Shares `testdata/stats-cases.json` with `TestComputeStats_*` |
| `MappingScoreHeader.test.tsx` › `AC3: announces coverage change` | `src/features/mapping/MappingScoreHeader.test.tsx` | |

### AC4 — Review suggestions
**Given** 4 pending suggestions, **then** they are listed by descending confidence with reasons. Accept → the mapping becomes active/manual and the counts update. Reject → it disappears and is recorded as rejected. *Accept all ≥ 75%* accepts only the qualifying ones and offers undo.

| Test | Location | Verifies |
|---|---|---|
| `SuggestionReview.test.tsx` › `AC4: sorted with reasons` | `src/features/mapping/SuggestionReview.test.tsx` | |
| `SuggestionReview.test.tsx` › `AC4: accept/reject update store and stats` | `src/features/mapping/SuggestionReview.test.tsx` | |
| `SuggestionReview.test.tsx` › `AC4: bulk accept threshold and undo` | `src/features/mapping/SuggestionReview.test.tsx` | |
| `SuggestionReview.test.tsx` › `AC4: keyboard J/K/A/R` | `src/features/mapping/SuggestionReview.test.tsx` | |
| `mappingOps.test.ts` › `AC4: acceptSuggestion/rejectSuggestion reducers` | `src/features/mapping/mappingOps.test.ts` | |

### AC5 — First-run callout
**Given** a mapping opened for the first time with auto results, **then** the callout shows the counts and never shows again after dismissal or review.

| Test | Location | Verifies |
|---|---|---|
| `AutomapIntro.test.tsx` › `AC5: shows once per mapping` | `src/features/mapping/AutomapIntro.test.tsx` | localStorage mocked, including the throwing case |

### AC6 — Unmapped panel actions
**Given** unmapped `loyalty_tier`, **then** *Create target field* adds `loyaltyTier: string` to the design root and maps it (origin manual). *Ignore* moves it to `ignoredColumns`, and the denominator decreases by 1.

| Test | Location | Verifies |
|---|---|---|
| `UnmappedPanel.test.tsx` › `AC6: create field and map` | `src/features/mapping/UnmappedPanel.test.tsx` | |
| `UnmappedPanel.test.tsx` › `AC6: ignore updates stats` | `src/features/mapping/UnmappedPanel.test.tsx` | |
| `UnmappedPanel.test.tsx` › `AC6: create for all unmapped` | `src/features/mapping/UnmappedPanel.test.tsx` | |

### AC7 — Re-run auto-map
**Given** *Reset auto & suggestions* is confirmed, **then** the API is called with `mode:"reset"`, the canvas refreshes, the headline auto score updates to the new snapshot, and manual mappings are kept.

| Test | Location | Verifies |
|---|---|---|
| `MappingScoreHeader.test.tsx` › `AC7: reset calls API and refreshes` | `src/features/mapping/MappingScoreHeader.test.tsx` | MSW |
