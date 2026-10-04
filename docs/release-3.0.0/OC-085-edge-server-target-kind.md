# OC-085 — Edge Server target kind: configure, test, save

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Edge Server Target | P0 | 5 | OC-084, OC-036 | Not started |

## User story
As a **data engineer**, I want to set my project's target to a Couchbase Edge Server, test that the tool can reach it and write there, and save it, so that my migration can deliver documents to an edge database.

## Scope
**In:**
- A third target kind `edge-server` in the registry and `GET /target-kinds`, with the connection config in [architecture §4](00-architecture.md#4-connection-config-kind-edge-server).
- **Test connection** checks, each pass/warn/fail with a message users can act on:
  1. Reachable: TCP + TLS (with the given CA, or a clear "certificate not trusted" failure).
  2. Authenticated: wrong credentials → `TARGET_AUTH_FAILED`.
  3. The database exists and the user may open it.
  4. Writes are enabled: if not → fail with the fix ("set `\"enable_client_writes\": true` on database `store` and restart Edge Server").
  5. Ad-hoc queries: warn when off ("Explore will only look up documents by key").
  6. Server version reported; warn below the oldest version tested.
- Target step UI: an **Edge Server** card beside Couchbase and Directory; URL, database, user, password, certificate (reusing the 1.0 dropzone), advanced batch settings; the test results list as for the other kinds.
- Capabilities for the kind (`supportsExpiry: false`, durability options hidden, `DocLimiter` from the spike).
- Switching a project to or from Edge Server moves mapping locations as OC-068 does (database ↔ bucket, scope and collection kept).

**Out:** writing documents (OC-086), browsing collections (OC-087), explore (OC-088).

## Acceptance criteria & unit tests

### AC1 — Config validation
**Given** a URL with a path, a missing database, a batch size of 0 or `insecureSkipVerify` with a CA, **then** each is rejected with a field-level `VALIDATION_FAILED` message.

### AC2 — Test connection
**Given** the Edge container with writes enabled and an `admin` user, **then** checks 1–4 pass. **Given** a wrong password, **then** check 2 fails with `TARGET_AUTH_FAILED` and later checks are skipped. **Given** `enable_client_writes: false`, **then** check 4 fails with the config fix in its message.

### AC3 — UI
**Given** the Target step, **then** Edge Server is offered beside Couchbase and Directory; saving a passing connection makes it the project's target and unlocks Tables.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC1` | `internal/connectors/target/edge/config_test.go` | AC1 |
| `TestTest_AC2_*` | `internal/connectors/target/edge/test_test.go` (integration, Edge container) | AC2 |
| `TargetStep.test.tsx` › `OC-085 AC3` | `web/src/features/projects/steps/TargetStep.test.tsx` | AC3 |
