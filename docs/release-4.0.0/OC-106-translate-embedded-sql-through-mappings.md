# OC-106 — Translate embedded SQL to SQL++ through the mappings

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 8 | OC-105 | Not started |

## User story
As a **migration architect**, I want the queries inside my PL/SQL functions rewritten to read the migrated documents (the right collection, field paths and embedded arrays), so that a converted function looks up the same data in Couchbase that it looked up in Oracle.

## Scope
**In:** `internal/plsql/sqlmap`, per [architecture D6](00-architecture.md#d6--embedded-sql-is-translated-through-the-mappings):
- A **resolution index** built from the project's mappings (spec v2): table → mapping(s) → keyspace and key template; column → field path (direct mappings only); relation → embedded array or object path; type field. A table with several mappings asks the user to choose one per conversion, and the choice is remembered.
- `SELECT` translation: projection, `WHERE`, `ORDER BY`, `GROUP BY`/`HAVING`, aggregates, `DISTINCT`, `FETCH FIRST`/`ROWNUM <= n`, scalar subqueries, `EXISTS`/`IN` subqueries, `CASE`, built-ins via OC-104.
  - Joins: a join that the mapping embeds becomes `UNNEST` over the embedded array (one-to-many) or a path into the embedded object (many-to-one lookup). A join between two separately mapped tables becomes a SQL++ `JOIN … ON KEYS`/`ON` using the key template when the key is fully determined, else an `ON` predicate.
  - A primary-key equality on the root table becomes a key lookup (`USE KEYS`).
  - The type field filter is added when a collection holds several types.
- Bind variables and PL/SQL variables become named parameters.
- Index advice: for each translated query, the predicates that need a GSI and a suggested `CREATE INDEX` statement (shown, never created automatically; created on request in OC-108).
- Unresolvable references (an unmigrated table, a column fed by an expression, a column dropped by the mapping) become TODO findings naming the table and column; the conversion records `mappingVersions`.
- `SELECT … INTO` keeps Oracle semantics: no row → `NO_DATA_FOUND`, more than one → `TOO_MANY_ROWS` (via `odb_plsql`).

**Out:** DML translation (Manual in 4.0, see OC-112), `CONNECT BY` (finding; candidate for a later recursive translation), analytic functions other than `ROW_NUMBER`/`RANK`/`DENSE_RANK`/`LAG`/`LEAD` and windowed `SUM`/`COUNT`/`AVG` (findings).

## Acceptance criteria & unit tests

### AC1 — Embedded children
**Given** the mapping `ORDERS` → `orders` with `ORDER_ITEMS` embedded as `items[]`, and a function that sums `QTY * PRICE` from `ORDER_ITEMS` for an order, **then** the SQL++ reads `orders` by key and sums over `UNNEST o.items`, and side-by-side results match for all seed orders.

### AC2 — Lookup and separate collections
**Given** `PRODUCTS` mapped separately and embedded into items as a lookup, **then** a query joining `ORDER_ITEMS` to `PRODUCTS` uses the embedded object; a query reading only `PRODUCTS` reads the `products` collection.

### AC3 — Stale on mapping change
**Given** a converted function, **when** the `ORDER_ITEMS` mapping renames `QTY` to `quantity`, **then** the conversion shows **Stale (mapping changed)** and re-converting uses `quantity`.

### AC4 — Unresolvable reference
**Given** a function reading an unmigrated table, **then** the conversion has a TODO naming it, and the unit's rating becomes Assisted.

| Test | Location | Verifies |
|---|---|---|
| `TestSqlmap_AC1_UnnestEmbedded` | `internal/plsql/sqlmap/sqlmap_test.go` + `sqlmap_integration_test.go` | AC1 |
| `TestSqlmap_AC2_LookupAndJoin` | same | AC2 |
| `TestConversions_AC3_StaleOnMappingChange` | `internal/plsql/conversions_test.go` | AC3 |
| `TestSqlmap_AC4_Unresolvable` | `internal/plsql/sqlmap/sqlmap_test.go` | AC4 |
