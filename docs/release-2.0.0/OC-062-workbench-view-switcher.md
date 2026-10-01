# OC-062 — Visible, explained workbench views

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 3 | OC-048, OC-035 | Implemented |

## User story
As a **data engineer** working under *Design & map*, I want the four ways to work on a mapping (Canvas, Composer, Table, Design) to be obvious, explained and reachable from the mapping list, so that I find the composer when my target document has nested arrays, without discovering it by accident.

## Background (gap found 2026-09-30)
The view switcher is a small `text-xs` segmented control on its own row, below the score header and the auto-map banner (`MappingWorkbench.tsx`). Nothing says what each view is for. A mapping always opens on Canvas, or on Composer once it's composed. The *Design & map* step's mapping list (`TablesStep.tsx`) only links to the mapping, so users don't know the composer exists until they click around.

## Scope
**In:**
- **A view bar at the top of the workbench**, above the score header: a `tablist` with four tabs at normal text size, each with an icon, its name and a one-line description visible on wide screens (a tooltip on narrow ones):
  - *Canvas*: draw lines from columns to fields.
  - *Composer*: design a nested document and fill arrays and objects from related tables.
  - *Table*: every field and its source, as a list.
  - *Design*: fields, document key and JSON Schema.
- **Remembered view**: the last view per mapping (browser storage, per user), else the project default (a project setting, default Canvas). A composed mapping always opens in the Composer.
- **Deep links**: `/projects/:id/mappings/:mid?view=composer|canvas|table|design`.
- **The mapping list on *Design & map*** gets an *Open in ▾* split button per mapping (the four views), a *Composed* badge, and a column with the mapping's shape (*flat*, or *nested: 2 arrays, 1 object*).
- **A one-time hint** in Canvas when the design has arrays or objects bound to tables, or an imported design (OC-059): "This document has nested arrays — the Composer is made for that." with *Open Composer* and *Don't show again*.
- **Keyboard**: Alt+1…4 switch views; arrow keys move within the tablist.

**Out:** a fifth view, and changing what each view does.

## Implementation details
- `features/mapping/ViewBar.tsx` replaces the inline radiogroup. Switching still goes through `switchMode` (which converts to composed on the first switch to the Composer, OC-047).
- `useWorkbenchView(mappingId)` reads `?view=`, then `localStorage["odb.view.<mappingId>"]`, then `project.settings.defaultView`. Every read and write is wrapped in try/catch, and writes happen on switch.
- `TablesStep.tsx`: the *Open in* menu items link with `?view=`. The shape column comes from the list endpoint's existing `spec` summary (`relations.length`, by node type), with a new `shape` summary field (`{arrays, objects}`).
- The Canvas hint is dismissed per user in browser storage.

## Acceptance criteria & unit tests

### AC1 — Views are visible and explained
**Given** a mapping opened on a 1280 px screen, **then** the view bar is the first control under the page header, each tab shows its description, and the tablist passes axe.

| Test | Location | Verifies |
|---|---|---|
| `ViewBar.test.tsx` › `AC1: tabs with descriptions` | `src/features/mapping/ViewBar.test.tsx` | |

### AC2 — Remembered and linkable
**Given** the user leaves mapping m1 in Table view, **when** it's reopened, **then** it opens in Table. **Given** `?view=composer`, **then** it opens in the Composer, whatever was remembered. **Given** a composed mapping, **then** it opens in the Composer.

| Test | Location | Verifies |
|---|---|---|
| `ViewBar.test.tsx` › `AC2: remembered, deep link wins` | same | |

### AC3 — Open in a view from *Design & map*
**Given** the mapping list, **then** each row has *Open in ▾* with the four views, composed mappings show a *Composed* badge, and the shape column reads "nested: 2 arrays, 1 object" for the composer fixture.

| Test | Location | Verifies |
|---|---|---|
| `TablesStep.test.tsx` › `AC3: open in a view` | `src/features/projects/steps/TablesStep.test.tsx` | |

### AC4 — The Composer is suggested where it fits
**Given** a canvas mapping with an imported design that has arrays, **then** the hint shows once. *Open Composer* switches to it; *Don't show again* hides it for good.

| Test | Location | Verifies |
|---|---|---|
| `MappingWorkbench.test.tsx` › `AC4: composer hint` | `src/features/mapping/MappingWorkbench.test.tsx` | |

### AC5 — Keyboard
**Then** Alt+1…4 switch views, arrow keys move between tabs, and focus stays on the tab.

| Test | Location | Verifies |
|---|---|---|
| `ViewBar.test.tsx` › `AC5: keyboard` | same | |

## Implementation notes (2026-10-01)
- **Single-line view bar (user decision):** height is premium, so `ViewBar` shows four compact, clearly button-styled tabs (border, background, icon and name; the selected one is filled). The one-line description is the tab's tooltip (with its Alt shortcut) and its accessible description, not visible text; a first version with the description under each name was rejected. It sits right under the mapping header, above the score header. Arrow keys / Home / End move within it; Alt+1…4 switch from anywhere.
- `viewPrefs.initialView`: `?view=` (from the router location) wins, then a composed mapping's Composer, then the view remembered in browser storage (`odb.view.<mappingId>`), then Canvas. **Not built:** the per-project default view setting (it needs a project settings API); Canvas is the default.
- The Canvas hint (*Open Composer* / *Don't show again*, `odb.hint.composer`) shows when relations are bound to fields, or an imported design has arrays.
- *Design & map* list (`MappingsStep.tsx`, not `TablesStep.tsx`): an *Open in ▾* menu next to *Open* (links with `?view=`), and the document's shape from the summary's new `shape {arrays, objects, groupBy}` ("nested: 2 arrays, 1 object · one per distinct order_id"). The *Composed* badge was already there.
- To keep the composer to one toolbar line: *Import JSON design…* stays a button, *Blank* and *Generated from table* moved into a *Start over ▾* menu, the two mapping-sheet downloads into one *Mapping sheet ▾* menu, the strict checkbox is labelled *Strict* (full text in its tooltip), and the driving-table label is screen-reader text (and the select's tooltip). The Problems panel collapses to one line, and the document tree keeps a minimum height.
- Tests: `ViewBar.test.tsx` (AC1, AC2, AC4, AC5), `MappingsStep.test.tsx` (AC3); every test and E2E spec now finds the views as `tab`s.
