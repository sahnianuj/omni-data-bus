# OC-044 — Many-to-many arrays through junction tables

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P0 | 8 | OC-042, OC-043 | Implemented |

## User story
As a **data engineer** whose database stores order lines in a junction table (`order_items(order_id, product_id, quantity)`), I want to embed `products` directly into the order as an array, with each product carrying the junction's quantity and price, so that I get `order.products[]` in one step without modeling the junction table myself.

## Scope
**In:**
- Relation kind `many-to-many` with a `via` junction ([architecture §6.1](00-architecture.md#61-mapping-spec-v2-mappingspec)).
- Junction payload columns (`quantity`, `unit_price`) mappable into each array item.
- One lookup query per batch, joining the junction and the target table in the source database.
- Order-by and limit over junction or target columns, plus optional de-duplication.
- Creation from a junction candidate (OC-042) in one click.

**Out:** chains of two junctions in one relation. Model those as a many-to-many with a nested relation (OC-043).

## Implementation details

### Model
```json
{ "id": "rel_products", "kind": "many-to-many",
  "table": { "schema": "public", "name": "products" },
  "via": { "table": { "schema": "public", "name": "order_items" },
           "parentJoin": [ { "parent": "order_id", "via": "order_id" } ],
           "childJoin":  [ { "via": "product_id", "child": "product_id" } ],
           "columns": [ "quantity", "unit_price" ] },
  "targetFieldId": "fld_products", "orderBy": [ { "column": "line_no", "from": "via" } ],
  "distinct": false, "limit": 1000 }
```
- Field mappings read target columns as `{relation:"rel_products", column:"name"}` and junction columns as `{relation:"rel_products", column:"quantity", from:"via"}`.

### SQL (`internal/connectors/source/postgres/relsql.go`)
```sql
SELECT j."order_id" AS "__p0", j."quantity" AS "__v_quantity", j."unit_price" AS "__v_unit_price",
       t."product_id", t."sku", t."name"
FROM "public"."order_items" j
JOIN "public"."products" t ON t."product_id" = j."product_id"
WHERE j."order_id" = ANY($1)
ORDER BY j."order_id", j."line_no"
```
- Only the needed columns are selected (plan projection, OC-037). Junction columns are aliased `__v_*` so they can't clash with target column names.
- Composite keys use the `unnest` form. Limits ≤ 100 use `row_number() OVER (PARTITION BY j.<parent cols> ORDER BY …)`, as in 1.0.
- `INNER JOIN` is the default: junction rows whose target row is missing are dropped and counted as the row warning `RELATION_DANGLING` (with the count). The option `keepDangling: true` switches to a `LEFT JOIN`, producing items with null target fields.
- `distinct: true` groups by the target's primary key per parent (`DISTINCT ON`). Junction columns then come from the first row in order. The UI explains this.

### UI
- A junction candidate in the Related tree (OC-042) shows **Embed as array of products**. The Embed dialog pre-fills `via`, lists junction payload columns as checkboxes (all checked), and offers *Generate fields*, which creates the item object with target columns and payload columns and auto-maps them (scoped).
- In the canvas source panel, a many-to-many group shows two sub-groups: *products* and *via order_items*.

### Validation
- `via.parentJoin` columns exist on the root/parent and the junction; `via.childJoin` columns exist on the junction and the target, with compatible types.
- `via.columns` exist on the junction and aren't join columns.
- The target field is an array (objects, or scalars per OC-045).

## Acceptance criteria & unit tests

### AC1 — Order with products via order_items
**Given** order 1001 with lines (product A, qty 2), (product B, qty 1), **then** `products` is `[{sku:"A",name:…,quantity:2,unitPrice:…},{sku:"B",…,quantity:1,…}]`, ordered by `line_no`.

| Test | Location | Verifies |
|---|---|---|
| `TestApplyRelations_AC1_ManyToManyPayload` | `internal/transform/relations_m2m_test.go` | Golden `testdata/golden/order_products_m2m.json` |

### AC2 — Single query per batch
**Given** a batch of 1,000 orders, **then** one query joins the junction and target, with correctly quoted identifiers and `__v_` aliases. **Given** a composite parent key, **then** the `unnest` form is used.

| Test | Location | Verifies |
|---|---|---|
| `TestRelSQL_AC2_ManyToManyJoin` | `internal/connectors/source/postgres/relsql_test.go` | Golden SQL |
| `TestRelSQL_AC2_CompositeUnnest` | same | |
| `TestResolveRelations_AC2_OneQueryM2M` | `internal/pipeline/relations_test.go` | |

### AC3 — Dangling & distinct
**Given** a junction row pointing to a deleted product, **then** by default the item is dropped with `RELATION_DANGLING` (count 1). With `keepDangling`, **then** it is kept with null product fields. **Given** the same product on two lines and `distinct: true`, **then** one item.

| Test | Location | Verifies |
|---|---|---|
| `TestApplyRelations_AC3_DanglingDropAndKeep` | `internal/transform/relations_m2m_test.go` | |
| `TestApplyRelations_AC3_Distinct` | same | |

### AC4 — One-click embed from a junction candidate
**Given** the candidate `products via order_items`, **when** the user confirms the dialog with defaults, **then** a many-to-many relation, a `products[]` field with product and payload fields, and scoped auto-mappings are created.

| Test | Location | Verifies |
|---|---|---|
| `TestAddRelation_AC4_FromJunctionCandidate` | `internal/automap/relations_test.go` | |
| `EmbedDialog.test.tsx` › `AC4: junction prefill and payload columns` | `src/features/mapping/EmbedDialog.test.tsx` | |

### AC5 — Validation
**Given** a `via.columns` entry that is a join column, or a missing junction column, **then** validation errors reference the relation ID.

| Test | Location | Verifies |
|---|---|---|
| `TestSpecValidate_AC5_ManyToManyErrors` | `internal/mapping/validate_test.go` | |

### AC6 — Nested under many-to-many
**Given** `products[]` (m2m) with a nested m2o `products → brands`, **then** each product item holds its brand, and the total query count per batch is 2.

| Test | Location | Verifies |
|---|---|---|
| `TestResolveRelations_AC6_NestedUnderM2M` *(integration)* | `internal/pipeline/relations_integration_test.go` | |

## Implementation notes (2026-09-29)
- Many-to-many rows are read with a `LEFT JOIN` always; rows whose target join columns are null are dropped in Go and counted (`RELATION_DANGLING` with the count) unless `keepDangling`. `INNER JOIN` would drop them without a count. For the same reason the SQL per-key limit (`row_number`) is only used when no row can be dropped after the query (`keepDangling` without `distinct`).
- `distinct` is applied in Go on the target's primary key (or its join columns without one), keeping the first row in order; junction columns come from that row.
- A relation's row is its table's columns followed by `mapping.ViaColumns` (parent join, payload, junction order columns) named `__v_<column>`; sources/order/filters/aggregates reach them with `from: "via"`.
