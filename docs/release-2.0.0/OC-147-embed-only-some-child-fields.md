# OC-147 — Embed only some of a child table's fields

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 3 | OC-022, OC-043, OC-075 | Implemented |

## User story
As a **data engineer**, I want to embed a related object with just the fields I need, so that I can keep a small copy of the child in the parent document and still migrate the child table in full through its own mapping.

## Background (2026-10-15)
Embedding a many-to-one table (`partner` in a cart document, say) generates one field per child column, and each `NOT NULL` column becomes a **required** field. Leaving one unmapped raised `REQUIRED_FIELD_UNMAPPED` errors (`partner.collectionType`, `store.currencyCode`) and blocked the mapping, although the user never chose to require them. Embedded arrays (one-to-many) never had this problem.

## Scope
**In:**
- A required field inside an embedded relation's object, left unmapped, is simply left out of the embedded object: no `REQUIRED_FIELD_UNMAPPED` issue.
- Still required: fields of the document's own table; fields an imported customer design marks required (OC-061, OC-075). A field the imported design marks optional gets the usual info-level `REFERENCE_FIELD_UNMAPPED`.
- A grouped document's rows (OC-063) are the table's own columns, so the rule doesn't apply to them.
- The mapping score's "required but unmapped" count follows the same rule on the server and in the browser (`RequiredToMap` / `requiredToMap`).

**Out:** changing how embedded fields are generated (they keep `required` from `NOT NULL`, which still documents the column).

## Acceptance criteria & unit tests

### AC1 — Partial embeds are valid
**Given** an embedded `address` object with a required `zip` field left unmapped, **then** validation reports no issue for `address.zip`, while an unmapped required top-level `email` still is an error.

### AC2 — Imported designs still rule
**Given** an imported design that requires `address.zip`, **then** it is `REQUIRED_FIELD_UNMAPPED`. **Given** the design makes it optional, **then** it is info `REFERENCE_FIELD_UNMAPPED`.

### AC3 — Same score everywhere
**Given** the shared stats fixture with a partly embedded many-to-one, **then** server and browser both report one required unmapped field.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_EmbeddedRequiredFieldsNeedNoMapping` | `backend/internal/mapping/validate_test.go` | AC1, AC2 |
| `stats-cases.json` › `many-to-one embed: a required embedded field left unmapped…` | `backend/internal/mapping/testdata/stats-cases.json` (run by `stats_test.go` and `computeStats.test.ts`) | AC3 |
