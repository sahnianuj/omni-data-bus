# OC-081 — Add fields anywhere in the target document, from every view

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 3 | OC-015, OC-048, OC-062 | Implemented |

## User story
As a **data engineer** shaping the target document, I want to see the fields inside an array of objects and add a field wherever I'm working (at the root, in an object or in an array's items), from the Canvas, Composer or Design view, so that I don't have to switch views or retype the design to add one field in the right place.

## Background (2026-10-02)
- **Design hid array items.** Its row list only descended into objects, so an `array<object>` such as `items` showed as one row with its item fields invisible, and nothing could be added to it.
- **Design's "Add field" was root-only.** The row menu offered "Add field" for objects only, and the link under the list always added at the root, whatever was selected.
- **The Canvas had no way to add a field** short of dropping a source column (which also maps it). The **Composer** could add only through each row's "…" menu or Enter/Tab, with no visible control.

## Scope
**In:**
- Design shows an array of objects' item fields under it (indented, collapsible like an object), and they can be renamed, typed, dragged and deleted like any other field.
- One placement rule for a new field, in all three views: **inside** the selected object or array of objects (at the end); otherwise **right after** the selected field, in its parent; with nothing selected, at the **end of the root**.
- A single-line **Add field** button under the target field list in all three views. Its label names where the field goes ("Add field to items") when that isn't the root; the hint beside it stays (height is premium: no extra row).
- Design's row menu offers "Add field" on arrays of objects as well as objects.
- The new field is selected and opens for renaming: Enter keeps the typed name, Escape keeps the default.
- On the Canvas, the new field gets a rename box in its row (the Canvas had no rename before).

**Out:** turning a scalar array (`array<string>`) into an array of objects from Design or the Canvas (change its type first; the Composer's menu keeps converting as before); adding objects or arrays from the Canvas (the type can be changed in Design).

## Acceptance criteria & unit tests

### AC1 — Design shows array items
**Given** a design with `items: array<object>` holding `sku` and `qty`, **then** Design lists `sku` and `qty` under `items`, one level deeper; collapsing `items` hides them; they can be dragged and edited.

### AC2 — One placement rule
**Given** the selection is an object or array of objects, **when** I add a field, **then** it goes at the end of that container (an array's item object). **Given** another field is selected, **then** it goes right after that field. **Given** nothing is selected, **then** it goes at the end of the root.

### AC3 — Add field in every view
**Given** the Canvas, Composer or Design view, **then** an **Add field** button sits under the target field list, labelled "Add field to ‹name›" when the field won't go to the root; clicking it adds a field by AC2, selects it and opens its name for editing.

### AC4 — Design's menu adds to arrays
**Given** an array of objects in Design, **then** its "…" menu offers **Add field**, which adds into its items.

| Test | Location | Verifies |
|---|---|---|
| `docschemaOps.test.ts` › `insertionPoint` | `web/src/features/docschema/docschemaOps.test.ts` | AC2 |
| `DocDesigner.test.tsx` › `array items and add field` | `web/src/features/docschema/DocDesigner.test.tsx` | AC1, AC3, AC4 |
| `MappingCanvas.test.tsx` › `add field` | `web/src/features/mapping/MappingCanvas.test.tsx` | AC3 |
| `DocumentTree.test.tsx` › `add field` | `web/src/features/composer/DocumentTree.test.tsx` | AC3 |
