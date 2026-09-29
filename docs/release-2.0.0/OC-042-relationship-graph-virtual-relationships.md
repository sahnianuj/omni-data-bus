# OC-042 — Relationship graph, junction-table detection & virtual relationships

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P0 | 8 | OC-037, OC-010 | Not started |

## User story
As a **data engineer**, I want the tool to show every way my root table connects to other tables, across several hops, through junction tables, and through relationships I define myself when the database has no foreign key, so that I can pick the right path to build an order document that contains all of its products.

## Scope
**In:**
- A relationship graph built from the discovery snapshot (FKs in both directions).
- Multi-hop path search from a root table (default depth 4): `orders → order_items → products`.
- Junction-table detection, which turns `orders ↔ products via order_items` into one many-to-many candidate that keeps the junction's extra columns (`quantity`, `unit_price`).
- **Virtual relationships:** user-defined joins where no FK exists, stored per project, with cardinality detection by sampling.
- Index advice: warn when a child's join columns have no index.

**Out:** relationships across different source databases, and joins on expressions (for example `lower(a) = lower(b)`).

## Implementation details

### Graph (`internal/relations/graph.go`)
- Nodes are tables and views. Edges are FKs, each giving two directed edges: child→parent (**many-to-one**) and parent→child (**one-to-many**), plus virtual relationships.
- `Paths(root, maxDepth) []Path`: a breadth-first search that never revisits a table on the same path. Paths are ranked by length, then by whether each hop has an FK and an index, then by name. Results are capped at 200 paths and computed in < 50 ms for 1,000 tables.
- Each `Path` is a list of hops `{from, to, kind, join, viaVirtual}` and converts directly into nested `relations[]` (OC-043) or one many-to-many relation (OC-044).

### Junction detection (`internal/relations/junction.go`)
A table `J` is a junction between `A` and `B` when:
1. `J` has FKs to both `A` and `B` (and `A ≠ B`, or a self-join with two distinct FKs, such as `friend(user_a, user_b)`), and
2. `J`'s primary key or a unique constraint is exactly the union of those FK columns, **or** `J` has ≤ 4 non-key columns (a "payload" junction like `order_items(order_id, product_id, quantity, unit_price)`).

Candidates carry `payloadColumns` (the junction columns that aren't FK columns) and a confidence value (strict PK rule = high, payload rule = medium). A junction with a surrogate PK (`order_item_id`) still qualifies through rule 2.

### Virtual relationships (`relationships` collection, key `<projectId>::<relId>`)
```json
{ "id": "vrel_01", "projectId": "…", "name": "orders.cust_code → customers.code",
  "from": { "schema": "public", "table": "orders", "columns": ["cust_code"] },
  "to":   { "schema": "public", "table": "customers", "columns": ["code"] },
  "cardinality": "many-to-one",            // many-to-one | one-to-one | one-to-many (as seen from `from`)
  "detected": { "maxPerKey": 1, "sampledRows": 100000, "at": "…" } }
```
- **Create dialog:** pick both tables and column pairs (types must be compatible under OC-020 coercion rules), then **Detect cardinality**, which runs on the source:
  `SELECT max(c) FROM (SELECT count(*) c FROM <to> GROUP BY <to cols> LIMIT 100000) s` (and the same for `from`), under `statement_timeout = 10s`. The result sets the suggested cardinality, which the user can override. An override that contradicts the sample shows a warning.
- Virtual relationships appear in the graph with a dashed edge and a "virtual" badge, and they are included in bundles (OC-050).

### Index advice
For each one-to-many or many-to-many hop, the child's join columns (the junction's parent-side columns for m:n) must be the leading columns of some index. Otherwise the warning `RELATION_NO_INDEX` says: "Lookups on order_items.order_id will scan the table; consider `CREATE INDEX ON order_items(order_id)`." We never create the index ourselves.

### API & UI
- `GET /connections/{id}/relations?root=public.orders&depth=4` returns paths and junction candidates.
- `GET|POST|PUT|DELETE /projects/{projectId}/relationships[/{relId}]` for virtual relationships, plus `POST …/relationships/detect`.
- The workbench's **Related tables** panel (OC-021) becomes a tree: direct relations first, expandable to deeper hops, with junction candidates shown as "↔ products via order_items (quantity, unit_price)".

## Acceptance criteria & unit tests

### AC1 — Multi-hop paths
**Given** `orders → order_items (FK order_id) → products (FK product_id)`, **then** `Paths(orders, 4)` includes `orders ↙ order_items ↗ products` with kinds one-to-many then many-to-one, and doesn't include a path that revisits `orders`.

| Test | Location | Verifies |
|---|---|---|
| `TestPaths_AC1_MultiHop` | `internal/relations/graph_test.go` | |
| `TestPaths_AC1_NoCycles` | same | |
| `BenchmarkPaths_1000Tables` | same | < 50 ms |

### AC2 — Junction detection
**Given** `order_items(order_item_id PK, order_id FK, product_id FK, quantity, unit_price)`, **then** a many-to-many candidate `orders ↔ products` with payload `[quantity, unit_price]` and medium confidence. **Given** `order_tags(order_id, tag_id)` with a composite PK, **then** high confidence. **Given** a table with 2 FKs and 12 other columns, **then** it is not a junction.

| Test | Location | Verifies |
|---|---|---|
| `TestJunction_AC2_Rules` | `internal/relations/junction_test.go` | Table-driven |
| `TestJunction_AC2_SelfJoin` | same | |

### AC3 — Virtual relationship CRUD & validation
**Given** a virtual relationship between `orders.cust_code (text)` and `customers.code (text)`, **then** it is saved and appears in the graph. **Given** `int4` against `jsonb`, **then** `422 RELATION_TYPE_MISMATCH`. **Given** a workspace mismatch, **then** `404`.

| Test | Location | Verifies |
|---|---|---|
| `TestRelationshipsAPI_AC3_CRUD` | `internal/httpapi/relationships_test.go` | |
| `TestRelationshipsAPI_AC3_TypeMismatch` | same | |

### AC4 — Cardinality detection
**Given** `customers.code` unique in the data, **then** detection suggests many-to-one. **Given** duplicates, **then** one-to-many with `maxPerKey`. **Given** the query times out, **then** the result is "unknown" and the user must choose.

| Test | Location | Verifies |
|---|---|---|
| `TestDetectCardinality_AC4_Suggests` *(integration)* | `internal/relations/detect_integration_test.go` | |
| `TestDetectCardinality_AC4_Timeout` | `internal/relations/detect_test.go` | |

### AC5 — Index advice
**Given** `order_items.order_id` without an index, **then** `RELATION_NO_INDEX` with the suggested DDL. **Given** a composite index leading with `order_id`, **then** no warning.

| Test | Location | Verifies |
|---|---|---|
| `TestIndexAdvice_AC5` | `internal/relations/advice_test.go` | |

### AC6 — Related tables tree
**Given** the workbench for `orders`, **then** the panel shows `order_items` (↙), `customers` (↗), expandable `order_items → products`, and the junction candidate `products via order_items`.

| Test | Location | Verifies |
|---|---|---|
| `RelatedTree.test.tsx` › `AC6: hops, junctions and virtual badge` | `src/features/mapping/RelatedTree.test.tsx` | |
