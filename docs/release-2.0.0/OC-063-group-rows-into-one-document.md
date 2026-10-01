# OC-063 — One document per group of rows (group the driving table)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P0 | 8 | OC-047, OC-043, OC-046, OC-049 | Implemented |

## User story
As a **data engineer** whose source holds only a detail table, such as `order_detail(type, order_id, line_no, product_id, quantity)` with one row per order line and no `orders` table, I want one document per `order_id` with an array of that order's rows, so that I get

```json
{ "type": "order_detail", "orderId": 1,
  "products": [ { "lineNo": 1, "productId": 2, "quantity": 3 },
                { "lineNo": 2, "productId": 4, "quantity": 2 } ] }
```

and can still embed each line's product through `product_id` (OC-064).

## Background (gap found 2026-09-30)
- **Already supported:** when an `orders` table exists, drive from it and bind `products[]` one-to-many to `order_detail` (OC-043/047). Nothing new is needed for that case.
- **Not supported:** grouping the driving table itself. `spec.root` is a table, and every row becomes a document. The closest workaround is a virtual relationship from `order_detail` to itself on `order_id`. It produces one document **per line**, each with the whole array, all written under the same key `order::{order_id}`. That means N× the reads and writes, a `KEY_MAY_COLLIDE` warning, and whichever line is written last wins. The relationship graph also skips paths back to the root table, so the composer never offers that relationship.

## Scope
**In:**
- **Group by** on the driving table: `spec.root.groupBy: ["order_id"]` gives one document per distinct value of those columns.
- **The group's rows** as a bindable source: an array (or, with `first`, an object) bound to `{kind: "group-rows"}` holds the group's rows, ordered (`orderBy`, default the table's primary key) and capped like any array (`limit`, `overflow`: truncate, fail or split, OC-046).
- **Root fields** may map the group-by columns, constants, aggregates over the group (count, sum, min, max, first, last, OC-045), and other columns through *First row* (an explicit adapter, e.g. `type`, which is the same on every line). Anything else is an error.
- **Nested relations under the group's rows** work as under any array: `products[].product` many-to-one to `products` through `product_id` (OC-043/064).
- **Execution**: a single ordered read (`ORDER BY order_id, line_no`), with consecutive rows streamed into groups. There's no second query. Checkpoints sit at group boundaries, so resume never splits a document.
- **Composer UI**: next to the driving table, *One document per*: *row* (default) or *distinct …* (pick columns). The Sources pane then shows *Rows of this group (order_detail)* as a bindable source.
- **Suggestions (OC-060)**: an imported design whose root fields match a non-unique column set of the driving table, with an array whose fields match the same table's other columns, gets a proposal to group by those columns and bind the array to the group's rows.
- The same behaviour for Couchbase and directory targets: key, file name and document are identical.

**Out:**
- Grouping in the Canvas (composer only; the Canvas shows a read-only notice).
- Grouping on expressions (only columns).
- Groups whose rows aren't contiguous in a sort, e.g. a source that can't `ORDER BY`. PostgreSQL always can.

## Implementation details

### Spec (v2, additive)
- `Root{Table, GroupBy []string}`. `GroupBy` columns must exist in the driving table, and they're the document's identity.
- `Relation.Kind = "group-rows"`: no `table` (it's the driving table), no `join`, `parentId` null, and only one per spec. `orderBy`, `limit`, `overflow`, `filter`, `shape` and `empty` apply.
- Validation:
  - `GROUP_BY_COLUMN_MISSING` (error).
  - `ROOT_FIELD_NOT_GROUPED` (error): a root-scope mapping reads a non-group column without the `first` adapter or an aggregate.
  - `GROUP_ROWS_WITHOUT_GROUP` (error).
  - `GROUP_ORDER_NO_INDEX` (warning, with a `CREATE INDEX ON order_detail(order_id, line_no)` hint).
- Key template: `KEY_MAY_COLLIDE` treats `groupBy` as unique (`order::{order_id}` is clean). A key that uses a non-group column is `KEY_NOT_GROUPED` (error).

### Plan & reader (`internal/plan`, `internal/connectors/source`)
- `ExecutionPlan.Group`: the read orders by `groupBy` columns, then `orderBy`. The transform receives a `Group{Key, Rows}` instead of a row. Root fields evaluate on the group (group columns from the first row, `first` adapter, aggregates over `Rows`), and the group-rows array evaluates per row with the usual per-row program.
- Streaming grouping keeps at most one group in memory. A group larger than `limit` follows `overflow`. With `split` the pages follow the OC-046 split rules.
- Progress counts documents (groups) and rows. `exactCount: always` runs `count(DISTINCT (order_id))`.
- Checkpoint: the last completed group key. Resume restarts at `WHERE (order_id) > :last`.
- Preview (OC-049) shows groups. The worst case picks the largest group (`GROUP BY … ORDER BY count(*) DESC LIMIT 1`).

### Composer UI
- `DrivingPicker`: the table select plus *One document per* (row / distinct columns, multi-select of columns, PK excluded). Turning grouping on when root fields map non-group columns opens a dialog that offers to move them into a new `rows[]` array or keep them with *First row*.
- Sources pane: a *Rows of this group* entry above the related tables, draggable onto arrays and objects. The Bind dialog shows kind *Group rows* with order, limit and overflow.
- The score header and problems panel cover the new codes.

## Acceptance criteria & unit tests

### AC1 — The customer's order document
**Given** `order_detail` rows (1,1,2,3), (1,2,4,2), (2,1,2,1) and the design `{type, orderId, products[]{lineNo, productId, quantity}}` with `groupBy: [order_id]`, `type` ← constant (or *First row* `type`), `orderId` ← `order_id` and `products[]` ← group rows ordered by `line_no`, **then** the migration writes exactly 2 documents, `order::1` with 2 products in line order and `order::2` with 1, to Couchbase and to a directory, identically.

| Test | Location | Verifies |
|---|---|---|
| `TestGroup_AC1_OrderDetail` *(integration)* | `internal/pipeline/group_integration_test.go` | |
| `TestGroup_AC1_DirectoryParity` *(integration)* | same | |

### AC2 — Validation
**Given** grouping on `order_id`, **when** a root field maps `line_no` directly, **then** `ROOT_FIELD_NOT_GROUPED` names the field and the fixes (*First row*, an aggregate, or move it into the array). **Given** a key `od::{order_id}-{line_no}`, **then** `KEY_NOT_GROUPED`. **Given** no index on `(order_id, …)`, **then** `GROUP_ORDER_NO_INDEX` with the `CREATE INDEX` hint.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC2_Grouped` | `internal/mapping/validate_group_test.go` | |

### AC3 — Streaming, bounded memory
**Given** 1,000,000 rows in 100,000 groups, **then** peak memory stays bounded by the largest group plus a batch, and throughput is within 15 % of the same rows as flat documents.

| Test | Location | Verifies |
|---|---|---|
| `BenchmarkGroup_AC3_Stream` | `internal/transform/group_bench_test.go` | |

### AC4 — Resume never splits a document
**Given** a run killed after group `order_id = 5000`, **when** resumed, **then** groups ≤ 5000 aren't re-read, 5001 onward are written once, and no document is partial.

| Test | Location | Verifies |
|---|---|---|
| `TestGroup_AC4_Resume` *(integration)* | `internal/pipeline/group_integration_test.go` | |

### AC5 — Large groups follow the overflow policy
**Given** `limit: 100` and an order with 250 lines, **then** `truncate` keeps 100 with a row warning, `fail` sends the group to the dead-letter queue, and `split` writes 3 linked pages (OC-046).

| Test | Location | Verifies |
|---|---|---|
| `TestGroup_AC5_Overflow` | `internal/transform/group_test.go` | |

### AC6 — Composer flow
**Given** the order_detail mapping in the Composer, **when** the user picks *One document per: distinct order_id* and drops *Rows of this group* on `products[]`, **then** the preview shows order 1 with 2 products and the score header counts groups. Turning grouping on with root fields mapped from non-group columns offers to move them into the array.

| Test | Location | Verifies |
|---|---|---|
| `Composer.test.tsx` › `AC6: group the driving table` | `src/features/composer/Composer.test.tsx` | |
| `composer-group.spec.ts` › `order_detail into one order` | `web/tests/e2e/composer-group.spec.ts` | |

### AC7 — Suggested from an imported design
**Given** the customer's JSON above imported onto the order_detail mapping, **then** *Suggest bindings* proposes *Group by order_id* (high) and `products[]` ← group rows, ordered by `line_no`. *Accept high & auto-map* maps all 5 fields.

| Test | Location | Verifies |
|---|---|---|
| `TestSuggestBindings_AC7_Group` | `internal/automap/bindings_test.go` | |

## Implementation notes (2026-10-01)
- **Model.** `root.groupBy` plus a relation with `group: true`, not a new `group-rows` kind. `mapping.SyncGroup` (run by `Normalize`, mirrored by `composerOps.syncGroup`) makes that relation a one-to-many from the driving table to itself on the group columns, directly under the root. The resolver, scopes, scoped auto-map, preview stats, aggregates, overflow (OC-046) and nested relations (`products[].product`) therefore work unchanged.
- **Reading: two reads, not one streaming pass.** The root read is `SELECT DISTINCT ON (order_id) … ORDER BY order_id, <primary key>`: one row per group, its first line (`source.TableRead.Distinct`/`Within`, `PreviewRequest.DistinctOn`). It pages, partitions, checkpoints, resumes, counts (`count(*)` over `SELECT DISTINCT`) and re-reads failed documents by the group key, so a document is never split across pages or a resume. The group's lines come through the batched / sorted-merge resolver, like any one-to-many. The trade-off is a second, index-backed read of the table; the AC3 "within 15 %" benchmark wasn't written. A grouped read always counts exactly (unless `exactCount: never`), because the row estimate isn't a document count.
- **Root fields** that read a non-group column take the group's first row (by primary key) and get a `ROOT_FIELD_FIRST_ROW` **warning**, not the error the story proposed: `type` (the same on every line) is the common case. A key using a non-group column is `KEY_NOT_GROUPED` (error), and `KEY_MAY_COLLIDE` treats the group columns as unique (`mapping.GroupedTable`). Turning grouping on in the composer rewrites such a key to its prefix plus the group columns (`order_detail::{order_id}::{line_no}` → `order_detail::{order_id}`, `composerOps.groupKey`).
- **Composer.** *One document per* (`GroupPicker`) next to the driving table. A *Rows of this group* source (draggable, or Enter binds the selected node; node menu *Fill with the group's rows*) binds ordered by the rest of the primary key. The source picker labels those columns "Rows of this group (table)". Changing the driving table or going back to one per row unbinds the group's rows. The dialog that offers to move root fields into the array wasn't built; the warning says what to do instead.
- **Import (OC-059 follow-up):** replacing the design with an imported one turns off a type field (e.g. `docType`) the customer's design doesn't have, and the import report says so.
- **Suggestions (AC7):** `automap.SuggestBindings` adds a group candidate for root arrays whose fields match the driving table's own columns. It groups by the root fields' matched key columns (in the primary key, or `*_id`), never the whole primary key, and orders by the rest of the key.
- Tests: `TestGroup_AC1_OrderDetail` (integration, Couchbase and directory parity, nested product), `TestGroupedRead_AC4_Integration` (PostgreSQL: pages, partitions, resume after a group, count, by key), `TestValidate_AC2_Grouped`, `TestGroup_AC5_Overflow`, `TestSuggestBindings_AC7_Group`, `Composer.test.tsx` › `AC6: group the driving table`, `web/tests/e2e/composer-group.spec.ts` (`sales.order_detail` is in the dev seed, `04-order-detail.sql`).
