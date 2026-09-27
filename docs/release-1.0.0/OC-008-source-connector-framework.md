# OC-008 — Source connector framework

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E2 Projects & Source | P0 | 3 | OC-002 | Implemented |

## User story
As an **engineer**, I want a pluggable source-connector abstraction, so that PostgreSQL ships in 1.0 and MySQL, SQL Server, or Oracle can be added later without changes to discovery, mapping, or the migration pipeline.

## Implementation details

### Package `internal/connectors/source`
```go
type Type string // "postgres" (1.0); reserved: "mysql", "sqlserver", "oracle"

type Descriptor struct {
    Type        Type
    DisplayName string        // "PostgreSQL"
    Icon        string        // key used by the UI icon registry
    ConfigSchema json.RawMessage // JSON Schema for the config form (drives dynamic UI later)
    Capabilities Capabilities
}

type Capabilities struct {
    SnapshotExport     bool // consistent multi-reader snapshot
    KeysetPagination   bool
    RangePartitioning  bool
    RowEstimate        bool
}

type Connector interface {
    Descriptor() Descriptor
    // Validate config shape without network access.
    Validate(cfg Config) error
    // Open a session; caller must Close.
    Open(ctx context.Context, cfg Config, secrets Secrets) (Session, error)
}

type Session interface {
    Ping(ctx context.Context) (ServerInfo, error)             // version, current user, db name, latency
    CheckPrivileges(ctx context.Context) (PrivilegeReport, error)
    Discover(ctx context.Context, opts DiscoverOptions) (*discovery.Snapshot, error)
    Preview(ctx context.Context, t TableRef, limit int) (*RowSet, error)
    Estimate(ctx context.Context, t TableRef) (Estimate, error)   // rows (approx + exact?), bytes
    ExactCount(ctx context.Context, t TableRef) (int64, error)
    NewReader(ctx context.Context, spec ReadSpec) (Reader, error) // OC-024
    Close() error
}

type Reader interface {
    // Next returns the next batch; io.EOF when done. Batch rows are []any aligned to spec.Columns.
    Next(ctx context.Context) (*Batch, error)
    Close() error
}

type Batch struct {
    Seq       uint64          // monotonically increasing per partition
    Partition int
    Rows      [][]any
    LastKey   []any           // keyset position after this batch (for checkpoint)
}
```
- `Registry` maps `Type → Connector` and is populated in `app` wiring. `Registry.Get(t)` returns `ErrUnsupportedType`, which maps to 422 `SOURCE_TYPE_UNSUPPORTED`.
- `GET /api/v1/connectors` returns the descriptors for both source and target (target registry in OC-012). The UI uses it to render the "Choose a database" picker cards. Upcoming types are shown as disabled "Coming soon" cards, driven by a static list in the UI.
- Error classification: connectors wrap driver errors in `ConnError{Kind, Cause}`, where `Kind ∈ {Unreachable, DNS, Timeout, TLS, Auth, Database NotFound, Permission, Unknown}`. Each kind maps to a stable problem code and a human hint, so the UI can show "Check that the host is reachable from the Omni-Data-Bus server" instead of raw driver text. The raw text is kept in `detail`.
- `Estimate` is used by discovery (row estimate) and by progress (total).

### Fake connector for tests
`internal/connectors/source/sourcetest.Fake` is an in-memory implementation: tables are defined in Go, `Reader` yields deterministic batches, and it can inject errors at batch N. The pipeline, discovery-service, and API tests use it.

## Acceptance criteria & unit tests

### AC1 — Registry resolves connectors
**Given** a registry with `postgres`, **when** `Get("postgres")` is called, **then** the connector is returned. **When** `Get("mysql")` is called, **then** `ErrUnsupportedType` is returned. Registering the same type twice panics at startup.

| Test | Location | Verifies |
|---|---|---|
| `TestRegistry_AC1_GetRegistered` | `internal/connectors/source/registry_test.go` | |
| `TestRegistry_AC1_UnknownType` | same | |
| `TestRegistry_AC1_DuplicatePanics` | same | |

### AC2 — Connector catalog API
**Given** the registries, **when** `GET /api/v1/connectors` is called, **then** it returns `{sources:[{type:"postgres", displayName:"PostgreSQL", capabilities…}], targets:[{type:"couchbase", …}]}` sorted by display name.

| Test | Location | Verifies |
|---|---|---|
| `TestConnectorsHandler_AC2_ListsDescriptors` | `internal/httpapi/connectors_test.go` | |
| `ConnectorPicker.test.tsx` › `AC2: renders available and coming-soon cards` | `src/features/connections/ConnectorPicker.test.tsx` | |

### AC3 — Error classification
**Given** driver errors (e.g., `*net.OpError` dial refused, `*net.DNSError`, `context.DeadlineExceeded`, `x509.UnknownAuthorityError`, and PG SQLSTATE `28P01` / `3D000` / `42501`), **when** classified, **then** they map to `Unreachable`, `DNS`, `Timeout`, `TLS`, `Auth`, `DatabaseNotFound`, and `Permission` respectively, with the problem codes `SOURCE_UNREACHABLE`, `SOURCE_DNS`, `SOURCE_TIMEOUT`, `SOURCE_TLS`, `SOURCE_AUTH_FAILED`, `SOURCE_DB_NOT_FOUND`, and `SOURCE_PERMISSION_DENIED`, each with a hint.

| Test | Location | Verifies |
|---|---|---|
| `TestClassify_AC3_TableDriven` | `internal/connectors/source/errors_test.go` | One case per kind |
| `TestConnErrorProblem_AC3_HasHintAndDetail` | same | |

### AC4 — Fake connector honors the contract
**Given** the fake with 2,500 rows and batch size 1,000, **then** the reader yields batches of 1000, 1000, and 500 with Seq 1, 2, 3 and increasing `LastKey`, then `io.EOF`. **Given** an injected error at batch 2, `Next` returns it on the second call.

| Test | Location | Verifies |
|---|---|---|
| `TestFakeReader_AC4_Batches` | `internal/connectors/source/sourcetest/fake_test.go` | |
| `TestFakeReader_AC4_InjectedError` | same | |

## Implementation notes (as built)
- Shared pieces live in `internal/connectors` (descriptors, credentials, the `StepRunner` checklist used by every "Test connection", error classification, and a generic `Registry[T]`); `internal/connectors/source` and `internal/connectors/target` define the role-specific contracts.
- The `Session` interface starts with `Ping`/`CheckPrivileges`/`Close`; discovery, preview and reader methods are added by OC-010, OC-011 and OC-024.
- Classified error codes (`SOURCE_*`/`TARGET_*` × 8 kinds) are listed in `problem.ConnectorCodes` and the OpenAPI `ErrorCode` enum; a test fails if a new kind isn't registered.
