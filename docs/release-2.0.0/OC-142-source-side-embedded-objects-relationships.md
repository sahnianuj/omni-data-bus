# OC-142 — The source side lists each embedded object's own relationships

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P0 | 8 | OC-141, OC-043, OC-047, OC-048 | Not started |

## User story
As a **data engineer** who has embedded `product` in each order line through `productId`, I want the source side to show `product`'s own foreign keys (its brand, its category) and child tables (its reviews) **under the embedded product**, ready to embed into that object or to pull single values from, so that I can keep enriching the document one level at a time without hunting for tables or knowing the join columns.

## Background (gap found 2026-10-06)
- **Canvas — Related tables** (`RelatedTree`, OC-042 AC6/OC-043 AC7) is a tree of *paths from the driving table*, cut by the graph's limits (OC-141). An embedded product's relationships appear only if a path to them survived the cut. Nothing tells the user which nodes are already in the document, and the same table embedded twice (billing and shipping address) shows once.
- **Composer — Sources** (`SourcesPane`, OC-048) lists the driving table's relationship tree flat, with "reachable" outlines computed from the same limited graph. Selecting the embedded `product` object doesn't list *its* foreign keys as things to bind; the user has to find `brands` among all the tables and know it's reachable.
- **Target side:** the Composer can turn an embedded object's foreign-key field into an object ("Embed brands here…", OC-064) when the hop is in the limited graph; that's covered by OC-143.

## Scope
**In:**
- **"In this document" section** at the top of the source side, in both the Canvas (Related tables) and the Composer (Sources): a tree of what the mapping already reads, one node per **relation instance**, labelled with its table and target path:
  ```
  orders                                   (driving)
  ├─ order_items → items[]                 ↙ 1:n
  │  └─ products → items[].product         ↗ n:1   ✓
  │     ├─ brands                          ↗ n:1   Embed · Look up
  │     ├─ categories                      ↗ n:1   Embed · Look up
  │     └─ reviews  ~12 per product        ↙ 1:n   Embed
  └─ customers → customer                  ↗ n:1   ✓
     └─ addresses                          ↗ n:1   Embed · Look up
  ```
  - Under every relation instance (and under the driving table), its **not-yet-embedded hops** from OC-141: ↗ many-to-one, ↙ one-to-many (with the rows-per-parent estimate), ↔ many-to-many via junction, virtual relationships badged, unindexed joins with the existing index advice icon.
  - Already-embedded hops show ✓ and the target path; clicking one selects that field on the target side.
  - Lookup-only relations (OC-047) show as ✓ with "lookup" and the fields they feed.
  - Lazily expanded: a node fetches its hops (OC-141) when opened; the path from the root to the selected target field is expanded automatically.
- **Actions on a hop under an embedded object** (keyboard-reachable buttons, shown on hover and focus; a single row each, height is premium):
  - **Embed** (↗ many-to-one): opens the existing embed flow pre-filled for *that* instance: parent = the embedded object's relation, join from the hop, object name = singular of the table (`brand`), columns to include. If the embedded object holds the foreign-key field (`product.brandId`), the dialog offers **replace** (default, OC-082) or **keep alongside**.
  - **Embed** (↙ one-to-many, ↔ many-to-many): an array under the embedded object (`product.reviews[]`) with the usual limit, order, filter and overflow options (OC-045/046), defaulting to the 1.0 limit; the estimate is shown in the dialog.
  - **Look up** (↗ many-to-one): pick columns to map as fields of the embedded object without a nested object (`product.brandName` ← `brands.name`), creating a lookup-only relation parented at the product relation.
  - Dragging a hop onto a target object/array binds it there, as dragging a table does today, but with the instance's join pre-filled.
- **Columns of every embedded table** remain listed under its instance (as the Canvas does for embedded groups today), so single columns can be dragged onto any field in scope.
- **Depth limit:** hops below `maxRelationDepth` (OC-141) are listed but disabled, with a tooltip "Deepest level reached (4 of 4). An administrator can raise OC_MAX_RELATION_DEPTH up to 6."
- **Self-references** (`categories → categories`): listed under the instance like any hop; embedding one creates one level ("parent category"). Repeating levels is OC-144.
- **Composer reach:** the solid/outlined/dimmed marking (OC-048) is computed from the selected node's scope and the hops of *every* table in that scope (OC-141), so the embedded product's brand is "reachable" when `items[].product` (or something inside it) is selected.
- Works the same for canvas and composed mappings, and for every target kind.

**Out:** embedding through several hops in one action (each step is one click, at any depth); editing an existing relation's join from this tree (unchanged: the Inspector); recursive hierarchies (OC-144).

## Implementation details
- `web/src/features/relations/DocumentSourceTree.tsx`: one component used by `RelatedTree` (Canvas) and `SourcesPane` (Composer). Model built by a pure function `documentSources(spec, hopsByTable)` → nodes `{ relationId | 'root', table, targetPath, kind, embedded: Hop[], available: Hop[] }`, matching embedded relations to hops by kind, table and join (the existing `relationFor` logic, generalised to any parent).
- Embedding: `composerOps.embedUnder(spec, parentRelationId, hop, options)` (Composer) and the server `automap.AddRelation` with `parentId` (Canvas, OC-043 AC7) share the dialog `EmbedDialog`/`EmbedKeyDialog`, which accept a `parentRelationId`. Each embed is one undo step and runs a scoped auto-map (OC-049) of the new object.
- The "Other tables" list of the Composer stays below the new section.
- Virtualise the tree beyond 300 visible rows (large schemas).

## Acceptance criteria & unit tests

### AC1 — An embedded object's relationships appear under it
**Given** `orders` with `items[]` (order_items) and `items[].product` (products), **then** the source side shows under `products → items[].product` the hops ↗ brands, ↗ categories and ↙ reviews (with "~12 per product"), even when the driving table's graph (depth 4, 200 paths) doesn't contain them.

| Test | Location | Verifies |
|---|---|---|
| `documentSources.test.ts` › `OC-142 AC1: hops under each instance` | `web/src/features/relations/documentSources.test.ts` | Pure model |
| `RelatedTree.test.tsx` › `OC-142 AC1` | `web/src/features/mapping/RelatedTree.test.tsx` | Canvas |
| `SourcesPane.test.tsx` › `OC-142 AC1` | `web/src/features/composer/SourcesPane.test.tsx` | Composer |

### AC2 — Embed under the embedded object
**Given** AC1's tree, **when** I click **Embed** on ↗ brands under the product and confirm with `name`, **then** a many-to-one relation is created with `parentId` = the product relation, `items[].product.brand{name}` is added and auto-mapped, and the preview shows each line's product with its brand. **Given** `product.brandId` is mapped, **then** replace is the default and `brandId` is removed (OC-082).

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-142 AC2: embedUnder many-to-one` | `web/src/features/composer/composerOps.test.ts` | |
| `TestAddRelation_AC2_UnderNestedManyToOne` | `backend/internal/automap/relations_test.go` | Canvas server path |

### AC3 — Arrays and lookups under the embedded object
**Given** ↙ reviews under the product, **when** I embed it with limit 5 ordered by `created_at desc`, **then** `items[].product.reviews[]` holds at most 5 reviews per product. **Given** **Look up** on ↗ categories with `name`, **then** `items[].product.categoryName` ← `categories.name` through a lookup-only relation under the product relation, and no `category` object.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-142 AC3: array and lookup under instance` | `web/src/features/composer/composerOps.test.ts` | |
| `TestComposed_AC3_LookupUnderNestedManyToOne` | `backend/internal/transform/composed_test.go` | Document output |

### AC4 — One node per instance
**Given** `customer.billingAddress` and `customer.shippingAddress` both embedded from `addresses` (two foreign keys), **then** the tree shows two `addresses` nodes with their own target paths, and embedding `countries` under the shipping one nests it only there.

| Test | Location | Verifies |
|---|---|---|
| `documentSources.test.ts` › `OC-142 AC4: same table twice` | `web/src/features/relations/documentSources.test.ts` | |

### AC5 — Embedded, lookups and navigation
**Given** embedded relations, **then** they show ✓ and their target path; clicking selects the target field; lookup-only relations show "lookup" and the fields they feed. **Given** a target field is selected, **then** the path to its relation is expanded.

| Test | Location | Verifies |
|---|---|---|
| `DocumentSourceTree.test.tsx` › `OC-142 AC5` | `web/src/features/relations/DocumentSourceTree.test.tsx` | |

### AC6 — Depth limit
**Given** `maxRelationDepth` 4 and an instance at depth 4, **then** its hops are listed but disabled with the tooltip, and no action can create a fifth level.

| Test | Location | Verifies |
|---|---|---|
| `DocumentSourceTree.test.tsx` › `OC-142 AC6: depth limit` | `web/src/features/relations/DocumentSourceTree.test.tsx` | |

### AC7 — Composer reach uses the embedded tables
**Given** `items[].product` is selected in the Composer, **then** `brands`, `categories` and `reviews` are outlined as reachable, and dropping `brands` on a new object under `product` binds it many-to-one through `brand_id` without asking for a join.

| Test | Location | Verifies |
|---|---|---|
| `scope.test.ts` › `OC-142 AC7: reach from embedded tables` | `web/src/features/composer/scope.test.ts` | |
| `Composer.test.tsx` › `OC-142 AC7: drop brands under product` | `web/src/features/composer/Composer.test.tsx` | |

### AC8 — Accessible and end to end
**Given** the tree, **then** it's an ARIA tree (arrow keys expand and move, Enter runs the default action), axe-clean in both themes. **Given** the dev stack (`05-chained-fks.sql`, OC-144), **when** a user embeds product → brand → supplier from the source side and migrates, **then** the document browser shows the nested objects.

| Test | Location | Verifies |
|---|---|---|
| `Composer.a11y.test.tsx` › `OC-142 AC8` | `web/src/features/composer/Composer.a11y.test.tsx` | |
| `chained-embeds.spec.ts` | `web/tests/e2e/chained-embeds.spec.ts` | |
