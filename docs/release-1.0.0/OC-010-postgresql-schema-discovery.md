# OC-010 — PostgreSQL schema discovery

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E2 Projects & Source | P0 | 8 | OC-009 | Implemented |

## User story
As a **data engineer**, I want the application to discover every schema, table, view, column, key, and index in my source database, with types and size estimates, so that I can pick what to migrate and the mapper has the metadata it needs.

## Implementation details

### Discovery queries (`internal/connectors/source/postgres/discover.go`)
All of these run in **one read-only REPEATABLE READ transaction** so the result is consistent. Everything is filtered to non-system schemas (`nspname NOT IN ('pg_catalog','information_schema') AND nspname NOT LIKE 'pg_toast%' AND nspname NOT LIKE 'pg_temp_%'`), plus the optional `DiscoverOptions.Schemas` include list and `ExcludePatterns` (glob).

1. **Server info:** `SELECT version(), current_database(), current_user`, plus `server_version_num`.
2. **Relations:** `pg_class` joined to `pg_namespace` for relkind in `r, p, v, m, f` (table, partitioned table, view, materialized view, foreign table). It collects `reltuples` (the estimate; `-1` means never analyzed, which becomes null plus a flag `statsStale:true`), `pg_total_relation_size(oid)`, `obj_description(oid)`, and `relispartition` (partitions are nested under their parent and hidden by default).
3. **Columns:** `pg_attribute` (attnum > 0, not dropped) joined to `pg_type`, with `format_type(atttypid, atttypmod)` for the display type, plus `attnotnull`, `pg_get_expr(adbin, adrelid)` for defaults, `attidentity`, `attgenerated`, and `col_description`. It collects the element type for arrays (`typelem`), `typtype='e'` enum labels (one aggregated query on `pg_enum`), and `typtype='d'` domains resolved to their base type.
4. **Constraints:** `pg_constraint` with contype `p`, `u`, and `f` (columns resolved via `unnest(conkey) WITH ORDINALITY` to keep order), `confrelid`, and `confkey`. This produces `primaryKey`, `uniqueConstraints`, `foreignKeys`, and the reverse index `referencedBy`.
5. **Indexes:** `pg_index` + `pg_class`, with column names (expression indexes shown as `expr`).

- Each column gets a `jsonType` via `postgres.NormalizeType(udtName, typtype, elemType)`, following [architecture §6.4](00-architecture.md#64-logical-type-normalization-postgresql--jsontype). Unsupported types get `jsonType:"string"` plus `warning:"UNSUPPORTED_TYPE_AS_TEXT"`.
- Safety: `statement_timeout = 60s` for discovery, and no user input is interpolated (the schema filter is passed as a `text[]` parameter).
- Large catalogs: the queries are set-based (≈6 queries in total regardless of table count), never one query per table.

### Service & storage (`internal/discovery`)
- `POST /api/v1/connections/{id}/discover` runs synchronously when the estimated relation count is ≤ 2,000, and otherwise returns `202 {discoveryId}` and runs in a goroutine with the status polled at `GET /connections/{id}/discoveries/{did}`. Only one discovery runs per connection at a time (advisory lock `hashtext(connection_id)`); a second request gets the in-flight ID.
- The snapshot is persisted in `discovery_snapshots` with `is_latest=true`, and the previous latest is flipped to false. At most 5 snapshots are kept per connection.
- `GET /connections/{id}/schema` returns the latest snapshot (ETag = snapshot ID, supports `If-None-Match`). `?summary=true` returns only schemas → tables (name, kind, rowEstimate, sizeBytes, columnCount) for fast tree loading.
- `GET /connections/{id}/schema/{schema}/{table}` returns the full table detail.
- **Diff:** when re-discovering, it computes `SnapshotDiff{addedTables, removedTables, changedTables:[{table, addedColumns, removedColumns, typeChanged}]}` against the previous snapshot. This is returned in the response and used by mappings to flag drift (OC-022).

## Acceptance criteria & unit tests

### AC1 — Complete metadata
**Given** the seed `shop` schema, **when** discovery runs, **then** the snapshot lists every table and view with kind, rowEstimate, and sizeBytes; each column has name, ordinal, dataType, udtName, nullable, default, isPrimaryKey, and jsonType; and tables have `primaryKey`, `uniqueConstraints`, `foreignKeys` (with ordered multi-column keys), `referencedBy`, and `indexes`.

| Test | Location | Verifies |
|---|---|---|
| `TestAssembleSnapshot_AC1_FromRawRows` | `internal/connectors/source/postgres/discover_test.go` | Pure function assembling the snapshot from fixture query rows (no DB) |
| `TestAssembleSnapshot_AC1_CompositeFKOrderPreserved` | same | |
| `TestAssembleSnapshot_AC1_ReferencedByComputed` | same | |
| `TestDiscover_AC1_SeedSchema` *(integration)* | `…/discover_integration_test.go` | Golden JSON comparison |

### AC2 — Type normalization
**Given** each PostgreSQL type in §6.4 (including arrays, enums, domains over numeric, and `numeric(38,10)`), **then** `jsonType` and the warnings match the table.

| Test | Location | Verifies |
|---|---|---|
| `TestNormalizeType_AC2_Matrix` | `internal/connectors/source/postgres/types_test.go` | One case per row of §6.4 |
| `TestNormalizeType_AC2_DomainResolvesToBase` | same | |
| `TestNormalizeType_AC2_ArrayOfEnum` | same | |
| `TestNormalizeType_AC2_UnsupportedWarns` | same | `tsvector` |

### AC3 — System schemas and filters
**Given** default options, **then** `pg_catalog`, `information_schema`, `pg_toast*`, and `pg_temp_*` are excluded. **Given** `schemas:["shop"]` and `exclude:["*_archive"]`, **then** only matching tables are returned.

| Test | Location | Verifies |
|---|---|---|
| `TestDiscoverOptions_AC3_ExcludeGlob` | `internal/connectors/source/postgres/discover_test.go` | |
| `TestDiscoverSQL_AC3_SystemSchemaPredicate` | same | Query text contains the predicate; filter passed as a param |

### AC4 — Stale statistics
**Given** a never-analyzed table (`reltuples = -1`), **then** `rowEstimate:null, statsStale:true`, and the UI shows "Estimate unavailable — run ANALYZE" (OC-011).

| Test | Location | Verifies |
|---|---|---|
| `TestAssembleSnapshot_AC4_NegativeReltuples` | `…/discover_test.go` | |

### AC5 — Async discovery for large catalogs & single flight
**Given** a relation count > 2,000, **then** the API returns 202 with `discoveryId`, and polling reaches `completed` with the snapshot ID. **Given** a concurrent second request, **then** it returns the same `discoveryId`.

| Test | Location | Verifies |
|---|---|---|
| `TestDiscoveryService_AC5_AsyncAboveThreshold` | `internal/discovery/service_test.go` | Fake session |
| `TestDiscoveryService_AC5_SingleFlight` | same | Two goroutines, one Discover call |

### AC6 — Snapshot persistence, ETag & retention
**Given** a new snapshot, **then** it is marked latest, the previous is un-marked, and only 5 are retained. `GET /schema` with a matching `If-None-Match` returns 304.

| Test | Location | Verifies |
|---|---|---|
| `TestDiscoveryService_AC6_LatestFlagAndRetention` | `internal/discovery/service_test.go` | |
| `TestSchemaHandler_AC6_ETag304` | `internal/httpapi/schema_test.go` | |
| `TestSchemaHandler_AC6_SummaryShape` | same | `summary=true` omits columns |

### AC7 — Drift diff
**Given** a previous snapshot and a new one where table `x` gained a column, `y` was dropped, and `z.col` changed from `int4` to `int8`, **then** the diff lists exactly those changes.

| Test | Location | Verifies |
|---|---|---|
| `TestDiff_AC7_DetectsAddRemoveTypeChange` | `internal/discovery/diff_test.go` | |
| `TestDiff_AC7_NoChangesEmpty` | same | |

### AC8 — Performance
**Given** a synthetic catalog of 1,000 tables × 20 columns, **then** assembly completes in < 500 ms, and end-to-end discovery (integration) completes in < 10 s.

| Test | Location | Verifies |
|---|---|---|
| `BenchmarkAssembleSnapshot_AC8_1000Tables` | `…/discover_bench_test.go` | Fails when > 500 ms/op via `b.ReportMetric` + threshold check helper |
| `TestDiscover_AC8_LargeCatalog` *(integration)* | `…/discover_integration_test.go` | |

## Implementation notes (as built)
- **Model:** the snapshot types (§6.1) live in the leaf package `internal/schema`, shared by connectors, discovery, and (later) the mapper. The HTTP handlers live in the feature package `internal/discovery` (not `internal/httpapi`), like every other feature; `TestSchemaHandler_AC6_*` are in `internal/discovery/service_test.go`.
- **Storage (Couchbase):** one document per snapshot in `discovery_snapshots`, plus a head document `latest::<connectionId>` listing the connection's snapshot IDs newest first. The head is updated with CAS and is the single source of truth for `isLatest`, so "latest" and the 5-snapshot retention need no index. Payloads can be megabytes, so they are written outside transactions. Deleting a connection or its project deletes its snapshots.
- **Single flight:** discovery runs are tracked in memory (the "advisory lock" in the original design assumed a PostgreSQL metadata store). A second request for the same connection joins the in-flight run. Finished runs can be polled for 15 minutes. Multi-node deployments will need a lease in `leases` (OC-032).
- **Additions:** each table has `readable` (false when the user can't SELECT it, e.g. `billing` for `omni_ro`); partitions are nested under their parent (`partitions`), whose estimate and size are the partitions' sums; views report no estimate without being flagged stale; arrays report `elementType`. A missing snapshot returns 404 `SCHEMA_NOT_DISCOVERED`. `POST …/discover` is editor-only; reading the schema is open to viewers. Discovery is audited as `connection.discover`.
- **Measured:** assembly of 1,000 tables × 20 columns takes ~8 ms; end-to-end discovery of 1,000 tables takes ~0.3 s on PostgreSQL 16 (budget 10 s).

