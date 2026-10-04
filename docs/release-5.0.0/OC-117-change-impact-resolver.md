# OC-117 — Change impact: from a changed row to the documents it feeds

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E16 Continuous Replication | P0 | 8 | OC-115 | Not started |

## User story
As a **data engineer**, I want every source row change mapped to exactly the documents it affects, including embedded children, looked-up rows and grouped documents, so that my denormalized documents stay correct and not just my flat ones.

## Scope
**In:** `internal/replication/impact`, built from a mapping's spec v2 relation tree and `ExecutionPlan`:
- An **impact plan** per mapping: for every table the mapping reads, how to get from a changed row to root keys, per [architecture §2](00-architecture.md#2-why-this-is-harder-than-it-looks-documents-are-not-rows):
  - Root table: the key from the change (old and new when key columns change).
  - Child at depth 1: the parent key from the join columns in the change (old and new, so a re-parented row affects both parents).
  - Deeper levels: a lookup query up the chain (batched, using the existing related-read SQL in reverse) when the change doesn't carry the root's join columns.
  - Lookup/many-to-one and many-to-many targets: a reverse query (`SELECT root key FROM … WHERE fk IN (…)`), streamed in key batches.
  - Grouped documents (OC-063): the group-by value(s) from the change, old and new.
  - Filters and aggregates: always the parent (a filtered-out row can still change a count).
- **Fan-out classification** and change policies per relation ([D5](00-architecture.md#d5--fan-out-is-a-choice-made-visible)): measured at setup with `KeyCounter`/`RelatedCounter`, shown in the setup dialog with the estimate ("a change to one PRODUCTS row updates up to 84,000 documents").
- Key templates using non-key columns: the old key comes from the change's old values when the replica identity provides them; otherwise the setup refuses the mapping with `REPLICATION_KEY_NEEDS_IDENTITY` and the fix (`REPLICA IDENTITY FULL`, or a key template on key columns).
- One table read by several mappings: impact computed per mapping.
- Explainability: `GET /replications/{id}/impact?table=…` describes the plan in words for the UI ("changes to ORDER_ITEMS update the orders document of their ORDER_ID").

**Out:** reading the stream, applying.

## Acceptance criteria & unit tests

### AC1 — Golden mappings
**Given** each 2.0 golden mapping and a table-driven set of changes (insert, update, delete, key change, re-parent, group change) on every table it reads, **then** the resolved key set equals the expected set (computed by brute force: rebuild every document before and after and diff).

### AC2 — Fan-out policy
**Given** a lookup with 100k dependent documents and policy `refresh-capped` at 10,000, **then** 10,000 keys are emitted, the relation is marked stale with the count, and the monitor event says so.

### AC3 — Identity requirement
**Given** a key template on `EMAIL` and a table with default replica identity, **then** setup fails with `REPLICATION_KEY_NEEDS_IDENTITY` and the snippet.

| Test | Location | Verifies |
|---|---|---|
| `TestImpact_AC1_GoldenBruteForce` | `internal/replication/impact/impact_integration_test.go` | AC1 |
| `TestImpact_AC2_FanOutCap` | `internal/replication/impact/fanout_test.go` | AC2 |
| `TestImpact_AC3_KeyIdentity` | `internal/replication/impact/impact_test.go` | AC3 |
