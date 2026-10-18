# OC-169 — Unresolved fields say why, and offer a fix

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P1 | 3 | OC-168, OC-061, OC-042 | Implemented |

## User story
As a **data engineer**, I want every field the proposal couldn't fill to say why and what would fix it, and I want a coverage sheet to send the customer, so that I can close the gaps or agree with the customer which fields stay empty.

## Background (gap found 2026-10-17)
On the cart design some fields have no source, or only a weak one:
- `updatedAt` is required, but `carts` has no such column (only `last_priced_at` and `dds_synced_at`).
- `taxDataID` is a reference to another document, built from `carts.id`.
- `payments[]` items are generic in `cart.schema.json` (`{"type": "object"}`). Their shapes come from separate variant files (OC-171).

OC-060 says "unresolved, best candidate X". It doesn't say what kind of fix applies.

## Scope
**In:**
- **A reason code for each unresolved field or container**, with its fix as a button in the review (OC-168):

  | Code | Meaning | Fix offered |
  |---|---|---|
  | `NO_COLUMN` | No column scores ≥ 0.5 in scope or in a 1:1 lookup | *Pick a column…* (best 3 shown), *Constant…*, *Mark not populated* |
  | `NO_PATH` | No relationship from the scope reaches a table that fits | *Add virtual relationship…* (OC-042), prefilled with the best table |
  | `TYPE_MISMATCH` | The best column can't convert to the field's type (OC-167) | *Pick a column…*, *Expression…* |
  | `DEPTH_LIMIT` | Deeper than `OC_MAX_RELATION_DEPTH` | The OC-142 tooltip |
  | `REFERENCE_TEMPLATE` | The field looks like another document's key (`tax_data::{uuid}` in its examples) | *Use expression* `concat('tax_data::', row.id)` |
  | `UNTYPED_ITEMS` | The array items have no properties (`{"type":"object"}`) | *Import item designs…* (OC-171) |

- **A required field** (design `required`) that's unresolved stays in *Needs review* until it's fixed or marked not populated, and **Apply** warns about it.
- **The coverage sheet.** The OC-061 mapping sheet gains a `proposal` status column (`mapped`, `converted`, `constant`, `not populated`, `unresolved: <code>`) and a summary row. It's downloadable from the review banner.

**Out:** fixing the source (adding columns); changing the customer's design.

## Acceptance criteria & unit tests

### AC1 — Reasons on the cart design
**Then**:
- `updatedAt` is `NO_COLUMN`, with `last_priced_at` among its candidates, and it's flagged required;
- `taxDataID` is `REFERENCE_TEMPLATE`, proposing `concat('tax_data::', row.id)`.

| Test | Location | Verifies |
|---|---|---|
| `TestPropose_OC169_AC1_Reasons` | `backend/internal/automap/propose_test.go` | |

### AC2 — Fixes from the review
**When** the user clicks *Pick a column…* on `updatedAt` and chooses `last_priced_at`, **then** it's mapped and leaves *Needs review*. **When** they click *Add virtual relationship…* on a `NO_PATH` container, **then** the dialog opens prefilled.

| Test | Location | Verifies |
|---|---|---|
| `ReviewMode.test.tsx` › `OC-169 AC2` | `web/src/features/composer/ReviewMode.test.tsx` | |

### AC3 — Coverage sheet
**Then** the CSV has one row per design field, with the proposal status and reason code, and a summary row with the counts that the banner shows.

| Test | Location | Verifies |
|---|---|---|
| `TestMappingSheet_OC169_AC3_ProposalStatus` | `backend/internal/mapping/sheet_test.go` | |

## Implementation notes (2026-10-17)
- Each proposal item has a `code` (`NO_COLUMN`, `NO_PATH`, `TYPE_MISMATCH`, `DEPTH_LIMIT`, `REFERENCE_TEMPLATE`, `UNTYPED_ITEMS`), `candidates`, `required`, and a `fix` for reference templates.
- A field with no column alike by name gets the row's unused columns of the same type as candidates (`updatedAt` → `carts.last_priced_at`).
- **Fixes in `ProposalPanel`:**
  - *Use table.column* for in-scope candidates;
  - *Mark not populated*;
  - *Add virtual relationship…*, prefilled with the scope table and the best candidate;
  - *Use the key expression*;
  - *Import item designs…*;
  - *Use other table* for alternatives.
- **AC3 as built:** the coverage sheet is generated in the browser from the review (`coverageCSV`). It's downloaded from the banner, with one row per item, its status, reason code and reasons, plus a summary row. The proposal's statuses aren't in the saved spec, so the server's mapping sheet can't produce them. The server sheet does show "not populated" now.
