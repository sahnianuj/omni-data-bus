# OC-043 — Nested relations: multi-level objects and arrays

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P0 | 8 | OC-037, OC-042 | Not started |

## User story
As a **data engineer**, I want to embed related tables inside embedded tables, for example an order with an `items` array where each item holds its `product` object, and to pull single values from a related table into the document without creating a nested object, so that one order document contains everything about the order in the shape my application reads.

## Scope
**In:**
- Relations with `parentId`: a relation under a one-to-many or many-to-one relation, up to depth 4 (hard cap 6).
- **Lookup-only** relations: columns from a many-to-one relation mapped into the parent's own fields (flattening), such as `items[].productName` from `products.name`.
- Batched, level-by-level resolution with no N+1 queries, in the shared source snapshot.
- Canvas support: nested source groups, and the Embed dialog creating a relation under an existing one.

**Out:** many-to-many through a junction (OC-044), array shapes other than objects (OC-045), and the sorted-merge strategy for very large arrays (OC-046).

## Target shape example
```json
{
  "orderId": 1001, "orderedAt": "2026-09-01T10:00:00Z",
  "customerName": "Ada Lovelace",                 // lookup-only m2o: orders → customers
  "items": [                                      // o2m: orders → order_items
    { "lineNo": 1, "quantity": 2, "unitPrice": 9.5,
      "product": { "sku": "A-1", "name": "Pen" }  // m2o under items: order_items → products
    }
  ]
}
```

## Implementation details

### Resolver (`internal/related`, `pipeline.ResolveRelations`)
- Input: a parent batch plus the `ExecutionPlan` (OC-037), whose relation steps are grouped by depth.
- For depth `d = 1..max`: for every relation at depth `d`, collect the distinct join keys from **all** rows produced at depth `d-1` for its parent (the root rows when `parentId` is null), then run **one** query per relation with the 1.0 SQL builder (`= ANY($1)` / `unnest`). Rows are attached to their parent row context under `relations[relId]`.
- Queries at the same depth run concurrently (bounded by `relationConcurrency`, default 4). Depths run in sequence, since each depth needs the previous depth's keys.
- All lookups join the exported snapshot (as OC-021 AC5).
- Query count per batch = the number of relations, whatever the depth or the number of rows.
- Memory: rows are kept only until the batch's documents are built. The total size of relation rows per batch is capped by `OC_RELATION_MEMORY_MB` (default 256). When a batch would exceed it, the resolver splits the parent batch in half and retries (recursively down to 1 parent). A single parent that still exceeds the cap → row error `RELATION_MEMORY_EXCEEDED` (OC-046 adds better strategies).

### Transform
- The compiled program has one sub-program per relation scope (OC-037). An object or array item's fields are evaluated with a row context holding the current relation row plus its ancestors (`$parent`, `$root`), so an item field can read a root column, for example `items[].orderId` from `orders.order_id`.
- Lookup-only relations resolve to at most one row (m2o). Their columns are available to every field in the scope of the relation's parent. A missing row → null per null handling.

### Canvas (extends OC-021 UI)
- Embedded groups can be expanded to show their own **Related tables**. Clicking *Embed* there creates a child relation.
- The Embed dialog gains **Mode: nested object | values only (lookup)** for m2o.
- The target panel shows nested arrays and objects with relation badges (for example `items[] ↙ order_items`, `product ↗ products`).
- The Related tree (OC-042) lets the user pick a whole path, such as `order_items → products`, and creates both relations and both target fields in one step, with *Generate fields* and scoped auto-map at each level.

### Validation
- A relation's `targetFieldId` must lie **inside** its parent relation's field (for example `product` inside `items[]`), unless it is lookup-only.
- Lookup-only relations must be many-to-one (or one-to-one).
- The estimated document size (OC-021 AC6) multiplies across levels: rows per parent × rows per child × average row width.

## Acceptance criteria & unit tests

### AC1 — Two-level assembly
**Given** order 1001 with 3 items, each referencing a product, **then** the document matches the target shape example: 3 items ordered by `line_no`, each with its product object.

| Test | Location | Verifies |
|---|---|---|
| `TestApplyRelations_AC1_OrderItemsProduct` | `internal/transform/relations_test.go` | Golden `testdata/golden/order_items_product.json` |

### AC2 — One query per relation per batch
**Given** 1,000 orders with 4,000 items and 600 distinct products, **then** exactly 2 lookup queries run: `order_items` with 1,000 keys, then `products` with 600 keys.

| Test | Location | Verifies |
|---|---|---|
| `TestResolveRelations_AC2_QueryCountByDepth` | `internal/pipeline/relations_test.go` | Fake querier |

### AC3 — Lookup-only flattening
**Given** a lookup-only relation `orders → customers` and a mapping `customerName ← customers.name`, **then** the document has `customerName` and no `customer` object. **Given** a dangling FK, **then** `customerName` is null or omitted per null handling.

| Test | Location | Verifies |
|---|---|---|
| `TestApplyRelations_AC3_LookupOnly` | `internal/transform/relations_test.go` | |

### AC4 — Ancestor access
**Given** `items[].orderId ← $root.order_id`, **then** each item carries the order's ID.

| Test | Location | Verifies |
|---|---|---|
| `TestApplyRelations_AC4_RootColumnInItem` | `internal/transform/relations_test.go` | |

### AC5 — Memory cap splits batches
**Given** `OC_RELATION_MEMORY_MB=1` and a batch whose relation rows are ~3 MB, **then** the resolver splits it and every document is still built. **Given** one parent whose rows alone are 2 MB, **then** that row fails with `RELATION_MEMORY_EXCEEDED` and the rest succeed.

| Test | Location | Verifies |
|---|---|---|
| `TestResolveRelations_AC5_SplitOnMemoryCap` | `internal/pipeline/relations_test.go` | |

### AC6 — Validation
**Given** a relation under `items` whose target field is at the root, **then** `RELATION_FIELD_OUTSIDE_PARENT`. **Given** depth 5 with the default cap, **then** `RELATION_TOO_DEEP`. **Given** a one-to-many relation marked lookup-only, **then** `RELATION_LOOKUP_KIND`.

| Test | Location | Verifies |
|---|---|---|
| `TestSpecValidate_AC6_NestedRelationErrors` | `internal/mapping/validate_test.go` | |

### AC7 — Path embed in one step
**Given** the user picks `order_items → products` in the Related tree with *Generate fields*, **then** `items[]` and `items[].product` are created, and both levels are auto-mapped within their own scope.

| Test | Location | Verifies |
|---|---|---|
| `TestAddRelationPath_AC7_GeneratesAndAutomaps` | `internal/automap/relations_test.go` | |
| `EmbedDialog.test.tsx` › `AC7: embeds a two-hop path` | `src/features/mapping/EmbedDialog.test.tsx` | |

### AC8 — Snapshot consistency at every depth
**Given** a product renamed during the run, **then** documents show the name as of the snapshot, at depth 2 as well as depth 1.

| Test | Location | Verifies |
|---|---|---|
| `TestRelations_AC8_SnapshotAllDepths` *(integration)* | `internal/pipeline/relations_integration_test.go` | |
