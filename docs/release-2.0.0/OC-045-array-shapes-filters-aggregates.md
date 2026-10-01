# OC-045 — Array shapes, filters & aggregates

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P1 | 5 | OC-043 | Implemented |

## User story
As a **data engineer**, I want more control over the arrays built from related rows: arrays of plain values (`productIds: [1, 2]`), only the rows that match a condition (only active lines), and summary values on the parent (`itemCount`, `orderTotal`), so that the documents match what my application actually needs.

## Scope
**In:**
- `shape`: `objects` (default, 1.0 behavior) | `scalars` (one column per item) | `keyed` (an object keyed by a column: `{"A-1": {…}, "B-2": {…}}`).
- A structured `filter` on one-to-many and many-to-many relations, pushed down into SQL as parameters.
- `aggregates[]`: `count | sum | min | max | avg | first | last` over a relation's rows, written into scalar fields.

**Out:** free-form SQL filters (a security and portability risk); aggregates over nested relations of relations (only the relation's own rows); window functions.

## Implementation details

### Shapes
| Shape | Item | Required config | Example |
|---|---|---|---|
| `objects` | Object built from item field mappings | — | `items: [{sku, qty}]` |
| `scalars` | One value | `valueColumn` (with OC-020 transforms) | `productIds: [1, 2, 3]` |
| `keyed` | Object keyed by `keyColumn` (string-coerced); duplicate keys → last wins plus `RELATION_DUPLICATE_KEY` warning | `keyColumn` | `products: {"A-1": {…}}` |

The target field type must match: `array` of a scalar type for `scalars`, `object` for `keyed`. The designer converts the field when the shape changes, after confirmation.

### Filter
```json
"filter": [ { "column": "status", "op": "eq", "value": "active" },
            { "column": "quantity", "op": "gt", "value": 0, "from": "via" } ]
```
- Operators: `eq ne lt lte gt gte in notIn isNull notNull like`. Conditions are ANDed. For OR, use `in`.
- Values are coerced to the column type at validation time and sent as bind parameters, never string-concatenated into SQL.
- Filters on the junction (`from: "via"`) are allowed for many-to-many.
- The filter is shown as a chip list in the Embed dialog, with a column picker, operator, and a typed value input.

### Aggregates
```json
"aggregates": [
  { "id": "agg_1", "relationId": "rel_items", "fn": "count", "targetFieldId": "fld_itemCount" },
  { "id": "agg_2", "relationId": "rel_items", "fn": "sum", "column": "line_total", "targetFieldId": "fld_total" }
]
```
- Computed in Go over the relation's rows **after** the filter and **before** the limit, so `itemCount` is the true count even when the array is truncated. This is documented in the tooltip.
- A relation can exist only for aggregates, with no target array (`targetFieldId: null`). Then only the needed columns are fetched.
- `sum` and `avg` over numeric columns use decimal arithmetic (shopspring/decimal) for `numeric` columns, so money totals don't pick up float error. The output follows the 1.0 numeric encoding rules (§6.4).

## Acceptance criteria & unit tests

### AC1 — Scalar arrays
**Given** `productIds` with shape `scalars` over `order_items.product_id`, **then** `productIds: [7, 9]` in `line_no` order.

| Test | Location | Verifies |
|---|---|---|
| `TestApplyRelations_AC1_ScalarShape` | `internal/transform/shapes_test.go` | |

### AC2 — Keyed objects
**Given** shape `keyed` on `sku`, **then** `{"A-1": {…}, "B-2": {…}}`. **Given** a duplicate SKU, **then** the last item wins and `RELATION_DUPLICATE_KEY` is raised.

| Test | Location | Verifies |
|---|---|---|
| `TestApplyRelations_AC2_KeyedShape` | `internal/transform/shapes_test.go` | |

### AC3 — Filter pushdown
**Given** the filter `status eq active`, **then** the SQL has `AND t."status" = $2` with the bound value, and only active lines appear. **Given** a value that can't be coerced to the column type, **then** validation fails with `RELATION_FILTER_VALUE`.

| Test | Location | Verifies |
|---|---|---|
| `TestRelSQL_AC3_FilterParams` | `internal/connectors/source/postgres/relsql_test.go` | Includes an injection attempt as value |
| `TestSpecValidate_AC3_FilterCoercion` | `internal/mapping/validate_test.go` | |

### AC4 — Aggregates
**Given** an order with 1,500 lines, limit 1,000, and aggregates `count` and `sum(line_total)`, **then** `itemCount: 1500`, the exact decimal total, and `items` has 1,000 entries.

| Test | Location | Verifies |
|---|---|---|
| `TestAggregates_AC4_BeforeLimitDecimal` | `internal/transform/aggregates_test.go` | |
| `TestAggregates_AC4_AggregateOnlyRelation` | same | No array field |

### AC5 — Dialog UX
**Given** the Embed dialog, **when** the user switches the shape to `scalars`, **then** a value-column picker appears, the target field becomes `array<type>` after confirmation, and the preview updates.

| Test | Location | Verifies |
|---|---|---|
| `EmbedDialog.test.tsx` › `AC5: shape switch converts field` | `src/features/mapping/EmbedDialog.test.tsx` | |
| `FilterChips.test.tsx` › `AC5: typed values per column` | `src/features/mapping/FilterChips.test.tsx` | |

## Implementation notes (2026-09-29)
- `sum`/`avg` use `math/big` exact rationals (no new dependency instead of shopspring/decimal); sums keep the inputs' scale, averages 6 more digits, trailing zeros trimmed, written as JSON numbers.
- Aggregates need every row, so a relation with aggregates (or the split overflow policy) keeps all rows (`Keep = 0`) and doesn't push its limit into SQL.
- Keyed objects: a null key skips the row with `RELATION_NULL_KEY`. A scalars array uses a mapping on its item field when there is one (transforms), else its `valueColumn`.
- Filters also support `from: "via"` on many-to-many; values are validated with `mapping.FilterArgs` and sent as `$n::<column type>` parameters.
