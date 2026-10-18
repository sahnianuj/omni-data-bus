# OC-167 — Type-aware field matches: conversions, combined columns and totals

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P1 | 5 | OC-163, OC-020, OC-045 | Implemented |

## User story
As a **data engineer**, I want a proposed field to come with the conversion its types need (dollars to cents, a number to its display string, two columns combined, a sum over child rows), so that the proposal produces the values the design expects, not just the right column.

## Background (gap found 2026-10-17)
The OC-017 scorer matches names, and it uses types only as a tie-break. On the cart design that leaves several fields producing the wrong value or nothing:
- `items[].price`, `basePrice` and `modifiers[].price` are `integer` (cents), but the columns are `numeric(8,2)` (dollars). The examples (`495`) confirm cents.
- `pricing.totalToDisplay` is a `string` (`"3.95"`), from `carts.total_to_display numeric(8,2)`.
- `partner.name` is one string; `partners` has `first_name` and `last_name`.
- `modifiers[].label`, `value` and `valueString` all come from `customizations.name`. The scorer assigns each column once, so two of them stay unmapped.
- `pricing.tipInCents` is "the sum of payments[].tipInCents": `sum(collected_payments.tip_in_cents)`.
- `customer.eligibleBenefits` is `array<string>` from `text[]`, and `items[].id` is a `string` from `uuid`.
- `items[].quantity` "always 1 — no quantity column".

## Scope
**In:**
- **Conversion rules**, ordered and data-driven (`automap/conversions.go`). Each one produces an ordinary OC-020 transform chain:

  | Source → design | Condition | Transform |
  |---|---|---|
  | numeric/decimal → integer | name ends in `InCents`/`Cents`, or the examples are integers ≥ 100 and the column has scale 2 | `math ×100` → `round 0` |
  | numeric → string | — | `toString` with the column's scale (`3.95`) |
  | uuid, enum → string | — | none (already strings in JSON) |
  | `text[]`/`int[]` → array of string/integer | — | none |
  | timestamptz → string `date-time` | — | `formatDatetime iso8601` |
  | integer → boolean, text → integer… | — | flagged as a type mismatch (`REFERENCE_TYPE_MISMATCH`), not converted |

- **One column, several fields.** After the assignment, an unmapped field whose best column is already used, scoring ≥ 0.8 on name or on the field's description, gets the same column (`label`, `value`, `valueString` ← `name`).
- **Combined columns.** A string field with no single match ≥ 0.6, whose name matches the shared stem of two columns (`name` ← `first_name`, `last_name`; `fullName`, `displayName`), gets `concat(first_name, ' ', last_name)`, at medium confidence.
- **Totals.** A numeric field whose hint (OC-166) names a child relation's column with "sum / count / total / number of" gets an OC-045 aggregate on that relation. The relation is created lookup-only when the design has no array for it.
- **Constant from the description.** "Always 1" or "always <literal>" becomes a constant (`quantity = 1`), at medium confidence.

**Out:** unit conversions other than dollars to cents; currency conversion; anything that needs JavaScript (OC-137).

## Implementation details
- The scorer's type component becomes `typeFit(column, field)`: 1.0 for the same JSON type, 0.8 when a conversion rule applies, 0.0 when the types can't convert. The field score is `0.6·name + 0.25·typeFit + 0.15·exampleFit`. `exampleFit` compares the design's example with the column's type and range: an integer example against a numeric(8,2) column suggests cents.
- Proposed transforms are listed in the review's reasons ("×100: dollars → cents (examples are cents)").

## Acceptance criteria & unit tests

### AC1 — Cents and display strings
**Then**:
- `items[].price ← cart_items.price` with `math ×100, round 0`;
- `pricing.totalToDisplay ← carts.total_to_display` with `toString`.

On the seeded rows, the preview gives `495` and `"3.95"`.

| Test | Location | Verifies |
|---|---|---|
| `TestConversions_AC1_CentsAndDisplay` | `backend/internal/automap/conversions_test.go` | |
| `TestPropose_OC167_AC1_Preview` *(integration)* | `backend/internal/automap/propose_integration_test.go` | |

### AC2 — Shared column and combined name
**Then**:
- `modifiers[].label`, `value` and `valueString` all read `customizations.name`;
- `partner.name = concat(first_name, ' ', last_name)` at medium confidence.

| Test | Location | Verifies |
|---|---|---|
| `TestPropose_OC167_AC2_SharedAndConcat` | `backend/internal/automap/propose_test.go` | |

### AC3 — Totals and constants from the description
**Then**:
- `pricing.tipInCents` is `sum(collected_payments.tip_in_cents)` through a lookup-only 1:N relation (an aggregate, not an embed);
- `items[].quantity = 1` is a constant.

| Test | Location | Verifies |
|---|---|---|
| `TestPropose_OC167_AC3_AggregateAndConstant` | `backend/internal/automap/propose_test.go` | |

### AC4 — No silent wrong conversions
**Given** an `integer` design field matched by name to a `text` column, **then** no transform is proposed, the field is at most medium confidence, and the reason says "text → integer: no safe conversion".

| Test | Location | Verifies |
|---|---|---|
| `TestConversions_AC4_Mismatch` | `backend/internal/automap/conversions_test.go` | |

## Implementation notes (2026-10-17)
- **Cents:** there's no `math` op, so cents are `expression: value == nil ? nil : value * 100`, then `round {digits: 0}`, for numeric columns with scale 2 going into integer fields. Nulls stay null: real rows (`price_override`) caught this.
- **Display strings:** numeric → string is `toString`.
- **AC4 as built:** lossy conversions (text → integer, …) keep the type-matrix step, but their type score is 0.5, so they're at most medium, with the reason "may lose data". Incompatible types are never proposed. 2.0 put `parseInteger` on dollar amounts at high confidence.
- **Several fields from one column:** "value/valueString mirror label" in a container's description, plus "customizations has a single name column", maps all three fields to that column. A taken column that still scores ≥ 0.8 is shared too.
- **Combined names:** a `name`, `fullName` or `displayName` field with `first_name` and `last_name` columns becomes `concat(row.first_name, ' ', row.last_name)` (medium).
- **Totals:** "sum / count / total / number of" plus `table.column` becomes an aggregate. It's built on a summary-only 1:N relation (no target field), or on one already reading those rows.
- **Constants from the description:** "Always 1 …" is a constant (medium).
