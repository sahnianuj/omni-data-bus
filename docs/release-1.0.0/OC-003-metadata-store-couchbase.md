# OC-003 — Metadata store on Couchbase: provisioning & repositories

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E1 Foundation | P0 | 5 | OC-002 | Implemented |

## User story
As an **engineer**, I want Omni-Data-Bus's own data (users, sessions, projects, connections, jobs) stored in a Couchbase bucket, with repeatable provisioning and repositories that enforce tenant isolation, uniqueness, and optimistic concurrency, so that features can persist state safely and upgrades are repeatable. It runs on **Couchbase Capella** in production and on **Couchbase Server 8.0.3** (Docker) locally and in CI.

## Implementation details

### Configuration
| Variable | Default (dev) | Notes |
|---|---|---|
| `OC_META_CONNSTR` | `couchbase://localhost` | Capella: `couchbases://cb.<id>.cloud.couchbase.com` |
| `OC_META_USERNAME` / `OC_META_PASSWORD` | `Administrator` / `password` (dev compose only) | Capella: a database credential with read/write on the bucket |
| `OC_META_BUCKET` | `omniconnect` | Must already exist on Capella (the SDK can't create buckets there). The dev compose init creates it |
| `OC_META_SCOPE` | `meta` | Created by provisioning when permitted |
| `OC_META_CAPELLA` | `false` | `true` applies the WAN development profile (longer timeouts) |
| `OC_META_TLS_SKIP_VERIFY` | `false` | Dev only |
| `OC_META_AUTO_PROVISION` | `true` | Run provisioning at startup; production can run `odb admin provision` instead |

### Provisioning (`internal/store/provision`)
Versioned, ordered, idempotent **steps** replace SQL migrations:
```go
type Step struct {
    Version int
    Name    string
    Apply   func(ctx context.Context, env Env) error // must be idempotent
}
```
- **Step 1 `collections`:** create scope `meta` and every collection from [architecture §5](00-architecture.md#5-domain-model-metadata-store-couchbase-capella). `ErrScopeExists` / `ErrCollectionExists` are success. Then wait until each collection is usable: a probe `Get` returns `ErrDocumentNotFound`.
- **Step 2 `indexes`:** `CREATE INDEX IF NOT EXISTS …` over SQL++, then wait until the indexes are online. Initial indexes:
  - `idx_projects_ws` on `projects(workspaceId, createdAt DESC)`;
  - `idx_members_user` on `members(userId)`;
  - `idx_connections_project` on `connections(projectId)`;
  - `idx_audit_ws` on `audit(workspaceId, createdAt DESC)`.
  Later stories append their own steps (e.g., job indexes in OC-023).
- The applied version is stored in `system::schema` (`{version, appliedAt}`). Steps with `Version ≤ stored` are skipped, and each step bumps the version with a CAS-guarded write, so two nodes starting together can't double-apply.
- **Permission fallback:** if a create fails with an authorization error, provisioning does **not** crash. It returns `ErrProvisioningNeedsAdmin` with the exact list of scopes, collections, and index statements to create in the Capella UI, and `odb admin provision --print` prints them.
- The same code runs from `odb admin provision` and at startup (`OC_META_AUTO_PROVISION`).

### Access layer (`internal/store`)
- `store.Open(ctx, cfg)` connects with gocb, calls `WaitUntilReady` for KV and Query, and exposes the collections, SQL++ execution, and transactions to repositories.
- **SQL++ statements** live in `internal/store/queries/*.sqlpp`, embedded with `go:embed`, each named with `-- name: ListProjects`. Tenant queries carry the annotation `-- tenant` and must filter `workspaceId = $workspaceId` (lint test AC2). Keyspace placeholders like `#{projects}` are expanded to `` `bucket`.`scope`.`projects` `` at load time. Identifiers are never built from user input.
- **Repositories** are interfaces, with two implementations:
  - `store/cbstore`, backed by Couchbase;
  - `store/memstore`, in memory, with the same semantics, used by service and handler unit tests.

  A shared **contract test suite** (`store/storetest`) runs against both: memstore in unit tests, and cbstore in integration tests with Couchbase 8.0.3 via testcontainers.
  ```go
  type WorkspaceID string
  type Projects interface {
      Create(ctx context.Context, ws WorkspaceID, p NewProject) (Project, error)   // tx: project + name lock
      Get(ctx context.Context, ws WorkspaceID, id string) (Project, error)         // ErrNotFound if other ws
      List(ctx context.Context, ws WorkspaceID, page Page) ([]Project, string, error)
      Update(ctx context.Context, ws WorkspaceID, p Project) (Project, error)      // p.Version = CAS
      Delete(ctx context.Context, ws WorkspaceID, id string) error                 // tx: cascade
  }
  ```
- **Documents** carry `createdAt` and `updatedAt`, which are set by the repository from an injected `Clock` (there are no triggers).
- **Errors** are translated in one place (`store.Translate`):
  - `ErrDocumentNotFound` → `ErrNotFound`
  - `ErrDocumentExists` on a `uniques` key → `ErrConflict{Field}`
  - `ErrCasMismatch` → `ErrVersionConflict` (412)
  - a transaction failure wrapping one of these → the same domain error
  - timeouts and unavailability → `ErrUnavailable` (503)
- **Transactions:** Couchbase ACID transactions (`cluster.Transactions().Run`). The SDK retries write-write conflicts itself.
- **Pagination:** keyset on `(createdAt, id)` descending; the cursor is base64 of `createdAt|id` and opaque to clients. Queries use `ScanConsistency: request_plus` so a user sees their own writes immediately.

### Health
`/readyz` reports `checks.metadata` from a KV ping of the metadata bucket (1 s timeout).

## Acceptance criteria & unit tests

### AC1 — Provisioning is ordered, versioned, and idempotent
**Given** an empty bucket, **when** provisioning runs twice, **then** the second run applies no steps, all collections and indexes exist, and `system::schema.version` equals the last step. **Given** the step list, **then** versions are unique and strictly increasing from 1.

| Test | Location | Verifies |
|---|---|---|
| `TestSteps_AC1_VersionsSequential` | `internal/store/provision/provision_test.go` | Unit test over the step list |
| `TestRun_AC1_SkipsAppliedSteps` | same | Fake env; a stored version of 1 → only step 2 runs |
| `TestRun_AC1_ExistsIsSuccess` | same | Fake returns `ErrCollectionExists` → no error |
| `TestProvision_AC1_Idempotent` *(integration)* | `internal/store/cbstore/provision_integration_test.go` | Couchbase 8.0.3 container |

### AC2 — Tenant isolation is enforced
**Given** a project in workspace A, **when** it is fetched with workspace B's ID, **then** `ErrNotFound` is returned (never another tenant's data, and never `403`, to avoid ID probing). **And** every SQL++ statement annotated `-- tenant` filters on `workspaceId = $workspaceId`.

| Test | Location | Verifies |
|---|---|---|
| `storetest.Projects` › `AC2_CrossWorkspaceGetIsNotFound` | `internal/store/storetest/projects.go`, run by `memstore` and `cbstore` | |
| `TestQueries_AC2_TenantQueriesFilterByWorkspace` | `internal/store/cbstore/querylint_test.go` | Parses each embedded `.sqlpp` file |
| `TestQueries_AC2_KeyspacePlaceholdersExpand` | same | |

### AC3 — Store errors map to domain errors
**Given** `ErrDocumentExists` on a `uniques` key, **then** `ErrConflict{Field}` is returned. **Given** `ErrCasMismatch`, **then** `ErrVersionConflict`. **Given** `ErrDocumentNotFound`, **then** `ErrNotFound`. **Given** a transaction failure whose cause is one of these, **then** the same mapping applies.

| Test | Location | Verifies |
|---|---|---|
| `TestTranslate_AC3_Table` | `internal/store/cbstore/errors_test.go` | Table-driven over gocb sentinel errors, wrapped and unwrapped |

### AC4 — Multi-document writes are atomic
**Given** creating a project whose name already exists in the workspace, **then** neither the project document nor a second name lock is written. **Given** project delete, **then** the project, its connections, and its name lock are removed together.

| Test | Location | Verifies |
|---|---|---|
| `storetest.Projects` › `AC4_DuplicateNameWritesNothing` | `internal/store/storetest/projects.go` | memstore and cbstore |
| `storetest.Projects` › `AC4_DeleteCascades` | same | |

### AC5 — Cursor pagination is stable
**Given** 55 projects and `limit=20`, **when** paging with `nextCursor`, **then** exactly 55 unique projects come back across 3 pages, and the last page has `nextCursor=""`.

| Test | Location | Verifies |
|---|---|---|
| `TestCursor_AC5_EncodeDecodeRoundTrip` | `internal/store/cursor_test.go` | |
| `TestCursor_AC5_RejectsTampered` | same | Invalid base64 → `ErrInvalidCursor` (maps to 400) |
| `storetest.Projects` › `AC5_PaginatesAll` | `internal/store/storetest/projects.go` | |

### AC6 — Timestamps maintained by the repository
**Given** an update, **then** `updatedAt` advances to the injected clock's time and `createdAt` does not change.

| Test | Location | Verifies |
|---|---|---|
| `storetest.Projects` › `AC6_UpdatedAtAdvances` | `internal/store/storetest/projects.go` | Fake clock |

### AC7 — Project owns its connections
**Given** a project with a source and a target connection, **when** a second source is upserted, **then** the document at `<projectId>::source` is replaced in place (same ID, new config, new sealed secret), with never two sources. **When** the project is deleted, **then** its connections (and sealed secrets) are deleted in the same transaction.

| Test | Location | Verifies |
|---|---|---|
| `storetest.Connections` › `AC7_UpsertByProjectRole` | `internal/store/storetest/connections.go` | Same ID retained, a single document per role |
| `storetest.Projects` › `AC7_DeleteCascadesConnections` | `internal/store/storetest/projects.go` | |

### AC8 — Missing permissions produce actionable output
**Given** credentials that can't create collections, **when** provisioning runs, **then** it returns `ErrProvisioningNeedsAdmin` listing each missing scope, collection, and index statement, and `admin provision --print` prints them.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_AC8_PermissionDeniedListsMissing` | `internal/store/provision/provision_test.go` | |

## Implementation notes (as built)
- **Verified against Couchbase Capella and a local Couchbase Server 8.0.3 container.** The integration contract suite (`make test-integration`, or `OC_IT_META_*` pointed at a cluster) passed against the project's Capella cluster in a throwaway `it_<random>` scope that is dropped afterwards. By default the suite starts a local `couchbase/server:enterprise-8.0.3` container.
- **Optimistic concurrency** uses a `version` field inside each document, compared and incremented inside the transaction or CAS-guarded write, rather than exposing raw CAS values. It works the same for KV and transactional updates.
- **Timestamps** are stored as fixed-width UTC strings (`2006-01-02T15:04:05.000000000Z`) so SQL++ ordering and keyset pagination compare correctly.
- **Reading a missing collection** makes the SDK retry until timeout, so provisioning checks the bucket manifest (`GetAllScopes`) before reading `system::schema`.
- Repositories are hand-written with named SQL++ files (`internal/store/cbstore/queries/*.sqlpp`); a lint test enforces the `workspaceId` filter on every `-- tenant` query.
