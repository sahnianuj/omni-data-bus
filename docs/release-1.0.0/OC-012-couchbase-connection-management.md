# OC-012 — Couchbase target repository (per project): configure, test, save

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E3 Target | P0 | 8 | OC-006, OC-007, OC-008, OC-009 (shares project-scoped connection API & UI) | Implemented |

## User story
As a **data engineer**, I want to set up my project's Couchbase target repository (self-managed or Capella), test it with a **Test connection** button, see what I'm allowed to do there, and save it once to the project, so that every mapping and migration in the project writes with those stored credentials.

## Implementation details

### Target connector abstraction (`internal/connectors/target`)
```go
type Connector interface {
    Descriptor() Descriptor
    Validate(cfg Config) error
    Open(ctx context.Context, cfg Config, secrets Secrets) (Session, error)
}
type Session interface {
    Ping(ctx context.Context) (ClusterInfo, error)
    Capabilities(ctx context.Context) (Capabilities, error)
    Keyspaces() KeyspaceAdmin             // OC-013/022
    Sampler() DocumentSampler             // OC-015
    Writer(ks Keyspace, opts WriterOptions) (DocWriter, error) // OC-025
    Close() error
}
```
The Couchbase implementation lives in `internal/connectors/target/couchbase`. A session pool caches open `*gocb.Cluster` instances per connection ID with a 10-minute idle TTL, because bootstrapping gocb is expensive. The cache key includes a hash of the config version, so an edit invalidates the cached cluster.

### Config model
```go
type CouchbaseConfig struct {
    ConnectionString string `json:"connectionString" validate:"required"` // couchbase://h1,h2 | couchbases://cb.xxxx.cloud.couchbase.com
    Username         string `json:"username" validate:"required"`
    Deployment       string `json:"deployment" validate:"oneof=self-managed capella"`
    TLSVerify        bool   `json:"tlsVerify"`        // default true; false allowed only for couchbases:// in dev with warning
    KVTimeoutMs      int    `json:"kvTimeoutMs"`       // default 2500
    ConnectTimeoutMs int    `json:"connectTimeoutMs"`  // default 10000
}
// Secrets: password, caCertPem
```
- Connection-string validation: the scheme must be `couchbase` or `couchbases`, with host list parsing and optional ports. Query parameters are dropped except an allow-list (`network`). **Capella requires `couchbases://`**, which is enforced by validation.
- gocb options:
  ```go
  opts := gocb.ClusterOptions{
      Authenticator: gocb.PasswordAuthenticator{Username: u, Password: p},
      SecurityConfig: gocb.SecurityConfig{TLSRootCAs: pool, TLSSkipVerify: !cfg.TLSVerify},
      TimeoutsConfig: gocb.TimeoutsConfig{ConnectTimeout: …, KVTimeout: …},
  }
  if cfg.Deployment == "capella" { _ = opts.ApplyProfile(gocb.ClusterConfigProfileWanDevelopment) }
  ```
  For Capella, when no CA is provided, the SDK's bundled Capella CA is used.

### Test connection steps
Same response shape as OC-009:
1. `parse` — connection string.
2. `bootstrap` — `gocb.Connect`, then `cluster.WaitUntilReady(timeout, &gocb.WaitUntilReadyOptions{ServiceTypes: []gocb.ServiceType{gocb.ServiceTypeManagement}})`. Errors are classified: `ErrAuthenticationFailure` → `TARGET_AUTH_FAILED`, timeout → `TARGET_UNREACHABLE` (hint: "For Capella, add this server's IP to Allowed IPs"), x509 → `TARGET_TLS`.
3. `services` — `cluster.Ping(&gocb.PingOptions{ServiceTypes: kv, query, mgmt})`, which reports each service's reachability and latency. KV missing is a failure; Query missing is a warning ("document sampling and verification queries unavailable").
4. `version` — the cluster version from the ping and the diagnostics report. It warns if < 7.0 (no collections, so unsupported) and warns if < 7.6 (no KV range-scan sampling).
5. `permissions` — the capability probe (see below).

### Capability probe
Results are cached on the connection and refreshed on test:
```go
type Capabilities struct {
    CanListBuckets, CanCreateBucket, CanManageScopes, CanWriteDocs bool
    RangeScan bool          // server >= 7.6
    QueryService bool
    Deployment string
    BucketsAllowed []string // when user is scoped to specific buckets
}
```
- Self-managed: call the management REST `GET /whoami` via the SDK's HTTP transport, with the same auth and TLS. The roles determine the flags: `admin`/`cluster_admin` → all; `bucket_admin[b]` → scopes on `b`; `data_writer[b:s:c]` → write; and so on.
- Capella: `/whoami` is not exposed, so `CanCreateBucket=false` with the reason "Create buckets in the Capella UI". Scope and collection creation is inferred by trying `CollectionsV2().GetAllScopes()` on the accessible buckets.
- The UI uses the flags to enable or disable actions (OC-013/022) with explanatory tooltips.

### UI
- **API:** the same project-scoped endpoints as OC-009, with role `target`: `POST /connections/test` (unsaved form values), `PUT /projects/{projectId}/connections/target`, `POST /connections/{id}/test` (stored credentials, no re-entry), and `DELETE /projects/{projectId}/connections/target`. The same rules apply: one target per project, and no change while a job is running. Changing the cluster on a project with mappings warns that the selected keyspaces may not exist on the new cluster.
- **UI:** rendered in the project wizard's *Target* step with the same layout as the Source step: a form with **Test connection** and **Save to project** buttons, collapsing after save into a **Target summary card** (Couchbase logo, first host or Capella endpoint, user, deployment, StatusDot, and the actions *Test connection*, *Edit*, and *Browse keyspaces →*). The Couchbase form has a **Deployment** segmented control (*Self-managed* | *Capella*).
  - *Capella* shows helper text "Find this under Connect → Connection String in Capella" and forces `couchbases://`.
  - The field order is connection string, username, password, then Security (CA dropzone and the TLS verify toggle, which is hidden for Capella), then Advanced (timeouts).
- The test checklist adds a **Capabilities** card: ✓ Write documents · ✓ Manage scopes & collections · ✗ Create buckets (reason).

## Acceptance criteria & unit tests

### AC1 — Config validation
**Given** `http://host`, an empty username, or `capella` with `couchbase://`, **then** a 422 with a field error on `connectionString` or `username`. **Given** valid `couchbases://cb.abc.cloud.couchbase.com` with Capella, **then** the config is accepted.

| Test | Location | Verifies |
|---|---|---|
| `TestCouchbaseConfig_AC1_Validation` | `internal/connectors/target/couchbase/config_test.go` | Table-driven |
| `TestParseConnString_AC1_HostsPortsParams` | same | Multi-host, ports, params allow-list |
| `CouchbaseForm.test.tsx` › `AC1: capella forces couchbases scheme hint` | `src/features/connections/CouchbaseForm.test.tsx` | |

### AC2 — gocb options built correctly
**Given** a Capella config, **then** the WAN profile is applied and TLS verification is on. **Given** a CA PEM, **then** `TLSRootCAs` contains it. **Given** `tlsVerify:false`, **then** `TLSSkipVerify` is true and the test result includes the warning `TLS_VERIFY_DISABLED`.

| Test | Location | Verifies |
|---|---|---|
| `TestBuildClusterOptions_AC2_Capella` | `…/couchbase/options_test.go` | |
| `TestBuildClusterOptions_AC2_CustomCA` | same | |
| `TestBuildClusterOptions_AC2_SkipVerifyWarns` | same | |

### AC3 — Test connection steps & classification
**Given** bad credentials, **then** `bootstrap` fails with `TARGET_AUTH_FAILED` and later steps are skipped. **Given** the Query service is missing, **then** `services` is `warn`. **Given** version 6.6, **then** `version` fails with `TARGET_VERSION_UNSUPPORTED`.

| Test | Location | Verifies |
|---|---|---|
| `TestTester_AC3_AuthFailure` | `…/couchbase/tester_test.go` | Uses the `clusterAPI` interface fake over gocb |
| `TestTester_AC3_QueryMissingWarn` | same | |
| `TestTester_AC3_OldVersionFails` | same | |
| `TestClassifyGocbError_AC3_Table` | `…/couchbase/errors_test.go` | |
| `TestTester_AC3_Integration` *(integration)* | `…/tester_integration_test.go` | Couchbase container |

### AC4 — Capability probe
**Given** `/whoami` roles `[{role:"bucket_admin",bucket_name:"app"},{role:"data_writer",bucket_name:"app",scope_name:"*",collection_name:"*"}]`, **then** `CanCreateBucket=false`, `CanManageScopes=true` (for `app` only), `CanWriteDocs=true`, and `BucketsAllowed=["app"]`. **Given** `admin`, **then** all flags are true. **Given** Capella, **then** `CanCreateBucket=false` with a reason.

| Test | Location | Verifies |
|---|---|---|
| `TestCapabilitiesFromRoles_AC4_BucketAdmin` | `…/couchbase/capabilities_test.go` | |
| `TestCapabilitiesFromRoles_AC4_FullAdmin` | same | |
| `TestCapabilities_AC4_CapellaNoBucketCreate` | same | |
| `CapabilitiesCard.test.tsx` › `AC4: renders allowed/denied with reasons` | `src/features/connections/CapabilitiesCard.test.tsx` | |

### AC5 — Session pooling
**Given** two calls for the same connection within the TTL, **then** one `gocb.Connect` happens. **Given** the connection is edited, **then** the next call reconnects. Idle sessions close after 10 minutes.

| Test | Location | Verifies |
|---|---|---|
| `TestSessionPool_AC5_Reuses` | `internal/connectors/target/couchbase/pool_test.go` | Fake connect func counter, fake clock |
| `TestSessionPool_AC5_InvalidatesOnConfigChange` | same | |
| `TestSessionPool_AC5_IdleEviction` | same | |

### AC6 — Stored once per project and reused
**Given** a project without a target, **when** `PUT /projects/{p}/connections/target` succeeds, **then** a connection with `role=target` exists on the project. Keyspace browsing (OC-013), sampling (OC-015), and every job (OC-025) open the **stored** sealed secret without asking the user. *Test connection* on the summary card re-tests the stored credentials. **Given** the Target step form, **then** *Test connection* tests the unsaved values without persisting anything.

| Test | Location | Verifies |
|---|---|---|
| `TestConnectionsService_AC6_TargetUpsertForProject` | `internal/connections/service_test.go` | Role target, unique per project |
| `TestSessionFactory_AC6_OpensStoredSecretForProject` | `internal/connections/sessionfactory_test.go` | Sealer fake Open called with AAD including the project ID |
| `TargetStep.test.tsx` › `AC6: test unsaved values then save and show summary card` | `src/features/projects/TargetStep.test.tsx` | |
| `TargetStep.test.tsx` › `AC6: re-test from summary card sends no password` | same | |

## Implementation notes (as built)
- **Verified against Couchbase Capella:** the integration test (`OC_IT_TARGET_*` → the project's Capella cluster) passes for valid credentials and reports `TARGET_AUTH_FAILED` for a wrong password.
- **Capabilities come from `system:my_user_info`** (the credential's own RBAC roles), which works on both Capella and self-managed clusters, so no management-port `/whoami` call is needed. Capella never allows bucket creation from the SDK.
- **Wrong-password detection:** the SDK reports rejected credentials during bootstrap only as a timeout (`CONNECTION_ERROR`). On a bootstrap timeout, the tester sends one `SELECT 1` to the Query service (SRV-resolved node, TLS always verified with system roots + the Capella root CA + the user's CA); a `401` becomes `TARGET_AUTH_FAILED`.
- **Server version** comes from `SELECT RAW ds_version()`; KV reachability from cluster diagnostics (KV can't be pinged without a bucket).
- The session pool (AC5) is implemented and tested; keyspace browsing (OC-013) is its first user.
