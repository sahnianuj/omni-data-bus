# OC-105 — Translate PL/SQL functions to Couchbase UDFs

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 8 | OC-103, OC-104 | Not started |

## User story
As a **migration architect**, I want the tool to convert my PL/SQL functions into Couchbase UDFs automatically, choosing inline SQL++ where it can and JavaScript where it must, so that most of my business logic moves without being rewritten by hand.

## Scope
**In:** `internal/plsql/translate`, from the OC-103 AST:
- **Target choice** per [architecture D5](00-architecture.md#d5--two-udf-targets-picked-per-function), overridable per unit.
- **Inline SQL++**: expression bodies, `CASE`, a single `SELECT INTO` as a subquery (with OC-106), parameters as `$name` references.
- **JavaScript**:
  - Declarations: scalar variables, constants, `%TYPE`/`%ROWTYPE` (as plain objects), records, associative arrays (`Map`), nested tables and varrays (1-based wrapper from `odb_plsql`).
  - Statements: assignment, `IF`/`ELSIF`, `CASE`, `LOOP`/`WHILE`/numeric `FOR` (including `REVERSE`), `EXIT`/`CONTINUE WHEN`, `GOTO` (finding, Manual), `NULL;`, `RETURN`.
  - Exceptions: `EXCEPTION WHEN … THEN` blocks as `try/catch` matching `oracleCode`, `RAISE`, user-declared exceptions, `PRAGMA EXCEPTION_INIT`, `SQLCODE`/`SQLERRM`.
  - Calls to other converted units in the same project (as library imports, cycle-safe), and to `odb_plsql` for built-ins.
  - Cursors: `FOR rec IN (SELECT …)`, explicit cursors (`OPEN`/`FETCH`/`CLOSE`, `%FOUND`/`%NOTFOUND`/`%ROWCOUNT`), `BULK COLLECT INTO` (via OC-106 SQL and the SQL++ iterator).
- Output per unit: the `CREATE OR REPLACE FUNCTION` statement, the JS library source (if any), a source map (PL/SQL line → generated line), and TODO findings placed as comments at the right line. Stored as a new `conversions` version with `origin: rules`.
- Package functions become `<package>_<function>` UDFs (overloads suffixed `_<n>` with the signature in the comment); naming rules in one place and checked for collisions.
- **Convert** action on one unit, a selection, or all **Automatic** units.

**Out:** embedded SQL translation (OC-106), procedures and package state (OC-112).

## Acceptance criteria & unit tests

### AC1 — Golden conversions
**Given** one corpus unit per construct above, **then** the generated SQL++/JS equals the golden file, and the generated JS passes a syntax check.

### AC2 — Automatic units run and match
**Given** every **Automatic** unit in the seed corpus that has no SQL, **when** deployed and run side by side (OC-109 service, no UI) with its seeded test arguments, **then** every result matches Oracle's.

### AC3 — Inline when possible
**Given** `FUNCTION net_price(p NUMBER, d NUMBER) RETURN NUMBER IS BEGIN RETURN ROUND(p * (1 - d/100), 2); END;`, **then** the conversion is an inline UDF.

### AC4 — TODOs are honest
**Given** a unit with one unsupported `TO_CHAR` format model, **then** the conversion has exactly one TODO at that line, and the unit is not marked converted until the TODO is resolved or explicitly accepted.

| Test | Location | Verifies |
|---|---|---|
| `TestTranslate_AC1_Golden` | `internal/plsql/translate/translate_test.go` | AC1 |
| `TestTranslate_AC2_RunMatches` | `internal/plsql/translate/translate_integration_test.go` | AC2 |
| `TestTranslate_AC3_InlineChoice` | `internal/plsql/translate/target_test.go` | AC3 |
| `TestTranslate_AC4_Todos` | `internal/plsql/translate/translate_test.go` | AC4 |
