# OC-143 — Embed from any foreign-key field, at any depth, in the Canvas and the Composer

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P0 | 3 | OC-141, OC-064, OC-082 | Implemented |

## User story
As a **data engineer** looking at the target document, I want every field that holds a foreign key, including one inside an embedded object (`items[].product.brandId`), to offer **Embed *brands* here…**, in the Canvas as well as the Composer, so that I can expand a reference into its object wherever I see it, step by step down the chain.

## Background (gap found 2026-10-06)
OC-064 added "Embed *X* here…" and "Collapse to id" to the **Composer** only, and declared "embedding through a chain of foreign keys in one action" out of scope ("do it twice"). Doing it twice works only when the second hop is in the driving table's limited graph (`foreignKeyOf` searches `edges(graph)`, see OC-141). The Canvas has no target-side action at all: the user must find the table in the Related tree.

## Scope
**In:**
- `foreignKeyOf` finds the hop from the **field's scope table's own hops** (OC-141), so any foreign-key field at any depth qualifies: root, embedded object, array item, grouped rows (OC-063), lookup-fed fields excluded as today.
- The **Canvas** gets the same actions as the Composer in the Inspector of the selected target field: **Embed *table* here…** (replace / keep alongside, columns, name; OC-082 defaults) and **Collapse to id** on a many-to-one object, then a scoped auto-map of the new object.
- After an embed, the new object's own foreign-key fields offer the action in turn, so `productId → product → brandId → brand → supplierId → supplier` takes three clicks.
- The source side (OC-142) and the target side stay in step: the new relation shows ✓ under its instance.
- At the depth limit, the action is shown disabled with the same tooltip as OC-142 AC6.
- Composite foreign keys (two or more columns) qualify when all their columns are mapped in the same object; the dialog lists them.

**Out:** expanding a whole chain in one action (still one hop per action; OC-142 shows what comes next).

## Implementation details
- Replace `edges(graph)` in `bindPrefill.foreignKeyOf` with `hopsFor(scopeTable)` (OC-141); support composite joins by matching the set of mapped columns against the hop's `join` pairs.
- Canvas: `mapping/KeyActions.tsx` reuses `EmbedKeyDialog` and the Composer's `expandForeignKey` / `collapseToId`, so Canvas and Composer produce the same spec for the same choice by construction.
- `collapseToId` exists (OC-064); expose it in the Canvas menu.

## Acceptance criteria & unit tests

### AC1 — Any depth
**Given** `items[].product.brandId` mapped from `products.brand_id`, **then** its menu offers **Embed brands here…**; after embedding, `items[].product.brand.supplierId` offers **Embed suppliers here…**, even when neither hop is in the driving table's graph.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-143 AC1: a foreign key at any depth, composite keys when every column is mapped` | `web/src/features/composer/composerOps.test.ts` | A key two relations deep, found from the scope table's hops |

### AC2 — Canvas parity
**Given** the same mapping in the Canvas, **when** I use **Embed brands here…** with replace, **then** the spec equals the one the Composer produces for the same choice (same relation, fields and mappings, `brandId` removed); **Collapse to id** restores `brandId`.

| Test | Location | Verifies |
|---|---|---|
| `Inspector.test.tsx` › `OC-143 AC2: the Canvas embeds a foreign-key field and collapses it back` | `web/src/features/mapping/Inspector.test.tsx` | Replace, scoped auto-map, collapse |

### AC3 — Composite keys and limits
**Given** `order_items(product_id, variant_id) → variants(product_id, variant_id)` with both columns mapped in the item, **then** **Embed variants here…** is offered and joins on both. **Given** only one of them mapped, **then** it isn't. **Given** the field sits at the maximum depth, **then** the action is disabled with the depth tooltip.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-143 AC1: …composite keys when every column is mapped` | `web/src/features/composer/composerOps.test.ts` | Composite |
| `Inspector.test.tsx` › `OC-143 AC3: at the depth limit the action shows, disabled, with why` | `web/src/features/mapping/Inspector.test.tsx` | Depth |
