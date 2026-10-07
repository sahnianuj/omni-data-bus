# OC-141 — Relationship hops from any table, on demand

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P0 | 3 | OC-042 | Not started |

## User story
As a **data engineer** who has embedded a related object (an order line's `product`), I want the tool to know that object's own relationships (product → brand, product → category, product → reviews) however deep it sits in the document and however large the schema is, so that the source side can offer them for embedding (OC-142, OC-143).

## Background (gap found 2026-10-06)
Both source panels and the "Embed *X* here…" action (OC-064) get relationships from **one** graph request rooted at the driving table: `GET /api/v1/connections/{id}/relations?root=<driving>&depth=4` (`relations.Graph.Paths`). The paths in that response are the only hops the web app knows about, and they are limited three ways:
- **Depth 4** from the driving table. `orders → order_items → products → brands → suppliers` needs 4 hops just to reach `suppliers`; its `countries` is out of reach.
- **200 paths** (`MaxPaths`), ranked by length and quality, with each level expanded only from the paths kept at the level before. On a schema with many foreign keys (a typical ERP), level 3 is cut and the embedded product's relationships simply don't appear.
- **No table twice on a path.** Self-references (`categories.parent_id → categories`, `employees.manager_id → employees`) are never listed, and neither is a second hop into a table already on the path (`orders → customers → addresses` and `orders → addresses` for the shipping address).

The graph itself (`relations.Build`) has every hop, including self-references; only the path enumeration drops them.

## Scope
**In:**
- A **direct hops** endpoint: `GET /api/v1/connections/{id}/relations/hops?table=<schema.table>` returning every relationship of one table:
  - many-to-one (↗) and one-to-many (↙) hops from foreign keys and virtual relationships (OC-042), **including self-references** (flagged `self: true`);
  - many-to-many candidates through junction tables where this table is one end (OC-044);
  - per hop: join columns, constraint or virtual relationship id, whether the child side is indexed (index advice), and an **estimate**: rows per parent for one-to-many (from table statistics: row count ÷ distinct parent keys when available, else row-count ratio) and "1" for many-to-one.
- `maxRelationDepth` (the effective `OC_MAX_RELATION_DEPTH`) in the response, so the UI can tell how deep an embed may go.
- A web hook `useRelationHops(connId, table)` cached per table (React Query, shared by both panels and the Embed actions), plus `hopsFor(table)` helpers replacing `edges(graph)` wherever the code needs "the relationships of this table" (`bindPrefill.foreignKeyOf`, `scope` reach, `EmbedDialog`).
- The existing graph endpoint stays as is (it still serves the first level and the path ranking of the Related tree).

**Out:** changing `Paths` ranking or limits; relationships across connections.

## Implementation details
- `relations.Service.Hops(ctx, ws, connID, table) (HopsResult, error)` uses the cached `Graph` (built from the latest discovery snapshot and virtual relationships) and `Graph.Hops(table)`, which already returns the adjacency list; junctions come from `Junctions(snap, table)`. Add estimates in `relations/estimate.go` from snapshot stats (`rowEstimate`, column `nDistinct` where discovery recorded it).
- OpenAPI: `GET /connections/{id}/relations/hops` in `api/openapi.yaml`; regenerate the web client types.
- Permissions: same as the graph endpoint (viewer).
- Response size: one table's hops; no pagination needed (a table with > 500 hops returns the first 500 by quality and `truncated: true`).

## Acceptance criteria & unit tests

### AC1 — All direct hops, self-references included
**Given** `sales.products` with `brand_id → brands`, `category_id → categories`, reviews referencing it and `categories.parent_id → categories`, **then** `hops?table=sales.products` lists ↗ brands, ↗ categories, ↙ order_items, ↙ reviews; and `hops?table=sales.categories` lists ↗ categories (`self: true`) and ↙ categories (`self: true`) next to ↙ products.

| Test | Location | Verifies |
|---|---|---|
| `TestHops_AC1_DirectHopsAndSelfReferences` | `backend/internal/relations/graph_test.go` | |
| `TestHopsHandler_AC1` | `backend/internal/relations/handlers_test.go` | Contract, permissions |

### AC2 — Not limited by the driving table's graph
**Given** a synthetic schema of 400 tables where `products` is 3 hops from `orders` and the depth-4 graph from `orders` hits `MaxPaths` before listing `products → brands`, **then** `hops?table=sales.products` still lists `brands`.

| Test | Location | Verifies |
|---|---|---|
| `TestHops_AC2_IndependentOfPathCap` | `backend/internal/relations/graph_test.go` | |

### AC3 — Virtual relationships, junctions, index advice and estimates
**Given** a virtual relationship `products.supplier_code → suppliers.code`, **then** it's listed with its id and `virtual: true`. **Given** `product_tags(product_id, tag_id)`, **then** ↔ tags via product_tags. **Given** `reviews.product_id` without an index, **then** `indexed: false`. **Given** stats of 12,000 reviews over 1,000 products, **then** the estimate is 12 per product.

| Test | Location | Verifies |
|---|---|---|
| `TestHops_AC3_VirtualJunctionIndexEstimate` | `backend/internal/relations/graph_test.go` | |

### AC4 — One cached fetch per table in the web app
**Given** the Canvas and Composer both need `products`' hops, **then** one request is made, and `foreignKeyOf` finds `brand_id → brands` for a field in `items[].product` from that response, not from the driving graph.

| Test | Location | Verifies |
|---|---|---|
| `relationsApi.test.ts` › `OC-141 AC4: hops cached per table` | `web/src/features/mapping/relationsApi.test.ts` | |
| `bindPrefill.test.ts` › `OC-141 AC4: foreignKeyOf uses table hops` | `web/src/features/composer/bindPrefill.test.ts` | |
