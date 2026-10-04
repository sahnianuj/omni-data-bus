# OC-112 — Convert procedures and package state

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P1 | 5 | OC-105, OC-106 | Not started |

## User story
As a **migration architect**, I want stored procedures (including ones with `OUT` parameters and simple DML on migrated tables) converted too, so that more of my PL/SQL moves without manual rewrites.

## Scope
**In:**
- Procedures → JavaScript UDFs called with `EXECUTE FUNCTION`; `OUT`/`IN OUT` parameters are returned as one object `{param: value}` (the side-by-side runner compares them field by field).
- DML translation through the mappings (OC-106 index), for the cases where it is exact:
  - `UPDATE`/`DELETE` of root-table rows by key → `UPDATE`/`DELETE … USE KEYS`.
  - `INSERT` into a root table → `INSERT` with the key template and the mapping's transforms (only when every mapped field is a direct column).
  - `UPDATE` of an embedded child row → an `UPDATE … SET items[i]` on the parent, by key.
  - Anything else (multi-table, triggers relied on, sequences beyond `NEXTVAL` via a counter document) stays a Manual finding.
- `COMMIT`/`ROLLBACK`: SQL++ transactions (`BEGIN WORK`… `COMMIT`) when the target supports them and the unit's writes span documents; otherwise a finding. `SAVEPOINT` maps to SQL++ savepoints.
- Package state: package-level constants become library constants; package variables that are written are a Manual finding (UDFs are stateless), with the explanation in the hint.
- The side-by-side runner allows these units only with "allow writes for this run" and runs the Couchbase side inside a transaction rolled back at the end (when supported), so verification doesn't change the target.

**Out:** triggers (Eventing), pipelined functions.

## Acceptance criteria & unit tests

### AC1 — OUT parameters
**Given** `PROCEDURE split_name(p_full IN VARCHAR2, p_first OUT VARCHAR2, p_last OUT VARCHAR2)`, **then** the UDF returns `{p_first, p_last}` and side-by-side matches for the seed names.

### AC2 — Keyed update
**Given** a procedure that updates `CUSTOMERS.STATUS` by `CUSTOMER_ID`, **then** the UDF updates the customer document's mapped field by key, and a verification run leaves the target unchanged.

### AC3 — Package variable written
**Given** a package with a counter variable incremented by a function, **then** the unit is Manual with finding `PACKAGE_STATE_WRITTEN`.

| Test | Location | Verifies |
|---|---|---|
| `TestTranslate_AC1_OutParams` | `internal/plsql/translate/procedures_integration_test.go` | AC1 |
| `TestSqlmap_AC2_KeyedUpdate` | `internal/plsql/sqlmap/dml_integration_test.go` | AC2 |
| `TestAssess_AC3_PackageState` | `internal/plsql/assess/assess_test.go` | AC3 |
