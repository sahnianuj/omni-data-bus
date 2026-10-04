# OC-098 — Automap for Oracle names and types

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 3 | OC-096 | Not started |

## User story
As a **data engineer**, I want automap to turn `CUSTOMER_ID` into `customerId` and recognize Oracle's usual stand-ins for booleans and integers, so that an Oracle mapping starts as good as a PostgreSQL one.

## Scope
**In:**
- Name normalization: upper-case and quoted mixed-case identifiers produce the same camelCase field names as the equivalent PostgreSQL lower-case names (`CUSTOMER_ID`, `"CustomerId"` and `customer_id` → `customerId`). Default key templates use the real column names (`CUSTOMERS::{CUSTOMER_ID}`), and the type field value is the lower-cased table name, as for PostgreSQL ([target parity rule](../release-2.0.0/00-architecture.md)).
- Type suggestions (state `pending`, reason shown, user accepts or rejects):
  - `NUMBER(1)` or `CHAR(1)` with a check constraint `IN (0,1)`, `IN ('Y','N')` or `IN ('T','F')` → boolean with a `lookup` transform.
  - `NUMBER` without precision whose sampled values are all integers, and every primary or foreign key `NUMBER` column → integer.
  - `DATE` columns whose sampled values all have a zero time → `dateAs: date`.
- Automap scores (OC-018) treat these the same way as PostgreSQL suggestions.

**Out:** composer behavior changes (none needed).

## Acceptance criteria & unit tests

### AC1 — Same field names as PostgreSQL
**Given** the Oracle and PostgreSQL seeds, **when** automap runs on `CUSTOMERS`/`customers`, **then** the generated document schemas are identical apart from type suggestions.

### AC2 — Boolean and integer suggestions
**Given** `ACTIVE CHAR(1) CHECK (ACTIVE IN ('Y','N'))` and `ORDER_ID NUMBER` (primary key), **then** automap suggests boolean with `lookup {Y: true, N: false}` and integer, each with its reason.

| Test | Location | Verifies |
|---|---|---|
| `TestAutomap_AC1_OracleNamesMatchPostgres` | `internal/automap/oracle_test.go` | AC1 |
| `TestAutomap_AC2_OracleTypeSuggestions` | same | AC2 |
