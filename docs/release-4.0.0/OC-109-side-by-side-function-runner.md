# OC-109 — Side-by-side runner: PL/SQL on the left, UDF on the right

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 8 | OC-108 | Not started |

## User story
As a **developer**, I want to enter arguments once and run the original PL/SQL function on Oracle in the left pane and its Couchbase UDF in the right pane, and see whether the results are the same, so that I can trust the conversion before the application switches over.

## Scope
**In:**
- **Functions area → Run** page (also opened from a unit's Convert page): a single-line toolbar (unit picker, version, **Run both**, Run left, Run right, options menu), an argument form, and two result panes side by side (stacked on narrow screens), with a verdict between them.
- Argument form generated from the signature (`ALL_ARGUMENTS`): typed inputs (number, text, date/time picker, boolean, null toggle per argument, JSON editor for records and collections); defaults pre-filled when the parameter has one; the last 20 argument sets remembered per unit.
- **Oracle pane**: chooses the call form per [§2](00-architecture.md#2-oracle-facts-that-shape-the-design): `SELECT pkg.fn(:1, …) FROM dual` for SQL-callable functions; an anonymous block with binds for `BOOLEAN`/record/collection parameters and `OUT` parameters (results returned as JSON via `JSON_OBJECT` or `DBMS_OUTPUT`, per the spike). Runs in a transaction that is always rolled back ([D4](00-architecture.md#d4--we-never-change-a-customers-database)); units flagged as writing (DML, `COMMIT`, autonomous) are blocked unless the editor ticks "allow writes for this run" (audited).
- **Couchbase pane**: `SELECT RAW scope.fn($1, …)` with `readonly: true`, or `EXECUTE FUNCTION` when the unit was converted with DML (OC-112), only after the same explicit consent.
- Each pane shows: the result rendered as JSON with Oracle-side types shown (`NUMBER`, `DATE`…), the raw value, the error (Oracle `ORA-` code / Couchbase error with `oracleCode`), elapsed time, and the exact statement run (copyable).
- Verdict: **Match**, **Mismatch** (with a field-level diff: OC-110 rules), **Both errored the same**, **Error on one side**. Recorded in `udf_runs`.
- Per-pane timeout (default 30 s, configurable), cancel button; runs execute server-side with the project's stored credentials only (no credentials in the browser).

**Out:** comparison rules beyond strict equality (OC-110), saved test suites (OC-111).

## Acceptance criteria & unit tests

### AC1 — Run both
**Given** a deployed conversion of `net_price(100, 15)`, **when** the user clicks Run both, **then** both panes show `85`, the verdict is Match, and both statements are shown.

### AC2 — Call forms
**Given** a function with a `BOOLEAN` parameter, one with an `OUT` parameter, and a package function with two overloads, **then** each runs on Oracle with the right call form and the right overload, chosen from the signature.

### AC3 — Writes are blocked by default
**Given** a function flagged as writing, **then** Run is disabled with the reason; with "allow writes for this run" ticked, the Oracle run still ends in a rollback (verified by a row count before and after), and an audit event is written.

### AC4 — Errors compared
**Given** a function that raises `NO_DATA_FOUND` for a missing ID, **then** both panes show the error and the verdict is "Both errored the same".

### AC5 — Timeouts
**Given** a function that sleeps 60 s, **then** the Oracle pane times out after 30 s, the session is reset, and the other pane's result is still shown.

| Test | Location | Verifies |
|---|---|---|
| `TestRunner_AC1_RunBoth` | `internal/plsql/compare/runner_integration_test.go` | AC1 |
| `TestRunner_AC2_CallForms` | same | AC2 |
| `TestRunner_AC3_WritesBlocked` | same | AC3 |
| `TestRunner_AC4_ErrorsCompared` | same | AC4 |
| `TestRunner_AC5_Timeout` | same | AC5 |
| `RunPage.test.tsx` | `web/src/features/functions/RunPage.test.tsx` | AC1 (UI) |
| `functions-side-by-side.spec.ts` | `web/tests/e2e/functions-side-by-side.spec.ts` | AC1 end to end |
