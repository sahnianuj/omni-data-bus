# OC-067 — Hide the Table view from the view bar

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P2 | 1 | OC-062 | Implemented |

## User story
As a **data engineer**, I want only the views I need in the view bar (Canvas, Composer, Design), so that the choice is simpler and the bar takes less room.

## Background (2026-10-01)
The user asked to hide *Table* unless it has a huge value. Its value overlaps others: field-by-field sources are in the Composer's tree and inspector, the Canvas, and the mapping sheet (OC-061). What it uniquely offers is a filterable list for very wide flat tables, used by the score header's "N required unmapped" link and the unmapped and suggestion filters. Not huge, so it leaves the bar but stays reachable where it's the right tool.

## Scope
**In:**
- The view bar shows Canvas, Composer, Design. Alt+1…3 follow that order.
- The Table view stays reachable through the score header's *required unmapped* link (it opens the filtered list), and through `?view=table`. Once shown, there's a *Back to Canvas* control.
- *Open in ▾* on the mapping list offers the three views.
- A remembered `table` view opens the Canvas instead.

**Out:** removing the Table view's code.

## Acceptance criteria & unit tests

### AC1 — Three views in the bar
**Then** the view bar has Canvas, Composer, Design. Alt+3 opens Design. The mapping list's *Open in* has the three.

| Test | Location | Verifies |
|---|---|---|
| `ViewBar.test.tsx` › `OC-067 AC1: three views` | `src/features/mapping/ViewBar.test.tsx` | |

### AC2 — Still there where it helps
**When** the user clicks *N required unmapped*, **then** the filtered list opens, with *Back to Canvas*. **Given** `?view=table`, **then** the list opens too.

| Test | Location | Verifies |
|---|---|---|
| `ViewBar.test.tsx` › `OC-067 AC2: table on demand` | same | |

## Implementation notes (2026-10-01)
- `viewPrefs.BAR_VIEWS` (Canvas, Composer, Design) drives the view bar, Alt+1…3 and *Open in ▾*. `VIEWS` still includes `table` for `?view=table` and the score header's *required unmapped* link. A remembered `table` opens the Canvas.
- In the Table view, a *Back to Canvas* button sits in the search row, and no bar tab is selected (Canvas stays focusable).
- Tests that used the Table tab to reach the field list now open `?view=table`. The a11y E2E spec checks Composer and Design.
