# OC-090 — A managed Edge Server in the all-in-one image: create scopes, collections and users from the tool

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Edge Server Target | P2 | 5 | OC-087, OC-072, OC-084 | Not started |

## User story
As a **data engineer** trying the tool with the all-in-one image, I want a bundled Edge Server whose scopes, collections, indexes and users I can create from the tool, so that I can migrate into Edge Server without editing its config file by hand.

## Background
A customer's Edge Server can only be changed through its config file and a restart, so for theirs we generate snippets (OC-087, [architecture D2](00-architecture.md#d2--we-never-edit-a-customers-edge-server-configuration)). For an Edge Server **we run**, the tool can own that file.

## Scope
**In:**
- The appliance (OC-072) optionally runs Edge Server next to Couchbase CE under the same supervisor (`run.sh --with-edge`), on a volume for its databases and config; only if the spike (OC-084) confirms we may redistribute the image.
- A **bundled Edge Server** target button, like "Use the bundled Couchbase" (OC-072): creates a sealed connection with a generated admin user.
- For this connection only, the location picker's **New scope / collection** really creates it: the tool updates `config.json` (with indexes from OC-087's suggestions), restarts Edge Server, waits for it to be ready and re-lists. Also: turn ad-hoc queries on, add a read-only user for sharing.
- The config file is written atomically with a backup of the previous version; a config Edge Server refuses is rolled back and the error shown.

**Out:** managing an Edge Server the tool doesn't run; replication setup to Capella.

## Acceptance criteria & unit tests

### AC1 — Bundled Edge target
**Given** the appliance started with `--with-edge`, **when** I choose "Use the bundled Edge Server", **then** the connection is saved and its test passes.

### AC2 — Create a collection
**When** I create `pos.order_lines` from the picker, **then** within 30 s it's listed, a migration into it succeeds and Explore queries it.

### AC3 — Bad config rolls back
**Given** a change Edge Server refuses to start with, **then** the previous config is restored, Edge Server is running again and the error is shown.

| Test | Location | Verifies |
|---|---|---|
| `TestManagedEdge_AC2_CreateCollection` | `internal/connectors/target/edge/managed_test.go` (integration) | AC2 |
| `TestManagedEdge_AC3_Rollback` | same | AC3 |
| `deploy/appliance/test/smoke.sh` (extended) | `deploy/appliance/test/` | AC1 |
