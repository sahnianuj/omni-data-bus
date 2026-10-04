# OC-103 — PL/SQL parser and convertibility assessment

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 8 | OC-102 | Not started |

## User story
As a **migration architect**, I want each PL/SQL unit rated for how much of it the tool can convert, and why, so that I can plan the effort and tell my customer what will be automatic and what needs a person.

## Scope
**In:**
- Parser: the ANTLR4 PL/SQL grammar (`grammars-v4/sql/plsql`) generated for Go into `internal/plsql/parser`, wrapped in an AST of our own (units, declarations, statements, expressions, SQL statements). Grammar version pinned; generation reproducible with `make gen`.
- Assessment rules, each with a code, a weight and a fix hint. Ratings:

| Rating | Meaning | Examples of findings that cause it |
|---|---|---|
| **Automatic** | Every construct has a rule; no TODOs expected | Expressions, `IF`/`CASE`/loops, local variables, records, `SELECT INTO` on mapped tables, supported built-ins |
| **Assisted** | Converts with TODOs a person should resolve | `EXECUTE IMMEDIATE` with a literal string, `BULK COLLECT`, explicit cursors, collections of records, unsupported but rare built-ins, a table that's mapped through an expression |
| **Manual** | Converted code would be misleading; a person writes it | DML (`INSERT`/`UPDATE`/`DELETE`/`MERGE`) on migrated tables, `COMMIT`/`ROLLBACK`, autonomous transactions, package global state, `REF CURSOR` returns, pipelined table functions, `DBMS_*`/`UTL_*` without an equivalent, dynamic SQL built at run time |
| **Unsupported** | Can't be converted | Wrapped source, database links, Java/C external procedures, invalid units |

- `plsql_assessments` per unit; a project-level **assessment report** (counts and effort points by rating, top blocking findings, per-schema breakdown) on the Functions area overview and exportable as Markdown/CSV.
- Findings are line-anchored, so the source viewer highlights them.

**Out:** generating code (OC-104 onwards).

## Acceptance criteria & unit tests

### AC1 — Parser coverage
**Given** the seed corpus and a 300-unit public PL/SQL test corpus checked into `testdata/plsql/`, **then** at least 98% of units parse, and each failure is reported as an `Unsupported` finding `PARSE_FAILED` with the line, never a crash.

### AC2 — Ratings
**Given** one corpus unit per rule, **then** each gets the expected rating and finding code (golden file).

### AC3 — Report
**Given** the seed corpus, **then** the report's totals equal the sum of unit ratings, and the Markdown export renders the same numbers.

### AC4 — Speed
**Given** 1,000 units, **then** assessment completes in under 2 minutes on the reference environment.

| Test | Location | Verifies |
|---|---|---|
| `TestParse_AC1_Corpus` | `internal/plsql/parser/parse_test.go` | AC1 |
| `TestAssess_AC2_Golden` | `internal/plsql/assess/assess_test.go` | AC2 |
| `TestReport_AC3_Totals` | `internal/plsql/assess/report_test.go` | AC3 |
| `BenchmarkAssess_AC4` | `internal/plsql/assess/assess_bench_test.go` | AC4 |
