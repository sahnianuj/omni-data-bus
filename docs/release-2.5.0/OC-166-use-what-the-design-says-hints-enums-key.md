# OC-166 — Use what the design says: constants, enums, source hints, "not populated" and the document key

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P1 | 5 | OC-059, OC-163 | Implemented |

## User story
As a **data engineer**, I want the proposal to take into account what the customer wrote in their design (fixed values, allowed values, "sourced from table.column", "Not populated", the document ID pattern), so that it doesn't guess where the customer already told us, and doesn't count deliberately empty fields as gaps.

## Background (gap found 2026-10-17)
The customer's JSON Schemas carry a lot of intent:
- **Fixed values**: `"type": {"const": "cart"}`, `"paymentType": {"const": "SCC"}`, `"status": {"const": "completed"}`.
- **Allowed values**: `"status": {"enum": ["open", …, "saved"]}`, `"orderCollectionType": {"enum": ["IN_STORE", …, null]}`.
- **Source hints in descriptions**: "sourced from saved_orders.saved_at", "tipInCents is the sum of payments[].tipInCents".
- **Intentionally empty fields**: around 20 of them say "Not populated" (`userID`, `receipt`, `associatedScan`, `pricing.label`, …).
- **The key**: the root's description says "Document ID: cart::{uuid}".

2.0 keeps descriptions and turns `const` into constant hints (OC-059). It ignores `enum`, with a warning. It never reads descriptions for evidence. Every "Not populated" field counts as unmapped, so the score never reaches 100 % and the Problems panel fills with false gaps.

## Scope
**In:**
- **`enum`** is imported as `DocField.enum`. It's checked in the preview and in conformance (`REFERENCE_ENUM_VALUE`, a warning; an error in strict mode) and exported back to JSON Schema.
  - When the source column is a PostgreSQL enum (discovery `EnumValues`), the proposal compares the two. Design values missing from the column (`saved`) are listed in the reason ("design allows 'saved', not in cart_status") and point to OC-165's derived status.
- **Description hints**, parsed by `docschema.Hints(description)`:
  - `notPopulated`: "Not populated", "not tracked", "always empty", "no column", case-insensitive and at the start of a sentence;
  - `sources[]`: `table.column` tokens that name a table and column in the source snapshot;
  - `keyTemplate`: "Document ID: <prefix>::{…}" on the root.
- **In the proposal:**
  - `const` becomes a constant mapping (applied, not just hinted);
  - `notPopulated` becomes a new mapping state, `notPopulated`. It's written as nothing, counted as done in the score and the Problems panel, and shown dimmed in the tree with "Not populated (design)";
  - `sources[]` is the strongest evidence for a field: +0.3 to its score, and it can pull in a lookup (OC-165) or an aggregate (OC-167);
  - `keyTemplate` becomes the mapping's key, `cart::{id}`. The placeholder is matched to the driving table's primary key.
- Users can override any of these. Turning a not-populated field into a mapped one is an ordinary mapping edit.

**Out:** natural-language understanding beyond these patterns; changing the customer's design text.

## Implementation details
- `internal/docschema/hints.go`: a small, table-driven parser with no regex backtracking over the whole description. It returns `Hints{NotPopulated bool; Sources []string; KeyTemplate string}` and stores them on `DocField.hints` at import. Descriptions over 2 KB are truncated before parsing.
- `jsonschema.go`: `enum` moves to `supportedKeywords`, and `null` in an enum maps to `nullable`.
- `mapping.FieldMapping.State` gains `notPopulated`. The plan skips it like an unmapped field, and stats count it as mapped (`computeStats` and the Go `Stats` alike).

## Acceptance criteria & unit tests

### AC1 — Hints parsed from the cart design
**Then**:
- `savedAt.hints.sources = ["saved_orders.saved_at"]`;
- `receipt.hints.notPopulated = true`;
- the root's `keyTemplate = "cart::{uuid}"`;
- `status.enum` has 7 values.

| Test | Location | Verifies |
|---|---|---|
| `TestHints_AC1_CartDesign` | `backend/internal/docschema/hints_test.go` | |
| `TestJSONSchema_AC1_Enum` | `backend/internal/docschema/jsonschema_test.go` | Round-trip through export |

### AC2 — Proposal follows the hints
**Then**:
- `type = "cart"` is a constant;
- every "Not populated" field has state `notPopulated`;
- the key is `cart::{id}`;
- `savedAt` names `saved_orders.saved_at` in its reasons.

Coverage counts not-populated fields as done.

| Test | Location | Verifies |
|---|---|---|
| `TestPropose_OC166_AC2_Hints` | `backend/internal/automap/propose_test.go` | |
| `computeStats.test.ts` › `OC-166 AC2: notPopulated counts as mapped` | `web/src/features/mapping/computeStats.test.ts` | Same as Go stats |

### AC3 — Enum values checked
**Given** a preview document whose `consumptionType` is `"Other"`, **then** `REFERENCE_ENUM_VALUE` is reported for it, as an error when strict.

| Test | Location | Verifies |
|---|---|---|
| `TestConform_OC166_AC3_Enum` | `backend/internal/docschema/conform_test.go` | |

## Implementation notes (2026-10-17)
- **Hints aren't stored.** `docschema.ParseHints(description)` reads them on demand, so designs imported before 2.5 get them too, and no client has to carry a new spec field. It returns `notPopulated`, `sources`, `aggregate`, `always` and `keyTemplate`.
- **`enum` is stored** (`DocField.enum`, `ContractField.enum`). Imported `null` values make the field nullable, and *Export JSON Schema* writes the enum back.
- **Conformance:** `Conformance.enumValues` lists the paths whose value isn't allowed (`REFERENCE_ENUM_VALUE`), per preview document.
- **`notPopulated` state:**
  - it counts as done in Go and web stats (shared `stats-cases.json` case);
  - validation accepts it, and the mapping sheet shows "not populated";
  - nothing is written for it, and an object whose every field is not populated is written as `null`, not as an object of nulls;
  - the Inspector says so and offers *Map it instead*.
- **In the proposal:**
  - `const` becomes a constant mapping (all fields, array items included);
  - the root's "Document ID: cart::{uuid}" becomes the key `cart::{id}`;
  - a named `table.column` scores ≥ 0.95.
