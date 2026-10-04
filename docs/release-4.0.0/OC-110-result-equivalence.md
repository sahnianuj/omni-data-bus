# OC-110 — Result equivalence: normalization, tolerances, same-data alignment

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 5 | OC-109 | Not started |

## User story
As a **developer**, I want "same result" to mean the same value once Oracle and JSON representations are lined up, and both sides to read the same data, so that a Mismatch always points at a real difference rather than at formatting or at rows that changed after the migration.

## Scope
**In:**
- **Canonical form**: both results are turned into canonical JSON using the migration's own encoding rules (OC-096: `NUMBER`, `DATE`, timestamps, `''`-is-null), so `DATE '2026-03-01'` on the left and `"2026-03-01T00:00:00"` on the right compare equal.
- **Compare options** (per run, per test case or per unit default): numeric tolerance (absolute/relative, default exact), timestamp precision (default seconds), treat `null`, missing and `""` as equal (default on, shown as a chip because it hides a class of differences), array order significant (default on), ignore fields (paths), case-insensitive strings (default off). Each option shows in the verdict when it was needed ("matched with tolerance 0.005").
- **Same data**: the Oracle pane can run **as of the migrated data**: `DBMS_FLASHBACK.ENABLE_AT_SYSTEM_CHANGE_NUMBER(scn)` with the SCN of the last completed job that wrote the tables the unit depends on (OC-099 stores it), so both panes see the same rows. Shown as "Oracle as of the migration on <date> (SCN n)" with a switch to "live data". Warns when the SCN is older than undo retention and falls back to live with a notice. **(CDC)** With continuous replication (6.0), the Oracle pane can align with the replication's applied SCN instead.
- Diff view: field-level diff of the canonical forms, with the raw values on hover.

**Out:** batch runs (OC-111).

## Acceptance criteria & unit tests

### AC1 — Canonical equality
**Given** pairs that differ only in representation (`NUMBER 1.50` vs `1.5`, `DATE` vs ISO string, `NULL` vs missing, `TIMESTAMP WITH TIME ZONE` vs UTC string), **then** each compares equal with default options.

### AC2 — Tolerance is explicit
**Given** `0.1 + 0.2` computed in binary float on one side, **then** the default verdict is Mismatch; with relative tolerance `1e-12` it is Match and the verdict names the tolerance.

### AC3 — As of the migration
**Given** a migration completed at SCN n, then an Oracle update to a row the function reads, **then** "as of the migration" matches the UDF, and "live data" shows a Mismatch.

| Test | Location | Verifies |
|---|---|---|
| `TestCanonical_AC1` | `internal/plsql/compare/canonical_test.go` | AC1 |
| `TestCompare_AC2_Tolerance` | `internal/plsql/compare/compare_test.go` | AC2 |
| `TestRunner_AC3_AsOfMigration` | `internal/plsql/compare/runner_integration_test.go` | AC3 |
