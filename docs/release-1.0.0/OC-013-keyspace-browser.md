# OC-013 — Couchbase keyspace browser (buckets, scopes, collections)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E3 Target | P0 | 5 | OC-012 | Implemented |

## User story
As a **data engineer**, I want to browse the buckets, scopes, and collections of my Couchbase target and pick the destination collection, so that I control where migrated documents land.

## Implementation details

### API
| Method | Path | Response |
|---|---|---|
| GET | `/connections/{id}/buckets` | `[{name, type, ramQuotaMB, numReplicas, storageBackend, itemCount?, canManage}]` |
| GET | `/connections/{id}/buckets/{bucket}/scopes` | `[{name, collections:[{name, maxExpirySec, history?}]}]` |
| GET | `/connections/{id}/buckets/{bucket}/scopes/{scope}/collections/{collection}` | `{name, maxExpirySec, docCountEstimate?}` |

- **Bucket list:** `cluster.Buckets().GetAllBuckets(nil)`. If the user lacks the permission (Capella DB credentials or scoped users), it falls back to `Capabilities.BucketsAllowed` and then to opening each allowed bucket, and the response carries a `partial: true` flag. When nothing can be listed, the UI offers a *Type a bucket name* input, validated by opening the bucket with `WaitUntilReady(5s)`.
- **Scopes and collections:** `bucket.CollectionsV2().GetAllScopes(nil)`. System scopes (`_system`) are hidden, and `_default._default` is shown with a "default" badge.
- **Doc count estimate:** best-effort and optional. It uses `SELECT COUNT(*) FROM \`b\`.\`s\`.\`c\`` only when the Query service is available, with a 3 s timeout, and failure is ignored. It is lazy-loaded by the UI per collection.
- The adapter lives behind the `KeyspaceAdmin` interface so it can be unit-tested with fakes:
  ```go
  type KeyspaceAdmin interface {
      ListBuckets(ctx) ([]BucketInfo, bool /*partial*/, error)
      ListScopes(ctx, bucket string) ([]ScopeInfo, error)
      CreateBucket(ctx, BucketSpec) error                      // OC-014
      CreateScope(ctx, bucket, scope string) error             // OC-014
      CreateCollection(ctx, bucket, scope string, CollectionSpec) error // OC-014
      WaitCollectionReady(ctx, ks Keyspace) error              // OC-014
  }
  ```
- Name ordering is case-insensitive alphabetical, with `_default` first.

### UI component: `KeyspacePicker`
Used in the project wizard's Tables step (per-table target, OC-007), on the Target summary card's *Browse keyspaces →*, and in the mapping header (OC-019). It always operates on the project's stored target connection.
```
Target collection
┌ Bucket ───────────┐ ┌ Scope ────────────┐ ┌ Collection ───────────┐
│ travel-sample   ▾ │ │ inventory       ▾ │ │ airline            ▾ │
└───────────────────┘ └───────────────────┘ └───────────────────────┘
   + New bucket          + New scope           + New collection
Keyspace: `travel-sample`.`inventory`.`airline`  · ~187 docs  [copy]
```
- Three cascading comboboxes, each searchable. Changing the bucket resets the scope and collection. Each dropdown includes a *+ New …* action (OC-014), which is disabled with a tooltip when the capability is missing.
- The resolved keyspace is shown in monospace with a copy button, plus a doc count badge (lazy). If the collection already has documents, an amber note reads "Collection is not empty — existing documents with the same keys will be overwritten in upsert mode".
- Loading, empty ("No scopes yet — create one"), and error states follow OC-004.

## Acceptance criteria & unit tests

### AC1 — List buckets (full and partial)
**Given** a full-admin user, **then** all buckets are listed with their settings. **Given** a user without list permission but with `BucketsAllowed=["app"]`, **then** `[app]` is returned with `partial:true`. **Given** nothing can be listed, **then** an empty list with `partial:true` is returned, and the UI shows the manual bucket-name input.

| Test | Location | Verifies |
|---|---|---|
| `TestListBuckets_AC1_Full` | `internal/connectors/target/couchbase/keyspace_test.go` | Fake `bucketManager` |
| `TestListBuckets_AC1_FallbackAllowed` | same | |
| `TestListBuckets_AC1_NothingListable` | same | |
| `KeyspacePicker.test.tsx` › `AC1: manual bucket entry when partial & empty` | `src/features/targets/KeyspacePicker.test.tsx` | |

### AC2 — List scopes & collections
**Given** a bucket with scopes `_default`, `_system`, and `inventory`, **then** `_system` is hidden, `_default` comes first, and collections are nested and sorted.

| Test | Location | Verifies |
|---|---|---|
| `TestListScopes_AC2_HidesSystemOrdersDefaultFirst` | `…/keyspace_test.go` | |
| `KeyspacePicker.test.tsx` › `AC2: cascading reset on bucket change` | `KeyspacePicker.test.tsx` | |

### AC3 — Capability-gated create actions
**Given** `CanCreateBucket=false`, **then** *+ New bucket* is disabled with the reason tooltip. **Given** `CanManageScopes=false` for the selected bucket, **then** *+ New scope* and *+ New collection* are disabled.

| Test | Location | Verifies |
|---|---|---|
| `KeyspacePicker.test.tsx` › `AC3: disables actions per capabilities` | `KeyspacePicker.test.tsx` | |

### AC4 — Non-empty collection notice
**Given** a selected collection whose count estimate is > 0, **then** the amber overwrite notice is shown. **Given** the count call fails, **then** no badge and no error are shown (best-effort).

| Test | Location | Verifies |
|---|---|---|
| `TestDocCount_AC4_TimeoutReturnsUnknown` | `…/keyspace_test.go` | |
| `KeyspacePicker.test.tsx` › `AC4: shows overwrite notice when count > 0` | `KeyspacePicker.test.tsx` | |

### AC5 — Identifier escaping in N1QL
**Given** a bucket named `my-bucket` and a scope `s%1`, **then** the count query escapes them with backticks (`` `my-bucket`.`s%1`.`c` ``), and backticks inside names are rejected by name validation (they are not valid in Couchbase names).

| Test | Location | Verifies |
|---|---|---|
| `TestKeyspaceQuoting_AC5` | `…/keyspace_test.go` | |

## Implementation notes (as built)
- **API shapes:** `GET …/buckets` returns `{buckets, partial, capabilities}` so the picker can gate its create actions without another call; `GET …/scopes` returns `{scopes}`. Handlers live in `internal/keyspaces`; the SDK logic sits behind the `adminAPI` interface in `internal/connectors/target/couchbase/keyspace.go`, with `target.Keyspaces` as the target-neutral contract.
- **Pooling:** the keyspace browser reuses one SDK connection per repository and version (`Pool`), so an edited repository reconnects and browsing doesn't bootstrap the SDK on every click.
- **Fallbacks:** without bucket-management access (Capella database credentials, scoped users) the bucket list comes from the roles, or for all-bucket credentials from `system:buckets`, keeping only buckets that open within 5 s (`partial: true`). Without collection-management access, scopes come from `system:keyspaces`.
- **Fail fast:** the SDK retries an unknown bucket until its timeout (~75 s), so every bucket operation first opens the bucket with a 5 s bound and answers 404 "not found or not accessible".
- **Verified:** listing on Couchbase Server 8.0.3 (dev stack) and read-only against the Capella cluster.

