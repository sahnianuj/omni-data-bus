# OC-104 — Oracle built-ins and semantics runtime for UDFs (`odb_plsql`)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 8 | OC-092, OC-103 | Not started |

## User story
As a **migration architect**, I want converted functions to behave like Oracle (`NVL`, `DECODE`, 1-based `SUBSTR`, `''` as `NULL`, `TO_CHAR` date formats, exact decimal arithmetic), so that the UDF gives the same answer as the PL/SQL it replaces.

## Scope
**In:**
- A JavaScript library `odb_plsql` ([architecture D5](00-architecture.md#d5--two-udf-targets-picked-per-function)), versioned and deployed once per scope (OC-108), with unit tests run in Node (same engine family as the Couchbase evaluator) and on a real Couchbase in integration:
  - Null semantics: `''` → null for strings, null propagation in arithmetic and concatenation as in Oracle (`'a' || NULL = 'a'`).
  - Strings: `SUBSTR`, `INSTR` (with occurrence and negative start), `LENGTH`, `UPPER`/`LOWER`/`INITCAP`, `TRIM`/`LTRIM`/`RTRIM` with sets, `LPAD`/`RPAD`, `REPLACE`, `TRANSLATE`, `REGEXP_LIKE`/`REGEXP_SUBSTR`/`REGEXP_REPLACE`/`REGEXP_INSTR`/`REGEXP_COUNT` (POSIX classes mapped; unsupported flags → finding).
  - Numbers: decimal arithmetic (a small decimal implementation or vendored library, per the spike), `ROUND`/`TRUNC` (number and date), `MOD`, `REMAINDER`, `CEIL`, `FLOOR`, `ABS`, `SIGN`, `POWER`, `SQRT`, `GREATEST`, `LEAST`.
  - Dates: `SYSDATE`/`SYSTIMESTAMP` (UTC unless a session time zone is configured), `ADD_MONTHS`, `MONTHS_BETWEEN`, `LAST_DAY`, `NEXT_DAY`, `EXTRACT`, date ± number (days), date − date, `TO_CHAR`/`TO_DATE`/`TO_TIMESTAMP` with the common format models (`YYYY MM DD HH24 MI SS FF MON MONTH DY D J Q WW IW`, `FM`, quoted text); unsupported models → finding.
  - Conversion: `TO_NUMBER` (with format), `TO_CHAR(number, fmt)` for `9 0 . , G D $ S MI`, `CAST`, `NVL`, `NVL2`, `COALESCE`, `NULLIF`, `DECODE`.
  - Errors: `NO_DATA_FOUND`, `TOO_MANY_ROWS`, `ZERO_DIVIDE`, `VALUE_ERROR`, `INVALID_NUMBER`, `DUP_VAL_ON_INDEX` and `RAISE_APPLICATION_ERROR(-20xxx, msg)` as JS errors carrying `oracleCode` and message, so the comparison (OC-110) can match error to error.
- The SQL++ mapping table for inline UDFs: which built-ins have an **exact** SQL++ equivalent (for example `UPPER` → `UPPER`) and which need care (`SUBSTR` → `SUBSTR(s, start-1, len)` with the negative-start case, `NVL` → `IFNULL` only when `''` can't occur). A built-in without an exact inline form forces the JavaScript target.

**Out:** statement translation (OC-105, OC-106).

## Acceptance criteria & unit tests

### AC1 — Oracle-equivalent results
**Given** the built-in test matrix (≥ 600 cases: function, args, Oracle result captured from the Oracle Free container and committed as `testdata/builtins.oracle.json`), **then** `odb_plsql` returns the same result or the same Oracle error code for every case.

### AC2 — Inline mapping is exact
**Given** each built-in marked "exact inline", **when** its SQL++ form runs on Couchbase with the matrix arguments, **then** results equal the Oracle results.

### AC3 — Decimal arithmetic
**Given** `0.1 + 0.2`, `ROUND(2.675, 2)` and `1/3 * 3`, **then** results equal Oracle's (`0.3`, `2.68`, `1`).

| Test | Location | Verifies |
|---|---|---|
| `builtins.test.mjs` (Node) | `backend/internal/plsql/runtime/odb_plsql/builtins.test.mjs` | AC1, AC3 |
| `TestRuntime_AC1_OnCouchbase` | `internal/plsql/runtime/runtime_integration_test.go` | AC1 |
| `TestInline_AC2_Exact` | `internal/plsql/translate/inline_integration_test.go` | AC2 |
| `TestCaptureOracleResults` (manual regeneration) | `internal/plsql/runtime/capture_integration_test.go` | AC1 golden file |
