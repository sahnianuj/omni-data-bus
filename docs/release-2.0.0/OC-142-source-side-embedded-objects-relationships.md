# OC-142 — The Composer's source side lists each embedded object's own relationships

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P0 | 5 | OC-141, OC-043, OC-047, OC-048, OC-157 | Implemented |

> **Trimmed (2026-10-16):** the Canvas side is done: Related tables lists the hops under every embedded row (OC-152), grouped as References and Referenced by with their cardinality (OC-157). This story is the Composer's Sources pane. Hierarchies and the chained dev seed moved to OC-161 (release 3.0).

## User story
As a **data engineer** composing a document, I want the Sources pane to show, under each object I've already embedded, that object's own foreign keys and child tables, ready to embed into it or to pull single values from, so that I can keep enriching the document one level at a time without hunting through every table or knowing the join columns.

## Background (gap found 2026-10-06)
**Sources** (`SourcesPane`, OC-048) lists the driving table's relationship tree flat, from one graph request limited to 4 hops and 200 paths (OC-141), then every other table. Selecting an embedded `product` doesn't list *its* foreign keys as things to bind: the user has to find `brands` among all the tables, drag it onto a new object under `product`, and hope the join is reachable.

## Scope
**In:**
- **An "In this document" section** at the top of Sources: one row per relation the mapping already has (a *relation instance*), nested like the document, labelled with its table and target path (`order_items → items[]`, `products → items[].product`). The driving table is the root. A lookup-only relation shows "lookup".
- **Under each instance, its relationships that aren't embedded yet**, from OC-141's hops of that instance's table, split into **References** and **Referenced by** with the cardinality chips of OC-157 (`N:1`, `1:1`, `1:N`, `M:N`, junctions under Referenced by at the root only). Virtual relationships keep their badge. Embedded hops aren't repeated: they are the instance rows below.
- **Actions on each available hop** (one row each, buttons on hover and focus):
  - **Embed**: adds a node under the instance's object, named after the table (`brand`, `reviews`), bound to the hop with the join pre-filled, and auto-maps its columns: an object for N:1 and 1:1, an array for 1:N. One undo step.
  - **Look up** (N:1 and 1:1): binds the hop as a lookup-only relation in the instance's scope, so its columns can be dragged onto fields of the instance's object (`product.brandName` ← `brands.name`) without a nested object.
  - Dragging a hop onto a node binds it there, as dragging a table does, with the join pre-filled.
- **Depth limit:** at `OC_MAX_RELATION_DEPTH` (default 4), an instance's hops are listed but their actions are disabled, with the tooltip "Deepest level reached (4 of 4). An administrator can raise OC_MAX_RELATION_DEPTH up to 6."
- The rest of the pane (the driving table's tree and "Other tables") stays below, unchanged.

**Out:** the Canvas (done, OC-152/OC-157); hierarchies (OC-161); editing an existing relation's join from this tree (the Inspector does that).

## Implementation details
- `web/src/features/composer/documentSources.ts`: a pure `documentSources(spec, driving)` → instances `{ relationId ('' = root), table, path, containerId, depth, lookup, children }`, from `spec.relations` (parent links; a grouped document's relations nest under the root) and `docSchema` paths. `availableHops(spec, inst, hopsOf, junctionsOf)` = the table's hops (OC-141) minus those already embedded or looked up under the instance (same table and join) and the hop back to the relation that embedded it; junctions at the root only.
- `InThisDocument.tsx` renders the section at the top of `SourcesPane`; each instance's hops come from `useTablesHops` (one cached request per table).
- Embed reuses the Bind dialog (OC-048) rather than a separate op: `Composer.embedHop` adds the node under the instance's container (an object named in the singular for N:1, else an array) and opens the dialog on it. The dialog's prefill takes the scope table's own hops (`prefill(…, scopeHops)`), so the join is pre-selected at any depth. On Bind, the node, the relation and the scoped auto-map are folded into one undo step (`history.squash`, store `squash(mark)`); Cancel drops the node (`revert(mark)`).
- Look up opens the dialog in lookup mode on the instance's container; `composerOps.lookupScope` is the relation the lookup hangs under (a bound node's own relation), and the dialog joins from that relation's table.

## Acceptance criteria & unit tests

### AC1 — An embedded object's relationships appear under it
**Given** `orders` with `items[]` (order_items) and `items[].product` (products), **then** "In this document" shows `products → items[].product` under `order_items → items[]`, and under it References ↗ (the product's foreign keys) and Referenced by ↙ (tables pointing at products), excluding the hop back to order_items that is already embedded.

| Test | Location | Verifies |
|---|---|---|
| `documentSources.test.ts` › `OC-142 AC1: instances nested like the document, and their available hops`; `the same table embedded twice is two instances` | `web/src/features/composer/documentSources.test.ts` | Pure model |
| `SourcesPane.test.tsx` › `OC-142 AC1` | `web/src/features/composer/SourcesPane.test.tsx` | Rendering, groups and chips |

### AC2 — Embed and look up under the embedded object
**When** I click **Embed** on ↗ brands under the product, **then** a `brand` object is added under `items[].product`, bound many-to-one with `parentId` = the product relation and the join from the hop, and its columns are auto-mapped; one undo removes it all. **When** I click **Look up** instead, **then** a lookup-only relation is added in the product's scope and no node is created.

| Test | Location | Verifies |
|---|---|---|
| `SourcesPane.test.tsx` › `OC-142 AC2` | `web/src/features/composer/SourcesPane.test.tsx` | Embed → dialog pre-filled with the hop's join → `brand` under `items[].product` with `parentId` = the product relation; one undo removes it; cancel leaves nothing; Look up adds a lookup-only relation and no node |
| `history.test.ts` › `OC-142 AC2: squash folds steps into one undo; revert drops them` | `web/src/features/docschema/history.test.ts` | |

### AC3 — Depth limit
**Given** `maxRelationDepth` 4 and an instance at depth 4, **then** its hops are listed with disabled actions and the tooltip.

| Test | Location | Verifies |
|---|---|---|
| `SourcesPane.test.tsx` › `OC-142 AC3` | `web/src/features/composer/SourcesPane.test.tsx` | Embed and Look up disabled with the tooltip at depth 2 of 2; a shallower instance's Embed stays enabled |
