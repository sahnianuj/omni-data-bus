# OC-138 — Computed field editor: value source, autocomplete and live results

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Computed Fields | P0 | 5 | OC-136, OC-137, OC-048, OC-081 | Not started |

## User story
As a **data engineer**, I want to add a field anywhere in the target document and set its value to a fixed value, a template or a JavaScript expression, with suggestions for the document's fields as I type and the result shown for real sample documents, so that I can build and check derived fields without running a migration.

## Background
OC-136/137 add computed fields to the spec and the engine. Today a fixed value needs a `constant` step in the transform pipeline editor (OC-020) on a field with no sources, which isn't discoverable, and nothing edits a computed rule.

## Scope
**In:**
- **Value source** in the Inspector of every target field, in the Canvas, Composer and Table views: a single-line segmented control **Source | Fixed | Template | JavaScript** (height is premium: one row, descriptions in tooltips).
  - **Source:** today's behaviour (mapped columns + transform pipeline).
  - **Fixed:** a typed value input (string, number, boolean, null, or JSON for objects and arrays). Stored as the existing `constant` op, so no spec change.
  - **Template / JavaScript:** the expression editor below, plus `onError` (Fail row | Null | Omit) and, for templates, *Null if any value is missing*.
  - Switching away from **Source** when the field has mappings asks once ("Remove the mapping from `first_name`?") and is undoable. Switching **Template → JavaScript** converts the text (wrap in backticks); **JavaScript → Template** is offered only when the expression is a plain template literal.
- **Add computed field:** next to the single-line **Add field** button (OC-081), an icon button **ƒx** (tooltip "Add computed field") that adds a field by the same placement rule, selects it and opens the Inspector on **JavaScript** with the cursor in the editor.
- **Expression editor** (extends `ExpressionEditor`, OC-020 AC8):
  - **Autocomplete** of `doc.`, `self.`, `parent.` and `row.` paths from the document design and the field's scope, with each field's type; string, number and array methods after a path of that type; the helpers (OC-137). Keyboard: ↑/↓, Enter/Tab to accept, Escape to close (ARIA combobox).
  - **Insert a field:** dragging a target field from the tree onto the editor, or Alt+click on it, inserts its path relative to the field (`self.qty` inside the same item, `doc.orderId` otherwise).
  - **Diagnostics** from the server compile (OC-136/137 issue codes) under the line, with a caret at the column, as today.
- **Live result:** under the editor, one line showing the value for the current sample document (`→ "Ada Lovelace"`), its type, and ◀ ▶ to step through the sample documents of the Sample pane (OC-049, up to 20). An item field shows the value for each item of the current document (first 5, then "+N more"). Errors show in place of the value, in the error colour, with the message. It reuses `previewMapping(…, { specOverride })` with a 300 ms debounce.
- **Marks in the tree:** a computed field shows an **ƒx** badge in every view (Canvas target list, Composer document tree, Design, Table). Its tooltip shows the expression (first 120 characters). A fixed value shows its value in muted text, as constants do today.
- **Canvas:** a computed field has no edge to the source side; its row shows the badge instead of "Unmapped". Selecting it highlights the target fields it reads (OC-136 dependencies).
- **Transform step palette:** the expr-lang **Expression** step is replaced by **JavaScript value** for new steps (it runs a `js` computed expression where `value` is the field's mapped value, before the remaining steps). Existing `expression` steps keep their editor.
- i18n strings, axe-clean in light and dark themes.

**Out:** a multi-line "notebook" editor or user-defined function library; editing computed fields in bulk; rename propagation (OC-139).

## Implementation details
- `web/src/features/mapping/ValueSourceControl.tsx` (segmented control, shared by `Inspector.tsx` and `ComposerInspector.tsx`); `ComputedEditor.tsx` (wraps `ExpressionEditor` with completions and the live result line).
- Completions come from a pure function `completionsAt(spec, fieldId, text, offset)` over `docSchema` and the scope (`scope.ts`), testable without the DOM. Method lists are static tables per type.
- `composerOps.setComputed(spec, fieldId, computed | null)` and `setFixed(spec, fieldId, value)`: one history entry each; removing the field's mapping in the same entry.
- Live result: `useComputedSample(fieldId)` selects the field's values from the preview response documents by path; no new endpoint.
- The add-step palette keeps `expression` behind a "Show legacy steps" toggle only when the mapping already uses it.

## Acceptance criteria & unit tests

### AC1 — Set a value source
**Given** a new field `fullName`, **when** I choose **Template** and type `${doc.firstName} ${doc.lastName}`, **then** the spec holds a template computed mapping, the tree shows **ƒx**, and the score counts the field as mapped. **When** I choose **Fixed** and enter `true` as a boolean, **then** the field has a `constant` step and shows `true`. **When** I switch a mapped field to **JavaScript**, **then** I'm asked once, the mapping is removed, and undo restores it.

| Test | Location | Verifies |
|---|---|---|
| `ValueSourceControl.test.tsx` › `OC-138 AC1: source, fixed, template, javascript` | `web/src/features/mapping/ValueSourceControl.test.tsx` | |
| `composerOps.test.ts` › `OC-138 AC1: setComputed / setFixed` | `web/src/features/composer/composerOps.test.ts` | |

### AC2 — Add a computed field
**Given** `items` is selected in the Composer, **when** I click **ƒx**, **then** a new field is added at the end of the items' fields, selected, with the Inspector on JavaScript and the editor focused. The same works in the Canvas and Design views.

| Test | Location | Verifies |
|---|---|---|
| `DocumentTree.test.tsx` › `OC-138 AC2: add computed field` | `web/src/features/composer/DocumentTree.test.tsx` | |
| `MappingCanvas.test.tsx` › `OC-138 AC2: add computed field` | `web/src/features/mapping/MappingCanvas.test.tsx` | |

### AC3 — Autocomplete
**Given** an item field and the text `self.`, **then** the suggestions are the item's fields with their types. **Given** `doc.email.`, **then** string methods (`substring`, `concat`, `replace`, …). **Given** `row.`, **then** the columns of the item's scope table. **Given** ↓ then Enter, **then** the suggestion is inserted and the cursor is after it.

| Test | Location | Verifies |
|---|---|---|
| `completions.test.ts` › `OC-138 AC3` | `web/src/features/mapping/completions.test.ts` | Pure function, table-driven |
| `ComputedEditor.test.tsx` › `OC-138 AC3: keyboard` | `web/src/features/mapping/ComputedEditor.test.tsx` | |

### AC4 — Live result and errors
**Given** sample documents, **then** the result line shows the value for the first one and ◀ ▶ step through them. **Given** an item field, **then** the values for the first 5 items. **Given** `doc.email.substring(0,`, **then** the syntax diagnostic shows under the line and the result line shows the error.

| Test | Location | Verifies |
|---|---|---|
| `ComputedEditor.test.tsx` › `OC-138 AC4: live result, items, errors` | `web/src/features/mapping/ComputedEditor.test.tsx` | MSW preview fixture |

### AC5 — Template ↔ JavaScript
**Given** the template `${doc.a}-${doc.b}`, **when** I switch to JavaScript, **then** the text is `` `${doc.a}-${doc.b}` ``. **Given** a JavaScript expression that isn't a single template literal, **then** **Template** is disabled with a tooltip saying why.

| Test | Location | Verifies |
|---|---|---|
| `ValueSourceControl.test.tsx` › `OC-138 AC5: convert` | `web/src/features/mapping/ValueSourceControl.test.tsx` | |

### AC6 — Accessible
**Given** the Inspector with a computed field in both themes, **then** axe reports no violations, the segmented control is a radio group, and the completion list is an ARIA listbox tied to the editor.

| Test | Location | Verifies |
|---|---|---|
| `Composer.a11y.test.tsx` › `OC-138 AC6` | `web/src/features/composer/Composer.a11y.test.tsx` | |
