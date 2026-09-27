# OC-021 — Embed related tables (denormalization into nested objects & arrays)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E4 Design & Map | P1 | 8 | OC-019, OC-020, OC-024 | Implemented |

## User story
As a **data engineer**, I want to embed rows from related tables into the parent document, for example a customer's `address` as a nested object and their `orders` as an array, so that my Couchbase model is document-oriented instead of a 1:1 copy of normalized tables.

## Scope
**In:**
- **Many-to-one** (FK on the parent → referenced row) embeds as a nested **object**, e.g., `customer.address_id → address`.
- **One-to-many** (child rows referencing the parent) embeds as an **array** of objects, e.g., `orders WHERE orders.customer_id = customer.customer_id`, with an optional order-by and limit.
- One level of nesting in 1.0 (embeds inside embeds are post-1.0).

**Out:** many-to-many through join tables (post-1.0), and filtered embeds with arbitrary SQL.

## Implementation details

### Spec extension (`mapping.Spec.embeds`)
```json
"embeds": [{
  "id": "emb_1", "kind": "many-to-one",
  "table": {"schema": "public", "name": "address"},
  "join": [{"parent": "address_id", "child": "address_id"}],
  "targetFieldId": "fld_addr"                    // object field in docSchema
}, {
  "id": "emb_2", "kind": "one-to-many",
  "table": {"schema": "public", "name": "orders"},
  "join": [{"parent": "customer_id", "child": "customer_id"}],
  "orderBy": [{"column": "order_date", "desc": true}],
  "limit": 1000,                                   // guard against huge arrays; overflow → row warning
  "targetFieldId": "fld_orders"                    // array field; items is an object
}]
```
Field mappings inside an embed reference `sources: [{embed:"emb_2", column:"total"}]`.

### Discovery-driven setup (UI)
- In the workbench's source panel, a **Related tables** section lists FK parents ("↗ address via address_id") and children ("↙ orders via customer_id, ~1.2M rows") from the snapshot.
- Dragging a related table onto the target panel (or clicking *Embed*) opens the **Embed dialog**:
  - kind (auto-detected from the FK direction),
  - target field name (a new object or array field is created in the design),
  - order-by and limit (one-to-many),
  - a checkbox *Generate fields from table*, which fills the item schema via OC-015 generate and **auto-maps** the embed's columns to it with OC-017, scoped to that sub-tree.
- Embedded tables render as additional collapsible source groups in the canvas, and their edges go into the object or array sub-tree.
- A warning badge appears when the child table's estimated rows per parent exceed the `limit`, or when the average embedded array size would put documents near 20 MB (estimated from the table's average row width, `pg_stats`/`sizeBytes ÷ rows`).

### Pipeline execution (extends OC-024 / OC-025)
- **Batch lookup**, never N+1: after the reader yields a parent batch, the `EmbedResolver` collects the distinct join keys and runs, per embed:
  - many-to-one: `SELECT <cols> FROM child WHERE (k1,k2) IN (SELECT * FROM unnest($1::t1[], $2::t2[]))`. Single-column keys use `= ANY($1)`.
  - one-to-many: the same, plus `ORDER BY <join cols>, <orderBy>`, grouped in Go. The limit per parent is applied in Go, or with `ROW_NUMBER() OVER (PARTITION BY …)` when the limit is small (≤ 100), to reduce transfer.
- The lookups run **in the same snapshot** as the parent reader (a shared exported snapshot, OC-024), so embedded data is consistent with the parents.
- Results are attached to the row context as `embeds[emb_id] → []row`, and the transform program builds the object or array items with the embed's compiled sub-program.
- Missing many-to-one (dangling FK or null key) → the embed field is null or omitted per null handling. An empty one-to-many → `[]` (configurable: omit).
- Throughput guard: embed lookups for a batch run concurrently (bounded by `embedConcurrency`, default 4) while the next parent batch is being read.

### Validation
- The join columns exist and have compatible types.
- The target field kind matches (object for m2o, array of object for o2m).
- No duplicate embeds for the same target field.

## Acceptance criteria & unit tests

### AC1 — Detect embed candidates from FKs
**Given** `customer` with FK `address_id → address` and referenced by `orders.customer_id`, **then** Related tables lists `address` as many-to-one and `orders` as one-to-many, with the join columns.

| Test | Location | Verifies |
|---|---|---|
| `TestEmbedCandidates_AC1_ParentsAndChildren` | `internal/mapping/embeds_test.go` | Snapshot fixture |
| `TestEmbedCandidates_AC1_CompositeFK` | same | |
| `RelatedTables.test.tsx` › `AC1: lists parents and children with counts` | `src/features/mapping/RelatedTables.test.tsx` | |

### AC2 — Create embed with generated & auto-mapped item schema
**Given** the user embeds `orders` as `orders[]` with *Generate fields*, **then** an array-of-object field is added, the item schema matches the orders columns (join column excluded by default), the embed's columns are auto-mapped inside that sub-tree only, and the score header counts the embed's columns in a separate "embedded" group. **Given** the user embeds `customer` into `orders` as an object (many-to-one), **then** the object includes the customer's primary key (the join column) mapped from the embedded row, the root's mapping of the foreign key `customer_id` is removed (its field stays, unmapped), and the score doesn't count that unmapped foreign key.

| Test | Location | Verifies |
|---|---|---|
| `TestAddEmbed_AC2_GeneratesItemsAndAutomapsScoped` | `internal/automap/embeds_test.go` | `AddEmbed` lives in `automap` (mapping can't import automap) |
| `EmbedDialog.test.tsx` › `AC2: creates embed and field` | `src/features/mapping/EmbedDialog.test.tsx` | |

### AC3 — Batched lookup SQL
**Given** a parent batch of 1,000 rows with 800 distinct `customer_id`s, **then** exactly one query per embed is issued, with an array parameter of 800 values and correctly quoted identifiers. **Given** a composite join, **then** the `unnest` form is used.

| Test | Location | Verifies |
|---|---|---|
| `TestEmbedSQL_AC3_SingleKeyAny` | `internal/connectors/source/postgres/embedsql_test.go` | The SQL builder belongs to the connector |
| `TestEmbedSQL_AC3_CompositeUnnest` | same | |
| `TestEmbedResolver_AC5_LookupsShareTheSnapshot` | same | Statements recorded on the lookup connection |
| `TestEmbedResolver_AC3_OneQueryPerEmbedPerBatch` | `internal/pipeline/embeds_test.go` | Fake querier counts calls |

### AC4 — Assembly semantics
**Given** a parent with 3 orders, **then** `orders` is an array of 3 objects ordered by `order_date desc`. **Given** 0 orders, **then** `[]` (or omitted when configured). **Given** 1,500 orders and limit 1000, **then** 1000 items and the row warning `EMBED_TRUNCATED`. **Given** a null `address_id`, **then** `address` is null or omitted per null handling.

| Test | Location | Verifies |
|---|---|---|
| `TestApplyEmbeds_AC4_OneToManyOrdered` | `internal/transform/embeds_test.go` | |
| `TestApplyEmbeds_AC4_EmptyArrayOrOmit` | same | |
| `TestApplyEmbeds_AC4_LimitTruncationWarning` | same | |
| `TestApplyEmbeds_AC4_ManyToOneNull` | same | |

### AC5 — Snapshot consistency
**Given** the pipeline runs with an exported snapshot, **then** the embed queries run in transactions that `SET TRANSACTION SNAPSHOT` with the same snapshot ID.

| Test | Location | Verifies |
|---|---|---|
| `TestEmbedResolver_AC5_UsesSharedSnapshot` | `internal/pipeline/embeds_test.go` | Fake tx records the statements |
| `TestEmbeds_AC5_Integration` *(integration)* | `internal/pipeline/embeds_integration_test.go` | Concurrent insert into orders during the run is not visible |

### AC6 — Size and cardinality warnings
**Given** the estimated average rows per parent (child rowEstimate ÷ parent rowEstimate) > limit, or an estimated doc size > 10 MB, **then** the embed shows a warning with numbers, and validation (OC-022) includes a warning, not an error.

| Test | Location | Verifies |
|---|---|---|
| `TestEmbedEstimates_AC6_Warnings` | `internal/mapping/embeds_test.go` | |

### AC7 — Validation
**Given** a join column missing from the child, a type mismatch (int4 vs text), or a target field of the wrong kind, **then** validation errors reference the embed ID and field.

| Test | Location | Verifies |
|---|---|---|
| `TestSpecValidate_AC7_EmbedErrors` | `internal/mapping/validate_test.go` | |

## Implementation notes
- Batched lookups live in `internal/related` (used by both the pipeline and the mapping preview, which can't import each other).
- The resolver is a pipeline stage between reading and transforming (`pipeline.ResolveEmbeds`, 2 batches at once, 4 lookups per batch); lookups use up to 4 connections per read session, each joined to the exported snapshot.
- One-to-many limits ≤ 100 are applied in SQL with `row_number()`; larger ones in Go. `limit + 1` rows are kept so truncation raises `EMBED_TRUNCATED`.
- UI: embedded columns appear as groups in the canvas source panel (keyed `embedId:column`); arrays of objects expand in the target panel; the × on a group removes the embed, its field and its mappings.
- Many-to-one embeds move the key into the object: `automap.AddEmbed` always generates and maps the referenced primary key inside the object and drops the root's single-column mappings of the foreign key (expressions combining it stay). The score (`mapping.ComputeStatsWith` / `computeStats.ts`) skips an unmapped foreign key of a many-to-one embed, and counts an embed's join column once it is mapped.
