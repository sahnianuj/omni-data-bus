# OC-165 — Fields from a one-to-one table, folded onto the parent

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P0 | 5 | OC-164, OC-047 | Implemented |

## User story
As a **data engineer**, I want fields that the customer's design puts directly on the document, but that live in a one-to-one table next to the driving table, to be filled from that table without a nested object, so that the document keeps the customer's flat shape.

## Background (gap found 2026-10-17)
The cart design puts `savedAt` and `orderCollectionType` at the top level. Their descriptions say "sourced from saved_orders.saved_at" and "…order_collection_type". `saved_orders` is one-to-one with `carts` (its primary key is `cart_id`).

In 2.0 a field can only read the columns of its scope, plus lookup-only relations. Lookups are **many-to-one only**: the Bind dialog filters them to `many-to-one`. So there are two ways to get these values today, and both are wrong:
- an embedded `savedOrder{}` object, which isn't the design;
- leaving the fields unmapped.

## Scope
**In:**
- **One-to-one lookups.** A lookup-only relation may be 1:1 in either direction:
  - References: as today;
  - Referenced by: the child's foreign key is its primary key or a unique key, so there's at most one row.

  The plan reads it like a many-to-one lookup, keyed by the parent's key, and the runner needs nothing new. A Referenced-by 1:N table isn't offered as a lookup, because it would be ambiguous.
- **The proposal uses them.** When it maps a scope's scalar fields, the columns in scope include the 1:1 lookups that are one hop from the scope's table. A field matched to such a column gets a lookup relation, created once per table and scope, and the mapping reads from it.
- **The UI offers them**:
  - the Bind dialog's *Look up values* lists 1:1 Referenced-by hops;
  - "In this document" (OC-142) shows *Look up* on 1:1 rows, not only N:1.
- **The derived status.** The design says `status` is `"saved"` when a `saved_orders` row exists, and the raw `status` column otherwise. The proposal suggests a composer expression (OC-047): `if(saved_orders.cart_id != null, 'saved', row.status)`, using the `if(cond, then, else)` helper this story adds to transform expressions. It's shown as medium confidence, with the description as the reason.

**Out:** lookups over 1:N, which need an aggregate or a filter (use an embed with `first`, OC-164); writing to more than one document.

## Implementation details
- `mapping.Relation` validation: `lookupOnly` with `kind: one-to-many` is valid when `join.child` columns are the child's primary key or a unique key in the discovery snapshot (`LOOKUP_NOT_UNIQUE` otherwise). The plan compiles it as a keyed read, like many-to-one, with `PerKeyLimit 1`.
- Expression helper `if(cond, then, else)` in `transform/expr.go` and the web autocomplete list (`scope.ts reserved`).
- `bindPrefill`: lookup options = direct `many-to-one` plus `one-to-many` hops marked `cardinality: one-to-one`.

## Acceptance criteria & unit tests

### AC1 — Flat fields from a one-to-one table
**Given** the cart design on the `pos` source, **then** the proposal maps `savedAt ← saved_orders.saved_at` and `orderCollectionType ← saved_orders.order_collection_type` through one lookup relation on `saved_orders` (1:1, Referenced by). No `savedOrder` node is created.

| Test | Location | Verifies |
|---|---|---|
| `TestPropose_OC165_AC1_OneToOneLookup` | `backend/internal/automap/propose_test.go` | |

### AC2 — Runs and previews like any lookup
**Given** that mapping, **then** the preview (OC-049) returns `savedAt` for a saved cart and `null` for the others. Server jobs and `odb run` give the same documents.

| Test | Location | Verifies |
|---|---|---|
| `TestPlan_OC165_AC2_OneToOneLookup` *(integration)* | `backend/internal/plan/compile_integration_test.go` | |
| `TestParity_AC3_GoldenMappings` *(integration)* | `backend/internal/headless/parity_integration_test.go` | New golden mapping `pos_cart` |

### AC3 — Not unique, not a lookup
**Given** `customers.cart_id` (not unique), **then** *Look up* isn't offered for it, and a spec with that lookup fails validation with `LOOKUP_NOT_UNIQUE`.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_OC165_AC3_LookupNotUnique` | `backend/internal/mapping/validate_test.go` | |
| `BindDialog.test.tsx` › `OC-165 AC3` | `web/src/features/composer/BindDialog.test.tsx` | |

### AC4 — Derived status suggestion
**Then** `status` is proposed as the expression `if(saved_orders.cart_id != null, 'saved', row.status)`, at medium confidence. In the preview, a saved cart shows `"saved"` and an open one `"open"`.

| Test | Location | Verifies |
|---|---|---|
| `TestPropose_OC165_AC4_DerivedStatus` | `backend/internal/automap/propose_test.go` | |
| `TestExpr_OC165_AC4_If` | `backend/internal/transform/expr_test.go` | |

## Implementation notes (2026-10-17)
- One-to-one hops are already `many-to-one` in both directions in the graph (OC-157). So *Look up*, the Bind dialog's lookup filter and plan compilation accepted them unchanged.
- **New:** `LOOKUP_NOT_UNIQUE`, for a lookup whose join isn't a key of a table that declares keys (`relvalidate.go`).
- **The proposal** offers, besides the scope's own columns:
  - one-to-one columns (weight 0.97);
  - many-to-one columns (weight 0.92), except the way back to the parent.

  One lookup relation is created per table and scope.
- **The derived status** uses expr-lang's ternary: `saved_orders.cart_id != nil ? 'saved' : row.status`. No `if` helper was added, because `if` is a reserved word in expr-lang.
- **AC2:** the documents come from the real source through the same plan and preview reads (`TestPropose_OC163_Documents`). The parity golden `pos_cart` is part of OC-173.
- **AC3, UI:** the Bind dialog only offers many-to-one lookups, so `customers.cart_id` (1:N) is never offered.
