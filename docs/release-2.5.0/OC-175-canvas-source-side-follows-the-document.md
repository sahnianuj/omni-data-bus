# OC-175 — Canvas: the source side follows the document, object by object

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 Mapping Canvas | P1 | 3 | OC-021, OC-043, OC-163 | Implemented |

## User story
As a **data engineer** reading a mapping with embedded objects in the Canvas, I want the source columns listed in the order of the document they fill (the root's columns, then each embedded object's table, its own nested objects right after it), so that each object's columns sit across from its fields and the lines run straight instead of crossing the whole canvas.

## Background (gap found 2026-10-17)
The mappings are correct, but the source pane is hard to read:
- **Groups follow creation order.** The source pane lists the driving table's columns, then one group per relation in `spec.relations` order: the order relations were *created*, not where they sit in the document. A nested relation added later appears after unrelated groups, and so do a lookup or a total's summary relation.

  On the design-first cart (OC-163) the groups come out as `saved_orders` (a lookup for two root fields), `customers`, `partners`, `cart_items`, `customizations`, `applied_rewards`, then `collected_payments` (behind the `pricing.tipInCents` total) last.
- **Columns follow the table, not the document.** Within each group, columns are in the table's column order. The target pane follows the design (`targetRows`), so the driving table's columns fill fields in `pricing`, at the root and elsewhere in no visible order.

The result is lines crossing back and forth, and an object's columns spread across the pane.

## Scope
**In:**
- **Groups in document order** (the default, *Order: Document*). Groups are placed depth-first in the order of the fields they fill:
  - each relation's group comes where its target field is in the design (`customer`, `partner`, `items[]`);
  - its nested relations (`items[].modifiers[]`, `items[].appliedRewards[]`) follow right after it, before the next object;
  - a **lookup** group comes right after the group of the scope it serves, at the position of the first field that reads it (`saved_orders` next to `savedAt`, among the root's columns);
  - a **total's** summary relation comes at its field (`collected_payments` within `pricing`);
  - a variant array's variants (OC-171) are listed under it in the design's order.
- **Columns in document order inside each group.** Mapped columns come first, in the order of the fields they fill (the first one when a column fills several). Unmapped and ignored columns follow, in table order, under a thin *Not mapped* divider.
- **A toggle in the pane header:** *Order: Document · Table*. *Table* keeps today's layout (table column order, groups in creation order). The choice is remembered per user, like the pane sizes.
- **The same order in the Table view's source column grouping** and in the Composer's Sources pane "In this document" section (OC-142), which already nests instances like the document.

**Out:** reordering the target design (the customer's order is the reference); changing the order relations are stored in the spec, which has no effect on documents.

## Implementation details
- `mapping/sourceOrder.ts`: a pure `orderSources(spec, columns, mode)` → `SourceRow[]`, used by `MappingCanvas` in place of the `srcRows` memo.
  - It walks the design (`targetRows` order, ignoring collapse) and assigns each field's sources to its scope's group.
  - Groups are emitted depth-first by the position of their first field. A group with no mapped field keeps its creation order, at the end of its parent's groups.
- **Stable:** ties keep table order, so `orderSources` is deterministic, and the edge layout doesn't jump while mapping.
- **Toggle preference:** `viewPrefs` (`mapping.sourceOrder`), next to `usePaneSize`.

## Acceptance criteria & unit tests

### AC1 — Groups follow the document
**Given** the design-first cart proposal (`web/testdata/design-first/cart-proposal.json`), **then** the source groups read:
1. `carts`, with `saved_orders` first among them: the root's `status` (its 2nd field) is computed from `saved_orders`;
2. `customers`, `partners`, `cart_items`, `customizations`, `applied_rewards`;
3. `collected_payments` at `pricing.tipInCents`.

**Given** a nested relation created after an unrelated one, **then** it still follows its parent.

| Test | Location | Verifies |
|---|---|---|
| `sourceOrder.test.ts` › `OC-175 AC1` | `web/src/features/mapping/sourceOrder.test.ts` | Group order; table mode = creation order; a moved nested relation |

### AC2 — Columns follow their fields
**Given** `carts`, **then** its mapped columns are in the order of the root's fields and then `pricing`'s (`channel`, `skip_fulfillment`, …, `total_in_cents`, `subtotal_in_cents`, …), followed by *Not mapped* (`receipt_number`, `tax_data`, …). Lines between the panes don't cross within a group (each column's first field index is non-decreasing).

| Test | Location | Verifies |
|---|---|---|
| `sourceOrder.test.ts` › `OC-175 AC2` | `web/src/features/mapping/sourceOrder.test.ts` | Monotonic target index per group |

### AC3 — Table order on request
**When** the user picks *Order: Table*, **then** the pane shows today's order, and the choice survives a reload.

| Test | Location | Verifies |
|---|---|---|
| `MappingCanvas.test.tsx` › `OC-175 AC3` | `web/src/features/mapping/MappingCanvas.test.tsx` | Divider in Document order; *Table* survives a remount |

## Implementation notes (2026-10-17)
- `mapping/sourceOrder.ts`: `orderSources(spec, tableColumns, columns, shown, keepEmpty, mode)`, which `MappingCanvas` uses in place of its `srcRows` memo. `designOrder(spec)` gives each field's depth-first position.
- **A column's position:** the first field it fills. That covers sources of active or pending mappings, columns an expression reads (`row.x` in the field's scope, `table.x` of a relation), and totals.
- **A relation's position:** its own field; for a lookup or a total's relation, the first field that reads it.
- **Within each scope:** mapped columns and child groups are merged by position, with ties broken by table order, then creation order. The unmapped columns follow a *Not mapped* divider (`divider-<scope>`).
- **The toggle:** *Order: Document · Table* in the pane header, kept in `localStorage` (`odb.canvas.sourceOrder`); the Canvas pane sizes don't use `viewPrefs`.
- **Not changed:**
  - the Table view has no source grouping to reorder;
  - the Composer's "In this document" (OC-142) already follows the document.
