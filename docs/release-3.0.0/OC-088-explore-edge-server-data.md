# OC-088 — Explore Edge Server data: browse, look up, query

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Edge Server Target | P0 | 5 | OC-085, OC-041 | Not started |

## User story
As a **data engineer**, I want to browse, look up and query the documents in an Edge Server collection from inside the tool, so that I can check the migrated result even though Edge Server has no UI of its own.

## Scope
**In:**
- An **Explore** view for Edge targets, reached from the Migrate step's finished job ("Explore the result") and from the target's location picker; it reuses the directory document browser (OC-041) layout.
- **Browse** a collection: paged list of document IDs (newest or by ID), document count, a JSON viewer for the selected document (Edge's `_id`/`_rev` shown as metadata, not body).
- **Look up** by document ID, with the mapping's key template as a hint ("order::{order_id}").
- **Query**: a SQL++ box run with `POST /{keyspace}/_query` (`FROM _`, named `$params`), results as a table or JSON, 100-row default limit, a 10 s timeout; templates for "count", "first 20", "find by field". Disabled with the reason and config fix when ad-hoc queries are off.
- Read-only: no edits or deletes from Explore.
- Server-side proxying only (Edge has no CORS); every request uses the project's sealed credentials, and viewers need the `viewer` role on the project.

**Out:** a live changes feed view; editing documents; Explore for the Couchbase target (the Capella UI covers it).

## Acceptance criteria & unit tests

### AC1 — Browse and look up
**Given** a migrated `pos.orders` with 1,000 documents, **then** Explore lists them 50 per page with the total, opens any one, and looking up `order::42` opens it; an unknown ID shows "No document with this ID".

### AC2 — Query
**Given** ad-hoc queries on, **when** I run `SELECT COUNT(*) AS n FROM _`, **then** `n = 1000` is shown; a syntax error shows Edge's message; a query over the row limit is truncated with a notice.

### AC3 — Queries off
**Given** `enable_adhoc_queries: false`, **then** the Query tab is disabled with the config fix, and browse/look-up still work.

| Test | Location | Verifies |
|---|---|---|
| `TestExplore_AC1_*`, `TestQuery_AC2_*` | `internal/connectors/target/edge/explore_test.go` (integration) | AC1, AC2 |
| `EdgeExplorer.test.tsx` | `web/src/features/targets/EdgeExplorer.test.tsx` | AC1 – AC3 UI |
| `edge-explore.spec.ts` | `web/tests/e2e/edge-explore.spec.ts` | Migrate → Explore end to end |
