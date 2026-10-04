# OC-087 — Edge Server locations, pre-migration checks and collection setup

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Edge Server Target | P0 | 5 | OC-085 | Not started |

## User story
As a **data engineer**, I want to pick the Edge Server scope and collection for each mapping, know before Migrate whether the server is ready, and get the exact configuration to add when a collection doesn't exist yet, so that I can set up the edge database without guessing at its config file.

## Background
Edge Server has no REST endpoint to create scopes, collections or indexes: they're declared in `config.json` and take effect after a restart ([architecture §2](00-architecture.md#2-what-edge-server-is-and-what-it-means-for-us), D2).

## Scope
**In:**
- `LocationBrowser` for `edge-server`: database → scope → collection, with document counts where the spike found a cheap way.
- **New scope / collection** in the location picker produces a **config snippet** instead of creating anything: the database's `collections` block with the new keyspace and suggested indexes (the mapping's key fields and the fields the Explore view filters on), plus a users-file grant for the migration user. Copy button, and a "Check again" button that re-lists after the user restarts Edge Server.
- One combined snippet on the Review step for every missing collection across the project's mappings.
- `Preflighter` checks per mapping, on the Review step (OC-078 auto-run): collection exists; writes enabled; the user may write there (probe write and delete of `odb-probe-<uuid>`); ad-hoc queries (warn only); replication configured on the database (info: "documents will also replicate to <target>").
- Mapping locations for Edge: `{database, scope, collection}`; default collection name from the mapping's type value, as for Couchbase.

**Out:** editing the customer's config file or restarting their server (D2; see OC-090 for a server we run).

## Acceptance criteria & unit tests

### AC1 — Browse
**Given** an Edge database with `pos.orders` and `pos.customers`, **then** the picker lists scope `pos` with both collections.

### AC2 — Missing collection → snippet
**Given** a mapping targeting `pos.order_lines`, which doesn't exist, **when** I choose "New collection", **then** the dialog shows a valid JSON snippet adding `pos.order_lines` (with indexes) to database `store`'s `collections`; after adding it and restarting Edge, "Check again" finds it and the mapping's location is valid.

### AC3 — Preflight
**Given** a missing collection, writes disabled, or a user without write access, **then** Review shows each as a failing check with its fix, and Migrate is disabled; **given** ad-hoc queries off, **then** a warning only.

| Test | Location | Verifies |
|---|---|---|
| `TestListLocations_AC1` | `internal/connectors/target/edge/locations_test.go` (integration) | AC1 |
| `TestConfigSnippet_AC2` | `internal/connectors/target/edge/snippet_test.go` | AC2: snippet parses and merges into a real config |
| `TestPreflight_AC3_*` | `internal/connectors/target/edge/preflight_test.go` (integration) | AC3 |
| `EdgeCollectionDialog.test.tsx` | `web/src/features/targets/EdgeCollectionDialog.test.tsx` | AC2 UI |
