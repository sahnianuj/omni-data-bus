# OC-075 — Unmapped fields of an imported design are information, not warnings

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 1 | OC-061, OC-070, OC-071 | Implemented |

## User story
As a **data engineer** mapping into a customer's own JSON design, I want fields of that design that I haven't mapped (yet, or on purpose) to stop showing up as warnings, so that the Problems panel only lists things that are actually wrong. A customer's design often has many fields that the source doesn't fill.

## Background (2026-10-01)
After grouping order lines and embedding `product`, the Problems panel showed `products[].productId · REFERENCE_FIELD_UNMAPPED` as a warning, with a warning icon on `each item` in the tree. The documents were correct; the field was simply left out. With real customer designs this means many warnings for fields nobody intends to fill. OC-070 already made the opposite case (`REFERENCE_EXTRA_FIELD`, fields added outside the design) `info` unless the mapping is strict.

## Scope
**In:**
- `REFERENCE_FIELD_UNMAPPED` is `info` normally, and an error when the mapping is `referenceStrict` (as `REFERENCE_EXTRA_FIELD`). Both forms: a design field that no longer exists, and one that nothing fills.
- Info issues already stay out of the Composer's Problems panel and the tree's warning icons. They still appear, with their fix buttons (*Add productId back*, *Map from …*), in the sample pane's Reference box and, with an info icon, in the Canvas validation panel. The mapping sheet (CSV/MD) still lists them.

**Out:** `REFERENCE_TYPE_MISMATCH` (stays an error: the document would have the wrong type), `REFERENCE_NULLABLE` (stays a warning: a required field can come out null), and `REQUIRED_FIELD_UNMAPPED` (a field marked required in the design stays an error).

## Acceptance criteria & unit tests

### AC1 — Info unless strict
**Given** an imported design with a field nothing fills, and one deleted from the design, **then** both are `info` `REFERENCE_FIELD_UNMAPPED` issues with their fixes, and the mapping is ready. **Given** `referenceStrict`, **then** both are errors.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC4_ReferenceStrict` | `backend/internal/mapping/validate_reference_test.go` | Unfilled field: info, strict error |
| `TestValidate_ReferenceTypeAndMissing` | same | Deleted field: info |

### AC2 — Not in the Problems panel, fix still offered
**Given** an `info` `REFERENCE_FIELD_UNMAPPED` issue with a fix, **then** the Composer's Problems panel doesn't list it, and the Reference box shows its fix button.

| Test | Location | Verifies |
|---|---|---|
| `ProblemsPanel.test.tsx` › `OC-075 AC2: info issues aren't listed` | `web/src/features/composer/ProblemsPanel.test.tsx` | |
| `SamplePane.test.tsx` (existing restore-field test, issue now `info`) | `web/src/features/composer/SamplePane.test.tsx` | |
