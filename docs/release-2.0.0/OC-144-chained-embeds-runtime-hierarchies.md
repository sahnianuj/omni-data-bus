# OC-144 — Chained embeds at runtime: shared lookups, hierarchies, parity and benchmark

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P1 | 5 | OC-142, OC-143, OC-046, OC-056 | Not started |

## User story
As a **data engineer** who embeds objects inside embedded objects (order → items → product → brand → supplier → country) and parent categories inside categories, I want those documents built correctly and fast, with no repeated reads of the same rows and a predictable document size, so that rich, deeply nested documents are as safe to migrate as flat ones.

## Background
The resolver (OC-043) already reads relations depth by depth with one query per relation per batch, de-duplicating join keys within that relation. Chains of many-to-one objects were never measured, though, and three cases aren't handled well:
- **The same table embedded in several places** (`items[].product` and `featuredProduct`; billing and shipping `address → country`) is read once **per relation**, even for the same keys in the same batch.
- **Hierarchies:** a self-reference (`categories.parent_id`) can be embedded one level at a time (OC-142), but "the category with its parents up to the top" needs one relation per level and can loop on bad data (A → B → A).
- **Size:** many-to-one chains add no fan-out but each level copies a whole row into every array item. The preview's worst case (OC-049) and the size guard (OC-046) cover it, but the embed dialogs don't say what a level will cost.

## Scope
**In:**
- **Shared many-to-one reads per batch:** relations at the same depth that read the same table with the same join columns share one query and one row cache for the batch (keyed by table, join columns and key values). Different filters or columns keep separate reads. Results are identical with and without sharing.
- **Hierarchy embeds (self-references):** in the embed dialog for a self-referencing many-to-one hop, **Levels: 1–N** (N ≤ the remaining relation depth) and **Shape**: nested (`category.parent.parent`) or **path array** (`categoryPath: [{…root}, …, {…leaf}]`, ordered root first). The spec stores one relation per level (no new relation kind); the path-array shape is a computed collection built from those levels. Embedding stops at a null parent; a **cycle in the data** stops at the first repeated key and adds a row warning `HIERARCHY_CYCLE`.
- **Cost hint in the embed dialogs** (OC-142/143): "+~0.4 KB per item, largest document ≈ 18 KB" from the preview worst-case statistics of the current sample, with the existing size-guard warning when a level would push the worst case over the limit.
- **Parity:** a golden mapping `orders → items[] → product → brand → supplier → country` plus `product.category` with 3 parent levels, byte-identical through preview, the server pipeline and `odb run`, for the Couchbase and directory targets.
- **Dev seed `05-chained-fks.sql`:** `sales.countries`, `sales.suppliers (country_code → countries)`, `sales.categories (parent_id → categories, 4 levels, one deliberate cycle in a separate test schema only)`, `brands.supplier_id` and `products.category_id` (nullable). Golden fixtures that generate all columns of `brands`/`products` are regenerated and reviewed in the same change.
- **Benchmark gate:** **G9 — Chained embeds throughput**: an order with 10 items, each with product → brand → supplier → country (5 tables, 4 levels) ≥ 6,000 docs/s on the reference environment; memory within G4b.

**Out:** recursive *one-to-many* trees (a category with all its descendants: unbounded fan-out; possible later story); recursive SQL (`WITH RECURSIVE`); embedding across connections.

## Implementation details
- `internal/related`: group the plan's relation steps at each depth by `(table, joinChildColumns, columnSet ∪, filter == nil)` for many-to-one; issue one `= ANY($1)` query with the union of keys and columns; fan the rows out to each relation's parent contexts. Covered by the existing `OC_RELATION_MEMORY_MB` accounting (count shared rows once).
- Hierarchy: `composerOps.embedHierarchy(spec, parentRelationId, hop, {levels, shape})` creates `levels` many-to-one relations, each the parent of the next; the path-array shape adds an internal `collect` step in the execution plan (`plan.CollectPath`) that walks the levels after assembly. The cycle check is per document (a set of seen keys along the chain).
- Cost hint: `preview` already returns per-field size statistics; add `sizeDelta(relationId)` from a preview with and without the new relation (the dialog requests it on open; cancelled if the dialog closes).

## Acceptance criteria & unit tests

### AC1 — Shared reads, same documents
**Given** `items[].product` and `featuredProduct` both from `products` by `product_id`, **then** each batch issues one `products` query, and the documents equal those built with sharing disabled.

| Test | Location | Verifies |
|---|---|---|
| `TestResolver_AC1_SharedManyToOneReads` | `backend/internal/related/related_test.go` | Query count (counting source stub) + golden |
| `TestResolver_AC1_SharedReadsIntegration` *(integration)* | `backend/internal/related/merge_integration_test.go` | Real PG, query count from a counting wrapper around the related-rows reader |

### AC2 — Hierarchies
**Given** a category 4 levels deep and **Levels: 3, nested**, **then** `category.parent.parent.parent` is filled and stops early at a null parent. **Given** **path array**, **then** `categoryPath` lists the ancestors root first. **Given** A → B → A in the data, **then** the chain stops at the repeat and the row has a `HIERARCHY_CYCLE` warning.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-144 AC2: embedHierarchy` | `web/src/features/composer/composerOps.test.ts` | |
| `TestHierarchy_AC2_NestedPathArrayCycle` | `backend/internal/transform/relations_test.go` | |

### AC3 — Cost hint
**Given** the embed dialog for ↗ brands under `items[].product`, **then** it shows the added size per item and the new worst-case document size from the sample; **given** a level that pushes the worst case over the size limit, **then** the size-guard warning appears in the dialog.

| Test | Location | Verifies |
|---|---|---|
| `EmbedDialog.test.tsx` › `OC-144 AC3: cost hint` | `web/src/features/mapping/EmbedDialog.test.tsx` | |
| `TestPreview_AC3_SizeDelta` | `backend/internal/preview/composed_integration_test.go` | |

### AC4 — Parity
**Given** the chained golden mapping, **then** preview, the server pipeline and `odb run` produce byte-identical documents for both target kinds.

| Test | Location | Verifies |
|---|---|---|
| `TestParity_AC3_GoldenMappings` (adds `chained-embeds.json`) | `backend/internal/headless/parity_integration_test.go` | |

### AC5 — Benchmark gate
**Given** `make bench-gate` with the new `chained` dataset, **then** G9 is evaluated and recorded under `bench/results/2.0.0/`.

| Test | Location | Verifies |
|---|---|---|
| `odbbench gate` with G9 | `bench/gates-2.0.0.json` | |
