# OC-084 — Edge Server API spike: confirm what the target needs

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Edge Server Target | P0 | 2 | — | Not started |

## User story
As the **team building the Edge Server target**, I want the open questions about Edge Server's REST API answered against a real server, so that the target stories are built on facts, not guesses.

## Background
The `brewpos` demo confirmed single-document CRUD, `_rev` handling, `_query` and the changes feed on Edge Server 1.1.0 ([architecture §2](00-architecture.md#2-what-edge-server-is-and-what-it-means-for-us)). A migration needs bulk paths and metadata the demo never used.

## Scope
**In:** a time-boxed (2 days) investigation against `couchbase/edge-server` pinned by digest, producing `docs/release-3.0.0/edge-server-api-notes.md` with a runnable `scripts/edge-api-probe.sh` that reproduces every finding. Answer:
1. **Bulk write:** the endpoint (e.g. `POST /{keyspace}/_bulk_docs`), its body, and how it reports per-document success, conflict and error.
2. **Bulk read:** fetching many documents or just their revisions by key in one call (`_bulk_get`, `_all_docs?keys=`, or `_query` on `meta().id`).
3. **Metadata:** how to list a database's scopes and collections, and read the server version and whether writes and ad-hoc queries are enabled, without the config file.
4. **Limits:** maximum document and request size; reserved top-level property names.
5. **Auth:** how a user with only collection `write` behaves for reads, `_query` and metadata; status codes for wrong password vs. missing permission.
6. **Throughput:** a rough docs/s for single PUTs vs. the bulk endpoint on a laptop, 1 KB documents.
7. **Licensing:** whether the Edge Server image may be redistributed inside our appliance (input to OC-090).

**Out:** any production code.

## Acceptance criteria

### AC1 — Every question answered with evidence
**Given** the notes file, **then** each of the 7 questions has an answer with the request and response that shows it, or "not possible" with what was tried. `scripts/edge-api-probe.sh` runs against a fresh container and prints the same results.

### AC2 — Stories updated
**Then** OC-085 – OC-090 and [00-architecture.md](00-architecture.md) are updated where an answer changes them (endpoint names, limits, D3 retry rules), and any story whose estimate changes by more than a point is re-estimated.

| Test | Location | Verifies |
|---|---|---|
| `scripts/edge-api-probe.sh` | `scripts/` | AC1, rerunnable |
