# OC-066 — Combine rows that share a document key

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 5 | OC-063, OC-065 | Implemented |

## User story
As a **data engineer** who sets the key of a detail table's mapping to `order::{order_id}`, I want the tool to recognise that rows will collide on that key and offer to combine each set of colliding rows into one document, its rows as array entries, so that a normalized table becomes a 1:N document without my knowing about grouping.

## Background (gap found 2026-10-01)
Today a key that doesn't cover the primary key gives `KEY_MAY_COLLIDE` (a warning). At migration time every row writes the same key, so the last row wins and the others are lost. Grouping (OC-063) is the right model, but it's reached from *One document per*, not from the key collision the customer thinks in. Merging into the existing document at write time (append on collision) is **not** the answer: it's order-dependent, not idempotent on re-runs or resume, and needs a read per write.

## Scope
**In:**
- `KEY_MAY_COLLIDE` on a mapping whose key columns are a **proper subset of the primary key** (or of a unique key) gets a fix: **Combine rows with the same key into one document**. It opens a dialog that:
  - groups by the key's columns (`order_id`);
  - names the array that holds the rows (default: the table name in camelCase plural, editable, e.g. `orderDetails`, or an existing unbound array of the design such as `products`);
  - moves every field mapped from a non-key column into the array's item, keeping its mapping re-pointed to the group's rows (OC-065). Fields mapped from key columns stay at the root;
  - orders the rows by the rest of the primary key.
- Works from the Canvas too: the mapping becomes composed (with confirmation) and opens in the Composer.
- After combining, the preview shows one document per key and the collision warning is gone.

**Out:** write-time merging into existing documents (see Background), and combining on a key that isn't a subset of a primary or unique key.

## Implementation details
- `mapping.KeyAnalysis` exposes `GroupCandidate []string` (the key's columns) when they're a proper subset of the primary key or a not-null unique key. The issue carries `Fix{Kind: "combine-rows", Columns: [...]}`.
- `composerOps.combineRows(spec, {groupBy, arrayName, targetArrayId?, primaryKey})`: `setGroupBy`, then create or reuse the array, then `bindGroupRows`, then move root fields mapped from non-group columns into the item (keeping IDs and mappings, re-pointed).
- UI: the fix button in the Validation panel (canvas) and Problems panel (composer) opens `CombineRowsDialog`.

## Acceptance criteria & unit tests

### AC1 — From the key collision to one document per order
**Given** the flat order_detail mapping `{type, orderId, lineNo, productId, quantity}` with key `order::{order_id}`, **then** `KEY_MAY_COLLIDE` offers *Combine rows*. **When** applied with array `products`, **then** the design is `{type, orderId, products[]{lineNo, productId, quantity}}`, grouped by `order_id`, ordered by `line_no`, and the preview shows order 1 with its 2 lines once each.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-066 AC1: combine rows` | `src/features/composer/composerOps.test.ts` | |
| `TestKey_AC1_GroupCandidate` | `internal/mapping/keytemplate_test.go` | |

### AC2 — From the Canvas
**Given** the same mapping in the Canvas, **when** the fix is applied, **then** it becomes composed and opens in the Composer with the combined design.

| Test | Location | Verifies |
|---|---|---|
| `ValidationPanel.test.tsx` › `OC-066 AC2: combine from canvas` | `src/features/mapping/ValidationPanel.test.tsx` | |

### AC3 — Not offered when it can't help
**Given** a key `{$uuid}`, or a key on a non-key column (`status`), **then** no *Combine rows* fix is offered.

| Test | Location | Verifies |
|---|---|---|
| `TestKey_AC3_NoGroupCandidate` | `internal/mapping/keytemplate_test.go` | |

### AC4 — End to end
**Given** sales.order_detail, **when** the key is set to `order::{order_id}` and the fix applied, **then** the migration writes 500 documents (one per order) with every line once.

| Test | Location | Verifies |
|---|---|---|
| `composer-group.spec.ts` › `combine rows from the key` | `web/tests/e2e/composer-group.spec.ts` | |

## Implementation notes (2026-10-01)
- `mapping.AnalyzeKey` sets `GroupCandidate` when the key's columns are a proper part of the primary key or of a not-null unique key. `KEY_MAY_COLLIDE` then says the rows can be combined and carries `Fix{Kind: "combine-rows", Columns}` (not when already grouped). `{$uuid}` and non-key columns get no fix.
- `composerOps.combineRows`: `setGroupBy`, then a new array (default `pluralName(table)`; already-plural names are kept, e.g. `order_items` → `orderItems`) or an existing unbound root array, then `bindGroupRows` ordered by the rest of the primary key, then the chosen root fields are moved into the item (IDs and mappings kept, re-pointed to the rows).
- **Which fields move is the user's choice** in `CombineRowsDialog`: every root field mapped from a non-key column is offered, ticked by default except discriminator-like names (`type`, `docType`, `kind`, `version`), which stay on the document (first row). The customer's target keeps `type` at the root.
- From the Canvas (Issues tab) it converts to composed and opens the Composer. From the Composer, from the Problems panel.
- E2E: `composer-group.spec.ts` › `combine rows from the key` (key set to `order::{order_id}` through the API, combined from the Issues tab, order 1's two lines once each). The 500-document migration of AC4 is covered by the preview and by OC-063's migration test, not run again here.
