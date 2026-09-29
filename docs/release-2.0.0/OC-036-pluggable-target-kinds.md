# OC-036 — Pluggable target kinds (Couchbase or directory)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E7 Foundations 2.0 | P0 | 5 | OC-012, OC-013, OC-025, OC-030 | Not started |

## User story
As a **data engineer**, I want to choose what kind of target my project writes to, either a Couchbase cluster or a directory of JSON files, so that I can use the same mapping work for a database load or a file export.

## Scope
**In:**
- A target **kind** on the project's target connection: `couchbase` (1.0) or `directory` (OC-038).
- Refactor the target-side code behind ports so the pipeline, preflight, the Target step, verification and the job monitor don't assume Couchbase.
- Target step UI: a kind picker before the connection form.
- A mapping **location** that depends on the kind: `{bucket, scope, collection}` for Couchbase, `{folder}` for a directory.

**Out:** more than one target per project; other target kinds (S3, GCS, Kafka), which stay post-2.0. The ports are designed so that they can be added.

## Implementation details

### Target ports (`internal/connectors/target`)
```go
type Connector interface {                // 1.0, unchanged
    Descriptor() connectors.Descriptor
    Validate(cfg json.RawMessage) error
    connectors.Tester
    Close(ctx context.Context) error
}
type LocationBrowser interface {          // new: generalizes the keyspace browser (OC-013)
    ListLocations(ctx context.Context, cfg json.RawMessage, creds connectors.Credentials, parent Location) ([]LocationNode, error)
    CreateLocation(ctx context.Context, cfg json.RawMessage, creds connectors.Credentials, loc Location) error
}
type Preflighter interface {              // new: moved out of jobs/ (OC-023)
    Preflight(ctx context.Context, cfg json.RawMessage, creds connectors.Credentials, locs []Location, est Estimate) []PreflightCheck
}
// WriterOpener, DocWriter, DocReader: unchanged (1.0). WriteOptions gains Location, replacing Bucket/Scope/Collection.
```
- `Location` is a tagged union: `{kind:"couchbase", bucket, scope, collection}` or `{kind:"directory", folder}`. It is stored on each mapping as `location`. Existing mappings get a Couchbase location from their 1.0 `target` refs during the OC-037 upgrade.
- The registry exposes each kind's capabilities (`supportsDurability`, `supportsExpiry`, `supportsCreateLocation`, `maxDocBytes`). The UI hides options that the chosen kind doesn't support. For example, document expiry isn't shown for directories.
- `jobs` preflight calls `Preflighter` instead of the Couchbase-specific checks. Write modes stay `upsert | insert | replace`, and each kind documents what they mean (OC-040).

### API
- `PUT /projects/{projectId}/connections/target` takes `type: couchbase | directory`. Changing the type of an existing target is allowed only while no mapping has a location. Otherwise `409 TARGET_KIND_IN_USE`, whose detail lists the mappings.
- `GET /connections/{id}/locations?parent=` and `POST /connections/{id}/locations` replace the keyspace endpoints for new clients. The 1.0 `/buckets…` endpoints stay as aliases for Couchbase targets until 3.0.
- `GET /target-kinds` returns descriptors and capabilities.

### UI
- Target step: a two-card kind picker (*Couchbase cluster* / *Directory of JSON files*) above the form. The form, the location browser and the test result come from the chosen kind.
- The Tables step's location column shows `bucket.scope.collection` or `📁 orders/`, depending on the kind.

## Acceptance criteria & unit tests

### AC1 — Kind registry
**Given** the server starts, **then** `GET /target-kinds` returns `couchbase` and `directory` with their capabilities, and an unknown `type` on PUT → `422 TARGET_KIND_UNKNOWN`.

| Test | Location | Verifies |
|---|---|---|
| `TestTargetRegistry_AC1_KindsAndCapabilities` | `internal/connectors/target/target_test.go` | |
| `TestPutTarget_AC1_UnknownKind` | `internal/httpapi/connections_test.go` | |

### AC2 — Couchbase behavior unchanged
**Given** the 1.0 pipeline, preflight and verification test suites, **when** they run through the new ports, **then** they all pass with no change to expected output.

| Test | Location | Verifies |
|---|---|---|
| `TestPipeline_AC2_CouchbaseThroughPorts` *(integration)* | `internal/pipeline/pipeline_integration_test.go` | Re-run of the 1.0 golden migration |
| `TestPreflight_AC2_CouchbaseChecksMoved` | `internal/connectors/target/couchbase/preflight_test.go` | |

### AC3 — Changing kind is guarded
**Given** a project whose mappings have locations, **when** the target type changes, **then** `409 TARGET_KIND_IN_USE` lists them. **Given** no mapping has a location, **then** the change succeeds.

| Test | Location | Verifies |
|---|---|---|
| `TestPutTarget_AC3_KindChangeGuard` | `internal/httpapi/connections_test.go` | |

### AC4 — Location model
**Given** a 1.0 mapping with `target {bucket, scope, collection}`, **when** read, **then** it exposes `location {kind:"couchbase", …}`. **Given** a directory location without `folder`, **then** validation fails with `LOCATION_INVALID`.

| Test | Location | Verifies |
|---|---|---|
| `TestLocation_AC4_FromV1Target` | `internal/mapping/location_test.go` | |
| `TestLocation_AC4_Validate` | same | |

### AC5 — Capability-driven UI
**Given** a directory target, **then** the job options hide durability and expiry. **Given** a Couchbase target, **then** they show as in 1.0.

| Test | Location | Verifies |
|---|---|---|
| `TargetKindPicker.test.tsx` › `AC5: switches form and browser by kind` | `src/features/targets/TargetKindPicker.test.tsx` | |
| `JobOptions.test.tsx` › `AC5: hides unsupported options` | `src/features/jobs/JobOptions.test.tsx` | |
