# OC-100 — Oracle related-row reads: embeds, many-to-many, filters, sorted-merge

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 8 | OC-099 | Not started |

## User story
As a **data engineer**, I want every relationship feature I use with PostgreSQL (embedded child rows, multi-level nesting, many-to-many arrays, filters, aggregates, grouping and large-array sorted-merge) to work with Oracle, so that I can build the same documents from an Oracle schema.

## Scope
**In:** `RelatedReader`, `RelatedCounter` and `RelatedStreamer` for Oracle, all `AS OF SCN` the session's SCN:
- Batch lookups by join keys: one query per batch. Single-column keys bind a collection (`TABLE(:keys)` via a SQL type, or chunks of up to 1,000 values in `IN` lists if the spike shows collection binds are unavailable in go-ora); multi-column keys use a `WITH keys AS (… UNION ALL …)` join.
- Per-key limits (`ROW_NUMBER() OVER (PARTITION BY join ORDER BY …)`), filters with dialect-typed binds (OC-093), many-to-many through junctions with left joins (dangling counts), counts and top key (composer preview).
- Sorted-merge ranges (`Range`) in byte order; `Dialect.ByteOrderedKey` returns false for `NVARCHAR2`/`NCHAR` (UTF-16 order differs from UTF-8 byte order for supplementary characters), so the planner chooses batched reads for them.
- Grouped driving tables (OC-063) with `Distinct`/`Within`.

**Out:** anything not already supported for PostgreSQL.

## Acceptance criteria & unit tests

### AC1 — Same documents as PostgreSQL
**Given** the 2.0 golden mappings re-pointed at the Oracle seed (same data), **when** they run, **then** the documents are identical to the PostgreSQL run, after the key and type-name differences documented in the golden files (upper-case column names in keys).

### AC2 — Sorted-merge
**Given** the skewed fan-out benchmark dataset loaded into Oracle, **when** the plan picks sorted-merge, **then** array order matches the PostgreSQL run and memory stays under `OC_RELATION_MEMORY_MB`.

### AC3 — NVARCHAR2 keys avoid sorted-merge
**Given** a relation joined on an `NVARCHAR2` column, **then** the plan uses batched reads and the plan explanation says why.

| Test | Location | Verifies |
|---|---|---|
| `TestParity_AC1_OracleGoldenMappings` | `internal/pipeline/oracle_parity_integration_test.go` | AC1 |
| `TestRelated_AC2_SortedMerge` | `internal/connectors/source/oracle/related_integration_test.go` | AC2 |
| `TestStrategy_AC3_NVarcharNotByteOrdered` | `internal/plan/strategy_test.go` | AC3 |
