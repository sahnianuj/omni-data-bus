# OC-095 — Oracle schema discovery

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 8 | OC-094 | Not started |

## User story
As a **data engineer**, I want the tool to discover my Oracle schemas (tables, views, keys, relationships, indexes and comments), so that I can explore and map them exactly like a PostgreSQL schema.

## Scope
**In:**
- `Discover` and `EstimateRelations` for Oracle, producing the same `schema.Snapshot` as PostgreSQL (1.0 architecture §6.1). Set-based dictionary queries over `ALL_*` views, or `DBA_*` when readable, never one query per table:
  - Objects: tables (`ALL_TABLES`, excluding `IOT_TYPE = 'IOT_OVERFLOW'`, nested tables, `DR$`/`BIN$` objects), views, materialized views (kind `materialized_view`), partitioned tables (`ALL_PART_TABLES`, partitions not listed separately), external tables (kind `table`, flagged).
  - Columns: type, length, precision and scale, nullable, default, identity (`ALL_TAB_IDENTITY_COLS`), virtual and invisible columns (flagged; invisible excluded by default), comments.
  - Constraints: primary, unique, foreign keys (with multi-column order); disabled or `NOVALIDATE` constraints flagged.
  - Indexes (key columns only; function-based parts reported as `expr`).
  - Row estimate from `NUM_ROWS` with `LAST_ANALYZED`; a missing or stale estimate (older than 30 days) is flagged so the UI can offer an exact count. Size from `DBA_SEGMENTS` when readable.
- Schema scope: Oracle-maintained users are excluded (`ALL_USERS.ORACLE_MAINTAINED = 'Y'`, plus a fixed list for 11g-style dictionaries), and the connection's include list and exclude patterns apply.
- Snapshot diffs (OC-010) and the schema explorer work unchanged.

**Out:** type normalization rules (OC-096), PL/SQL objects (OC-102).

## Acceptance criteria & unit tests

### AC1 — Shape parity with PostgreSQL
**Given** the Oracle and PostgreSQL dev seeds (same `shop` and `sales` model), **when** both are discovered, **then** the snapshots have the same tables, primary keys, unique constraints and foreign keys, after case-folding names.

### AC2 — Oracle specifics
**Given** the seed's partitioned table, identity column, virtual column, invisible column, materialized view and a disabled foreign key, **then** each is reported with the right kind or flag, and the invisible column is absent unless `includeInvisible` is set.

### AC3 — System schemas excluded
**Given** a user who can see `SYS` and `MDSYS` objects, **then** none appear in the snapshot.

### AC4 — Scale
**Given** a generated schema of 2,000 tables × 20 columns, **then** discovery completes in under 60 s and issues a constant number of queries, independent of table count.

| Test | Location | Verifies |
|---|---|---|
| `TestDiscover_AC1_ParityWithPostgres` | `internal/connectors/source/oracle/discover_integration_test.go` | AC1 |
| `TestDiscover_AC2_OracleSpecifics` | same | AC2 |
| `TestDiscover_AC3_SystemSchemas` | same | AC3 |
| `TestDiscover_AC4_Scale` | same (tagged `slow`) | AC4 |
