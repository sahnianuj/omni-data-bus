# OC-111 — Test cases and batch verification

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 5 | OC-110 | Not started |

## User story
As a **migration lead**, I want each converted function verified against a set of test cases, all at once, with a report of what matches, so that I can sign off the PL/SQL migration with evidence.

## Scope
**In:**
- Test cases per unit (`udf_tests`): save from a run ("Save as test case"), add by hand, import/export JSON.
- **Generate test cases**: boundary values from the signature (null, `''`, 0, −1, max precision, month ends, 29 February, time zone edges) plus real values sampled from the source, by finding the columns compared with each parameter in the unit's SQL (from the AST) and sampling distinct values, including values that hit `NO_DATA_FOUND`. Generated cases are proposals until saved. Default: 20 per unit.
- **Run all**: one unit, a selection, or every deployed unit; executed as a background job (reusing the jobs worker, no active-job lock with migrations); live progress; per-case verdicts in `udf_runs`.
- **Verification matrix**: units × verdict counts, filters (mismatches only), drill-down into the side-by-side view of any case.
- Unit state **Verified** when every saved case matches (with the options recorded); the Convert list and the assessment report show verified counts. A new conversion version clears Verified.
- **Equivalence report** export (Markdown/PDF via print CSS): per unit, the cases, verdicts, options used, the data alignment (SCN), and the conversion version deployed.

**Out:** AI-driven repair (OC-113).

## Acceptance criteria & unit tests

### AC1 — Generated cases
**Given** `get_order_total(p_order_id NUMBER)`, **then** generated cases include null, a non-existent ID, and at least 10 IDs sampled from `ORDERS`, each labeled with its origin.

### AC2 — Batch run
**Given** 30 deployed units with 20 cases each, **when** Run all starts, **then** progress streams, all 600 verdicts are recorded, and the matrix totals equal the recorded verdicts.

### AC3 — Verified state
**Given** a Verified unit, **when** a new conversion version is saved, **then** it is no longer Verified until its cases are run again.

| Test | Location | Verifies |
|---|---|---|
| `TestGenerate_AC1` | `internal/plsql/compare/generate_integration_test.go` | AC1 |
| `TestBatch_AC2` | `internal/plsql/compare/batch_integration_test.go` | AC2 |
| `TestVerified_AC3` | `internal/plsql/conversions_test.go` | AC3 |
| `VerificationMatrix.test.tsx` | `web/src/features/functions/VerificationMatrix.test.tsx` | AC2 (UI) |
