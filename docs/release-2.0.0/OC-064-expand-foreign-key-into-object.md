# OC-064 — Expand a foreign key into an embedded object (and back)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 3 | OC-043, OC-047, OC-063 | Implemented |

## User story
As a **data engineer** whose document has `productId` in each array item, I want to turn that id into the embedded `product` object it refers to (or keep both) with one action, and turn it back into an id, so that the customer can choose a referenced or an embedded document without me rebuilding the array.

## Background (gap found 2026-09-30)
Embedding works today: add an object under `products[]`, then bind it many-to-one to `products` through `product_id` (OC-043/047). It takes several steps, though, and nothing connects the mapped `productId` field to that relation. Switching between "reference by id" and "embed the object" means deleting and rebuilding fields by hand.

## Scope
**In:**
- On a field mapped from a foreign-key column (FK or virtual relationship, OC-042), whether at the root, in an array item or in the group's rows (OC-063): the node menu and inspector action **Embed *products* here…**. The dialog offers:
  - **Replace** `productId` with an object `product` (name editable, default the singular of the table name), or **add alongside**, keeping `productId`.
  - The object's fields: generated from the table's columns (choose which), or from an imported design's existing object (OC-059) when one is already there.
  - The kind and join are pre-filled from the FK (many-to-one `product_id = product_id`), followed by a scoped auto-map (OC-049).
- On a bound many-to-one object: **Collapse to id**, which replaces the object with a scalar field mapped from the join's parent column (`productId` ← `product_id`), dropping the relation and its mappings (with undo).
- Suggestions (OC-060): an imported design with `product{…}` next to a mapped `product_id` already gets the many-to-one proposal. When the design has `productId` and the customer also wants the object, the field's inspector shows *Embed products here…*.
- Works for the Couchbase and directory targets alike.

**Out:**
- Embedding through a chain of foreign keys in one action (do it twice).
- Replacing a reference with a lookup of a single column; lookups already do that (OC-047).

## Implementation details
- `composerOps.expandForeignKey(spec, fieldId, {table, join, mode: replace|alongside, name, columns})` builds on `bind` and `addNode`. In `replace` mode the field's mapping is removed and the object keeps the field's position. `collapseToId(spec, nodeId)` reverses it.
- The candidate FK comes from the relationship graph (`useRelationGraph`) for the field's scope table and its mapped column. The action is hidden when the column isn't on a relationship.
- The server needs no change: the result is an ordinary composed spec.

## Acceptance criteria & unit tests

### AC1 — Replace the id with the object
**Given** `products[]` bound to the group's rows (OC-063) with `productId` ← `product_id`, **when** *Embed products here* runs in replace mode with `sku` and `name`, **then** the item holds `product{sku, name}` (many-to-one under the array's relation, auto-mapped) where `productId` was, and the preview shows each line with its product.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-064 AC1: expand foreign key, replace` | `src/features/composer/composerOps.test.ts` | |
| `Composer.test.tsx` › `OC-064 AC1: embed products here` | `src/features/composer/Composer.test.tsx` | |

### AC2 — Keep both
**Given** the same, in *add alongside* mode, **then** `productId` stays mapped and `product{…}` follows it.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-064 AC2: alongside` | same | |

### AC3 — Collapse back to the id
**Given** the embedded `product`, **when** *Collapse to id* runs, **then** `productId` ← `product_id` replaces it, the relation and its mappings are gone, and undo restores the object.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-064 AC3: collapse to id` | same | |

### AC4 — Only offered on relationships
**Given** a field mapped from a column that isn't on any foreign key or virtual relationship, **then** the action isn't shown. **Given** a virtual relationship on that column, **then** it is.

| Test | Location | Verifies |
|---|---|---|
| `Inspector.test.tsx` › `OC-064 AC4: offered on relationships only` | `src/features/composer/Inspector.test.tsx` | |

## Implementation notes (2026-10-01)
- `composerOps.expandForeignKey(spec, fieldId, fk, {mode, name, columns})` builds on `deleteNode`/`addNode`/`bind` (with `generate`), and `collapseToId(spec, nodeId, {name?, type?})` reverses it. The id is mapped from the join's parent column in the object's scope, e.g. `{relation: <group rows>, column: product_id}`. `bindPrefill.foreignKeyOf(graph, spec, fieldId, own)` finds the many-to-one hop (FK or virtual) on the field's single mapped column of its own scope table. Constants, expressions, lookups and junction columns don't qualify.
- UI: node menu *Embed {table} here…* and *Collapse to id*, plus an inspector button. `EmbedKeyDialog` offers replace / alongside, the object's name (singular of the table) and its columns (all but the key by default), then runs a scoped auto-map of the new object.
- No server change. Embedding under the group's rows (OC-063) is covered by `TestGroup_AC1_OrderDetail` (product through `product_id`) and `web/tests/e2e/composer-group.spec.ts`.
- After embedding into an imported design, the fields the customer's design doesn't have show as `REFERENCE_EXTRA_FIELD` warnings and in the Reference's conformance (OC-061). That's expected: the document now differs from the imported shape.
