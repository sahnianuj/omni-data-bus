# OC-046 — Large arrays: sorted-merge strategy, overflow policies & document size guard

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P1 | 8 | OC-043, OC-024 | Not started |

## User story
As a **data engineer** with customers who have hundreds of thousands of orders, I want relations with very many child rows to be read efficiently and handled predictably when they don't fit in one document, so that a migration neither runs out of memory nor silently produces truncated or oversized documents.

## Scope
**In:**
- A second resolution strategy, `sorted-merge`, for high-fanout one-to-many and many-to-many relations at depth 1: stream parent and child ordered by the join key and merge them, instead of batched `IN` lookups.
- `strategy: auto | batched | sorted-merge`, with `auto` chosen from the snapshot estimates.
- Overflow policies when a parent exceeds the limit: `truncate` (1.0 default), `fail`, `split`.
- A final document size guard before writing, with the same policies.

**Out:** sorted-merge for nested relations below depth 1 (they always use `batched`), and parallel partitions for sorted-merge relations (a job using one runs that mapping with a single reader).

## Implementation details

### Strategy selection (`internal/plan`)
- `auto` picks `sorted-merge` when the estimated rows per parent > 200, **or** estimated child rows > 50M, **and** the relation is at depth 1. Otherwise it picks `batched`.
- The resolved strategy is shown in the Embed dialog ("Reads order_items alongside orders, sorted by order_id") and in the job log.

### Sorted-merge (`internal/related/merge.go`)
- The root reader switches to ordered reading by the root's join key (usually the PK, so no extra sort cost).
- A second cursor on the same exported snapshot reads the child: `SELECT … FROM order_items WHERE <filter> ORDER BY order_id, <orderBy>`.
- Both cursors stream, and the merger advances the child cursor while `child.key ≤ parent.key`, grouping rows for the current parent. Memory is O(one parent's children), bounded by `limit + 1` for truncate, or the split page size.
- Correctness depends on both sides sorting identically. Keys are compared with collation-aware ordering from the database: the child query uses `ORDER BY … COLLATE "C"` and so does the root query, so Go compares bytes.
- Checkpointing: the checkpoint records the last fully emitted parent key. On resume, both cursors restart with `WHERE key > $last`.
- An index on the child's join key is strongly recommended. `RELATION_NO_INDEX` (OC-042) is raised to an error for sorted-merge unless the user overrides it.

### Overflow policies (`overflow`)
| Policy | Behavior when a parent has more than `limit` children |
|---|---|
| `truncate` (default) | First `limit` items; row warning `EMBED_TRUNCATED` (1.0 behavior) |
| `fail` | Row error `RELATION_LIMIT_EXCEEDED`, dead-lettered; the document isn't written |
| `split` | Parent holds the first `limit` items, plus `"<field>Overflow": {"count": N, "pages": ["order::1001::items::2", …]}`. Extra pages are written as separate documents `{parentKey}::{field}::{page}` with `{parent, page, items: […]}`, each page up to `limit` items, in the same folder or collection |

`split` keys go through the same key rules (OC-016) and path encoding (OC-039). Page documents count toward the job's written documents, and the report lists them separately.

### Document size guard
After serialization and before the writer, each document is checked against the target's `maxDocBytes` (20 MiB for Couchbase; configurable for directories). When it's too big:
- `truncate`: items are halved from the end of the largest array (repeatedly) until the document fits, with `DOC_SIZE_TRUNCATED` listing the arrays and counts.
- `fail`: `DOC_TOO_LARGE` (1.0 behavior).
- `split`: the largest array is paginated as above.

## Acceptance criteria & unit tests

### AC1 — Auto strategy selection
**Given** estimates of 1M parents and 400M children, **then** `sorted-merge`. **Given** 1M and 3M, **then** `batched`. **Given** a depth-2 relation with high fanout, **then** `batched`, with an info message.

| Test | Location | Verifies |
|---|---|---|
| `TestCompile_AC1_StrategyAuto` | `internal/plan/strategy_test.go` | |

### AC2 — Sorted-merge correctness
**Given** parents 1..10,000 and children with skewed fanout (0 to 5,000 per parent, some parents with none), **then** merged output equals the `batched` output for every parent, and peak memory stays below `limit + 1` child rows.

| Test | Location | Verifies |
|---|---|---|
| `TestMerge_AC2_EqualsBatched` | `internal/related/merge_test.go` | Property test over random fanouts |
| `TestMerge_AC2_BoundedMemory` | same | Counts retained rows |
| `TestMerge_AC2_TextKeysCollation` *(integration)* | `internal/related/merge_integration_test.go` | Mixed-case text keys under a non-C default collation |

### AC3 — Resume mid-merge
**Given** a sorted-merge job killed after parent 5,000, **when** resumed, **then** documents 5,001..10,000 are written, and 1..5,000 are not re-read.

| Test | Location | Verifies |
|---|---|---|
| `TestMerge_AC3_ResumeFromKey` *(integration)* | `internal/pipeline/merge_resume_integration_test.go` | |

### AC4 — Overflow policies
**Given** 2,500 children and limit 1,000, **then** `truncate` gives 1,000 items plus a warning; `fail` dead-letters the row; `split` writes the parent (1,000 items, overflow `count: 2500`, 2 pages) plus 2 page documents with 1,000 and 500 items.

| Test | Location | Verifies |
|---|---|---|
| `TestOverflow_AC4_Policies` | `internal/transform/overflow_test.go` | |
| `TestOverflow_AC4_SplitKeysAndPaths` | same | Keys and directory paths |

### AC5 — Size guard
**Given** a 25 MiB document and a 20 MiB limit, **then** `truncate` reduces the largest array until it fits (with `DOC_SIZE_TRUNCATED`), `fail` gives `DOC_TOO_LARGE`, and `split` paginates.

| Test | Location | Verifies |
|---|---|---|
| `TestSizeGuard_AC5_Policies` | `internal/pipeline/sizeguard_test.go` | |

### AC6 — Throughput
**Given** 100k parents with 500 children each (50M rows), **then** sorted-merge sustains ≥ 1.5× the docs/s of batched on the benchmark fixture, with RSS < 768 MB.

| Test | Location | Verifies |
|---|---|---|
| `BenchmarkRelations_SortedMergeVsBatched` | `bench/relations_bench_test.go` | Gate enforced in OC-056 |
