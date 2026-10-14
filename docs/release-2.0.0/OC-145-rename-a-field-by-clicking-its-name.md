# OC-145 — Rename a target field by clicking its name, in every view

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 2 | OC-015, OC-048, OC-081 | Implemented |

## User story
As a **data engineer** shaping the target document, I want to rename a field by clicking its name in the Composer, Canvas or Design view, so that I don't have to find the rename action in a "…" menu or switch to another view to fix a field name.

## Background (2026-10-09)
- **Design** renames on double-click, F2 or "…" → Rename. A single click only selects.
- **Composer** renames on double-click, F2, "…" → Rename or the Inspector's name box. A single click only selects.
- **Canvas** can't rename an existing field at all. Its only rename box is the one OC-081 opens for a just-added field.
- None of the views shows that a field name can be edited.

## Scope
**In:**
- The same gesture in all three views, as in a file manager: the **first click selects** the field (selection drives the Inspector, OC-081's add-field placement and Design's multi-select, so it keeps its meaning). **Clicking the name of the field that is already selected** opens a rename box in place. Clicking elsewhere on the row (type, badges, empty space) doesn't rename.
- **Double-click** on a row and **F2** on the selected field rename in all three views. The Canvas gets both for the first time.
- The selected field's name shows a text cursor and the tooltip "Click to rename (F2)". No extra row or button: height is premium.
- The rename box behaves the same everywhere: the name is pre-selected, **Enter** or clicking away keeps the typed name, **Escape** keeps the old one. Leading and trailing spaces are trimmed, and an empty name keeps the old one.
- A name the design refuses (a duplicate at that level, an invalid name) shows the existing error toast and keeps the old name.
- Mappings, bindings and embeds follow the field, since they point at its id (no change needed).
- In Design, a click on the name of one field in a multi-selection selects just that field. Only a click on the name of the sole selected field renames.

**Out:** renaming the document root (`$`) or an array's item row (`[]`) in the Composer (neither has a name of its own); rewriting computed-field references on rename (OC-139); renaming source columns.

## Acceptance criteria & unit tests

### AC1 — Click the selected field's name to rename
**Given** a field in the Composer, Canvas or Design view, **when** I click its name once, **then** it is selected and no rename box opens. **When** I click its name again, **then** a rename box opens with the name selected; typing a new name and pressing Enter renames the field.

### AC2 — Double-click and F2 everywhere
**Given** any of the three views, **when** I double-click a field row, or press F2 with a field selected, **then** its rename box opens. On the Canvas, F2 applies only while a field (not a mapping line or a column) is selected.

### AC3 — Same rename box behaviour
**Given** an open rename box, **then** Escape keeps the old name, clicking away keeps the typed name, an empty or all-space name keeps the old name, and surrounding spaces are trimmed. **Given** a sibling already has that name, **then** an error toast shows and the field keeps its old name.

### AC4 — Only the name renames
**Given** a selected field, **when** I click its type, badge or the empty part of its row, **then** no rename box opens. **Given** a Design multi-selection, **when** I click the name of one of the selected fields, **then** only that field stays selected and no rename box opens.

### AC5 — It's discoverable
**Given** a selected, renamable field, **then** its name has a text cursor and the tooltip "Click to rename (F2)". The Composer's root and item rows don't.

| Test | Location | Verifies |
|---|---|---|
| `DocDesigner.test.tsx` › `OC-145 click to rename` | `web/src/features/docschema/DocDesigner.test.tsx` | AC1, AC3, AC4, AC5 |
| `DocumentTree.test.tsx` › `OC-145 click to rename` | `web/src/features/composer/DocumentTree.test.tsx` | AC1, AC4, AC5 |
| `MappingCanvas.test.tsx` › `OC-145 click to rename` | `web/src/features/mapping/MappingCanvas.test.tsx` | AC1, AC2, AC3 |
