# OC-174 — Canvas: selecting a field brings its mapped field into view

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 Mapping Canvas | P1 | 2 | OC-019, OC-021 | Implemented |

## User story
As a **data engineer** working on a large mapping in the Canvas, I want clicking a source column to scroll the target pane to the field it fills (and clicking a target field to scroll the source pane to its column), so that I can see both ends of a mapping without hunting through two long lists.

## Background (gap found 2026-10-17)
Both Canvas panes are virtualized lists that scroll on their own. Clicking a source column (`select({kind: 'column'})`) highlights it and its lines, but when the field it fills is scrolled out of view, the line is only pinned to the pane's edge (`clampY`), and the user has to scroll the target pane to find it. The Canvas only scrolls by itself in two cases: when a new embed is added, and when a field is being named.

On a design-first mapping (OC-163) this matters more: the cart design has 98 fields, and a column's field is often several screens away.

## Scope
**In:**
- **Source → target.** Selecting a source column scrolls the target pane so its first mapped field is in view.
  - It doesn't scroll when the field is already visible (`align: 'auto'`).
  - A collapsed parent of the field expands first.
  - Clicking the same column again moves to its next mapped field (*1 of 3* next to the column, when there are several).
- **Target → source.** Selecting a target field scrolls the source pane to the column it reads, or its first column for a field with several sources. An embedded table's group expands first if it's collapsed.
- **Keyboard:** the same applies when the selection moves with the arrow keys.
- **Filters:** when the other end is hidden by the search or the *Unmapped / Needs review* filter, a small notice says so ("Filled by `pricing.totalToDisplay`, hidden by the filter") with **Show it**, which clears the filter.
- A mapping selected on its line (`select({kind: 'mapping'})`) brings both ends into view.
- Columns and fields with no mapping don't scroll anything.

**Out:** the Composer, whose tree already selects the field it shows; the Table view, a single list where the row is the mapping.

## Implementation details
- `MappingCanvas.tsx`:
  - an effect on `selection` finds the other end through `edges` (`from` = column key, `to` = field id);
  - `tgtIndex` / `srcIndex` give the row, and `tv.scrollToIndex(i, { align: 'auto' })` / `sv.scrollToIndex(...)` scroll to it;
  - parents are expanded with `toggleCollapsed` before looking up the index;
  - a `cycle` ref keeps the column and position for *next mapped field*.
- The effect skips selections the user made by scrolling (no loop), and doesn't run during a drag.
- **Hidden by a filter:** the edge exists but its row isn't in `tRows` or `srcRows`.

## Acceptance criteria & unit tests

### AC1 — Source click reveals the target field
**Given** the cart mapping with `total_to_display` mapped to `pricing.totalToDisplay`, scrolled out of view, **when** the user clicks `total_to_display`, **then** the target pane scrolls so `pricing.totalToDisplay` is visible and selected-related. **Given** it's already visible, **then** the pane doesn't move.

| Test | Location | Verifies |
|---|---|---|
| `MappingCanvas.test.tsx` › `OC-174 AC1` | `web/src/features/mapping/MappingCanvas.test.tsx` | `scrollToIndex` called with the field's row; not called when visible |

### AC2 — Several fields, collapsed parents
**Given** `customizations.name` fills `label`, `value` and `valueString`, inside a collapsed `items[]`, **when** the user clicks it, **then** `items[]` expands and `label` is in view, with *1 of 3*. Clicking again brings `value` into view (*2 of 3*).

| Test | Location | Verifies |
|---|---|---|
| `MappingCanvas.test.tsx` › `OC-174 … AC1` | `web/src/features/mapping/MappingCanvas.test.tsx` | Parent expanded, *1 of 2* → *2 of 2* → *1 of 2* |

### AC3 — Target click reveals the source column; filters
**When** the user clicks `savedAt`, **then** the source pane scrolls to `saved_orders.saved_at`. **Given** the *Unmapped* filter hides the other end, **then** the notice and **Show it** appear.

| Test | Location | Verifies |
|---|---|---|
| `MappingCanvas.test.tsx` › `OC-174 … AC2`, `AC3` | `web/src/features/mapping/MappingCanvas.test.tsx` | Source pane scrolled; notice and **Show it** |

## Implementation notes (2026-10-17)
- `MappingCanvas.tsx`: the click handlers (`onSourceClick`, `onTargetClick`, `onEdgeClick`) find the other end from the spec's mappings and totals rather than an effect on `selection`. This rules out scroll loops and drags.
  - Collapsed parents (`parentMap`) are expanded in the handler.
  - A `pendingReveal` ref is consumed after the next render by `tv.scrollToIndex` / `sv.scrollToIndex(i, { align: 'auto' })`, so nothing moves when the row is already in view.
- **Several fields:** sorted in document order. Clicking the column again moves to the next one, and a status line shows *first_name · 2 of 3*.
- **Hidden by the filter or search:** a status line reads *name.first is hidden by the filter.* with **Show it**, which sets the filter to *All* and clears the search.
- Not applicable:
  - the Canvas has no arrow-key selection, so the keyboard bullet has nothing to follow;
  - source groups don't collapse, so no group expands.
- Tests: `MappingCanvas.test.tsx` › `OC-174: selecting one end reveals the other` (AC1 covers the cycle and the collapsed parent, which the story lists as AC2).
