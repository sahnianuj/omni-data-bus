# OC-139 — Computed fields follow changes to the document

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Computed Fields | P1 | 3 | OC-136, OC-138, OC-061, OC-064 | Not started |

## User story
As a **data engineer** who keeps reshaping the target document, I want computed fields to keep working when I rename, move or remove the fields they read, and to see what each computed field uses and is used by, so that a computed value never silently turns null after an edit.

## Background
OC-136 resolves the paths in a computed expression to field ids at compile time. The expression text still holds names, though: renaming `firstName` to `givenName` would break `${doc.firstName}`. Several existing actions also remove fields: deleting a field, *Collapse to id* (OC-064), removing an embed (OC-069), embedding a foreign key that removes the key field (OC-082), and re-importing a design (OC-061).

## Scope
**In:**
- **Rename and move** a field (any view, including drag and drop, OC-081): every template and every static path in a JavaScript expression that reads it is rewritten in the same undo step (`doc.firstName` → `doc.givenName`; `self.qty` → `parent.qty` when the item moves out of the array, or `doc.…` when no relative form fits). Dynamic access (`doc[name]`) isn't rewritten; the field gets the info issue from OC-137.
- **Remove** a field that a computed field reads (delete, collapse, remove embed, OC-082 key removal, re-import): the removal goes ahead, the computed field gets `COMPUTED_UNKNOWN_FIELD` with fixes **Edit expression** and **Remove computed value**, and the action's confirmation (where there is one) names the computed fields affected ("`fullName` reads `lastName`").
- **Uses / Used by:** the Inspector shows one line "Uses: firstName, lastName · Used by: label" with each name a link that selects the field.
- **Interplay with existing features:**
  - Auto-map, scoped auto-map and binding suggestions (OC-017/049/060) never map onto a computed or fixed field.
  - Imported designs (OC-059/061): a computed field in the design counts as mapped for `REFERENCE_FIELD_UNMAPPED`; the mapping sheet (CSV/Markdown) lists its kind and expression in the *Source* column.
  - Mode switch canvas ↔ composed (OC-048) keeps computed fields.
  - Changing the target kind or relocating mappings (OC-068) doesn't touch them.

**Out:** rewriting dynamic access; cross-mapping references.

## Implementation details
- `web/src/features/computed/refs.ts`: `parseRefs(computed)` returns path spans (start/end offsets, root name, field id) using the same grammar as the server (a small JS tokenizer for member chains; templates by `${…}` spans). `rewriteRefs(spec, fieldId → newPath)` edits the spans from right to left. A shared fixture `api/fixtures/computed-refs.json` keeps the TS parser and the Go analyser (OC-136) in step.
- Hooks in `composerOps.renameNode`, `moveRelative`, `deleteNode`, `collapseToId`, `removeEmbed`, and the server's `dropKeyFields` (OC-082) and `docschema.Reconcile` (OC-061). Server-side removals report affected computed fields in their response so the UI can show the confirmation.
- `automap.Run*` and `SuggestBindings` skip targets whose mapping has `computed` or a `constant` step.

## Acceptance criteria & unit tests

### AC1 — Rename rewrites references
**Given** `fullName` = `doc.firstName.concat(" ", doc.lastName)`, **when** `firstName` is renamed to `givenName`, **then** the expression reads `doc.givenName.concat(" ", doc.lastName)`, the preview is unchanged, and one undo restores both.

| Test | Location | Verifies |
|---|---|---|
| `refs.test.ts` › `OC-139 AC1: rename rewrites template and js` | `web/src/features/computed/refs.test.ts` | Shared fixture |
| `TestComputedRefs_AC1_SharedFixture` | `backend/internal/transform/computed/refs_test.go` | Same fixture in Go |

### AC2 — Move rewrites relative paths
**Given** `items[].label` = `${self.sku}`, **when** `sku` is moved out of the items to the root, **then** the template becomes `${doc.sku}`.

| Test | Location | Verifies |
|---|---|---|
| `refs.test.ts` › `OC-139 AC2: move` | `web/src/features/computed/refs.test.ts` | |

### AC3 — Removal flags, never silently nulls
**Given** `label` reads `product.name`, **when** *Collapse to id* replaces `product` with `productId`, **then** the confirmation names `label`, and afterwards `label` has `COMPUTED_UNKNOWN_FIELD` with its two fixes and the mapping is invalid until fixed.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-139 AC3: removal flags computed fields` | `web/src/features/composer/composerOps.test.ts` | |
| `TestDropKeyFields_AC3_ReportsComputedReaders` | `backend/internal/automap/embeds_test.go` | |

### AC4 — Uses / Used by
**Given** `fullName` reads `firstName` and `lastName`, and `label` reads `fullName`, **then** `fullName`'s Inspector shows "Uses: firstName, lastName · Used by: label", and clicking `label` selects it.

| Test | Location | Verifies |
|---|---|---|
| `ComputedEditor.test.tsx` › `OC-139 AC4: uses and used by` | `web/src/features/mapping/ComputedEditor.test.tsx` | |

### AC5 — Auto-map and imported designs respect computed fields
**Given** a computed `fullName` and a source column `full_name`, **then** auto-map and suggestions leave `fullName` alone. **Given** `fullName` is in the imported design, **then** it isn't reported unmapped, and the mapping sheet lists it as `JavaScript: doc.firstName.concat(…)`.

| Test | Location | Verifies |
|---|---|---|
| `TestAutomap_AC5_SkipsComputed` | `backend/internal/automap/engine_test.go` | |
| `TestReconcile_AC5_ComputedCountsAsMapped` | `backend/internal/docschema/reconcile_test.go` | |
| `TestMappingSheet_AC5_ComputedSource` | `backend/internal/mapping/sheet_test.go` | |
