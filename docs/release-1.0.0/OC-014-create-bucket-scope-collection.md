# OC-014 — Create bucket, scope & collection

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E3 Target | P0 | 5 | OC-013 | Implemented |

## User story
As a **data engineer**, I want to create the bucket, scope, or collection I need directly from the app when it doesn't exist yet, so that I don't have to switch to the Couchbase console mid-flow.

## Implementation details

### Validation rules (`internal/connectors/target/couchbase/names.go`)
| Entity | Rule |
|---|---|
| Bucket | 1–100 characters, `[A-Za-z0-9._%-]`, must not start with `.` |
| Scope / collection | 1–251 characters, `[A-Za-z0-9_%-]`, must not start with `_` or `%` |
| RAM quota | ≥ 100 MB (magma ≥ 1024 MB), ≤ the cluster's free quota (from `GET /pools/default` when accessible) |
| Replicas | 0–3, and ≤ data nodes − 1 (warning if more) |
| Max TTL (collection) | 0 or 1…2,147,483,647 seconds |

The same rules are mirrored in zod (`src/features/targets/names.ts`). A single JSON fixture of test cases is shared by both test suites, to keep the two in sync.

### API
| Method | Path | Body |
|---|---|---|
| POST | `/connections/{id}/buckets` | `{name, ramQuotaMB, bucketType: couchbase\|ephemeral, numReplicas, storageBackend: couchstore\|magma, flushEnabled:false}` |
| POST | `/connections/{id}/buckets/{b}/scopes` | `{name}` |
| POST | `/connections/{id}/buckets/{b}/scopes/{s}/collections` | `{name, maxExpirySec?}` |

- **Implementation:** `cluster.Buckets().CreateBucket(gocb.CreateBucketSettings{BucketSettings: gocb.BucketSettings{…}}, nil)`, `bucket.CollectionsV2().CreateScope(name, nil)`, and `CreateCollection(scope, name, &gocb.CreateCollectionSettings{MaxExpiry: d}, nil)`.
- **Idempotency:** `ErrBucketExists`, `ErrScopeExists`, and `ErrCollectionExists` map to `409 KEYSPACE_EXISTS` with `detail`. The UI treats that as "select the existing one" rather than an error.
- **Readiness wait:** creation is eventually consistent across nodes, so after a create the server polls until the entity is usable, then returns `201`:
  - Bucket: `bucket.WaitUntilReady(30s, KV)`.
  - Scope and collection: poll `GetAllScopes` until present on the manifest, then do a probe `collection.Get("__oc_probe__")` expecting `ErrDocumentNotFound` rather than `ErrCollectionNotFound`, with backoff at 100 ms ×1.5 up to 30 s.
  - On timeout it returns `202 {status:"pending"}`, and the UI polls `GET …/collections/{c}`.
- Every create is audited (`keyspace.create`) and the call is capability-checked server-side (403 `TARGET_PERMISSION_DENIED`).
- **Capella:** bucket create returns `422 CAPELLA_BUCKET_CREATE_UNSUPPORTED` with a hint and a link to the Capella docs.

### UI
- *+ New bucket* opens a dialog with name, type (Couchbase / Ephemeral cards), storage backend (Magma recommended for > 1 TB / Couchstore), RAM quota slider with a numeric input (showing free cluster quota when known), and replicas.
- *+ New scope* and *+ New collection* are inline popovers anchored to the combobox, with name and optional max TTL (human input such as `30d`, converted to seconds).
- The submit button has a progress state ("Creating… waiting for cluster to be ready"). On success the new entity is auto-selected in the picker and a toast confirms.
- **Plan-then-create option:** in the project wizard, the user can type a *new* scope or collection name and defer creation to "on migration start". The mapping stores `targetCreateIfMissing: true`, and the job's pre-flight (OC-023) creates it. This lets viewers without create rights draft projects, and lets editors review before touching the cluster.

## Acceptance criteria & unit tests

### AC1 — Name & settings validation (server and client parity)
**Given** the shared fixture of valid and invalid names and settings, **then** the Go validator and the zod schema produce identical accept or reject outcomes.

| Test | Location | Verifies |
|---|---|---|
| `TestNames_AC1_SharedFixture` | `internal/connectors/target/couchbase/names_test.go` | Reads `testdata/keyspace-names.json` |
| `names.test.ts` › `AC1: shared fixture parity` | `src/features/targets/names.test.ts` | Same fixture file |
| `TestBucketSpec_AC1_MagmaMinQuota` | `…/names_test.go` | |

### AC2 — Create scope & collection with readiness wait
**Given** valid input and permissions, **then** the SDK create is called once, readiness polling continues until the probe returns `ErrDocumentNotFound`, and the API returns 201. **Given** readiness is not reached within 30 s, **then** it returns 202 `pending`.

| Test | Location | Verifies |
|---|---|---|
| `TestCreateCollection_AC2_WaitsUntilReady` | `…/couchbase/create_test.go` | Fake returns CollectionNotFound ×3, then DocumentNotFound; fake clock |
| `TestCreateCollection_AC2_TimeoutPending` | same | |
| `TestCreateScope_AC2_CallsSDK` | same | |
| `TestCreateCollection_AC2_Integration` *(integration)* | `…/create_integration_test.go` | |

### AC3 — Already exists is not an error for the user
**Given** the collection already exists, **then** the API returns 409 `KEYSPACE_EXISTS`, and the UI selects the existing collection with the info toast "Collection already exists — selected it".

| Test | Location | Verifies |
|---|---|---|
| `TestCreate_AC3_ExistsMapsTo409` | `…/create_test.go` | Table: bucket, scope, collection |
| `NewCollectionPopover.test.tsx` › `AC3: selects existing on 409` | `src/features/targets/NewCollectionPopover.test.tsx` | |

### AC4 — Permission & Capella guards
**Given** `CanManageScopes=false`, **then** the API returns 403 without calling the SDK. **Given** Capella and a bucket create, **then** it returns 422 `CAPELLA_BUCKET_CREATE_UNSUPPORTED`.

| Test | Location | Verifies |
|---|---|---|
| `TestCreateHandler_AC4_PermissionDenied` | `internal/httpapi/keyspace_test.go` | SDK fake not called |
| `TestCreateHandler_AC4_CapellaBucket` | same | |

### AC5 — Deferred creation
**Given** the user types a new collection name and chooses *Create when migration starts*, **then** the mapping stores `targetCreateIfMissing:true` and the picker shows the collection with a "will be created" badge. No SDK create is called at this time.

| Test | Location | Verifies |
|---|---|---|
| `KeyspacePicker.test.tsx` › `AC5: deferred create sets flag and badge` | `src/features/targets/KeyspacePicker.test.tsx` | |
| `TestMappingSpec_AC5_CreateIfMissingPersisted` | `internal/mapping/spec_test.go` | |

### AC6 — TTL input parsing
**Given** `30d`, `12h`, `90m`, or `3600`, **then** they convert to 2592000, 43200, 5400, and 3600 seconds. **Given** `-1` or `abc`, **then** a validation error.

| Test | Location | Verifies |
|---|---|---|
| `parseDuration.test.ts` › `AC6: units and invalid inputs` | `src/lib/format/parseDuration.test.ts` | |

## Implementation notes (as built)
- **Guards run server-side before the SDK:** name/settings validation (422), Capella bucket create (422 `CAPELLA_BUCKET_CREATE_UNSUPPORTED`), and capabilities from the credential's live roles (`TARGET_PERMISSION_DENIED`; unknown roles deny). `KEYSPACE_EXISTS` is 409; the UI selects the existing keyspace with an info toast.
- **Readiness:** bucket → KV `WaitUntilReady(30s)`; scope → manifest; collection → manifest, then a probe read expecting `DocumentNotFound`, backoff 100 ms ×1.5 (capped at 2 s) for up to 30 s, else 202 `pending`, which the UI polls. On Couchbase 8.0.3 a scope + collection were usable in ~110 ms.
- **Forms opt out of browser validation** (`noValidate`): our zod rules (shared fixture with Go) are the single source of truth, and native number-step checks were silently blocking submits.
- **AC5 split:** the picker's *Create when migration starts* option (flag + "will be created" badge) is implemented and tested here; persisting `targetCreateIfMissing` in the mapping (`TestMappingSpec_AC5_CreateIfMissingPersisted`) moves to OC-035 with the mappings API, and the pre-flight create to OC-023. Deferred creation covers collections; the scope must exist.
- **Where it lives:** `/projects/:id/target/keyspaces` (*Browse keyspaces →* on the saved target card). The Tables step (OC-035) reuses `KeyspacePicker` per table.

