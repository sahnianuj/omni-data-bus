# OC-120 — PostgreSQL replication readiness: checks and setup assistant

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E17 PostgreSQL Change Capture | P0 | 5 | OC-115 | Not started |

## User story
As a **data engineer**, I want the tool to tell me exactly what my PostgreSQL (self-managed or cloud) needs for continuous replication, and give me the commands, so that I can get it ready without reading three manuals.

## Scope
**In:**
- `ChangeSource.Readiness` for PostgreSQL, with pass/warn/fail and a fix for each:
  1. Version ≥ 12 (warn below 13: no `max_slot_wal_keep_size`, no partition-root publishing).
  2. `wal_level = logical` (fail; the fix needs a restart, so the tool never changes it).
  3. Free replication slots and WAL senders.
  4. User can replicate: `REPLICATION` attribute or the provider role.
  5. User can create the publication (database `CREATE` + table ownership) — otherwise the DBA creates it from the snippet.
  6. Every mapped table (driving and related) has a primary key or a suitable replica identity; key templates on non-key columns need `FULL` (OC-117).
  7. `max_slot_wal_keep_size` set (warn when unlimited, with a suggested value).
  8. Failover slots available (PG 17) — info.
- **Provider detection** (from `version()`, settings such as `rds.extensions`, `cloudsql.*`, `azure.*`) and provider-specific instructions per [architecture §6](00-architecture.md#managed-postgresql-support-matrix-to-confirm-in-oc-115): parameter group/flag names, role grants, console steps.
- **Setup assistant** on the Migrate step when Replicate is chosen: the checklist, one combined SQL script for the DBA (copy/download), **Re-check**, and **Create for me** for the publication when check 5 passes ([D7](00-architecture.md#d7--we-still-never-change-a-customers-database-without-consent)), audited.

**Out:** creating the slot (OC-121 does that at start).

## Acceptance criteria & unit tests

### AC1 — Checks
**Given** PostgreSQL containers with `wal_level=replica`, then `logical` with a non-replication user, then fully ready, **then** the checks report fail on 2, fail on 4, then all pass.

### AC2 — Snippet
**Given** two mapped tables without a primary key and a user without publication rights, **then** the script contains the two `ALTER TABLE … REPLICA IDENTITY FULL` statements and the `CREATE PUBLICATION` for exactly the mapped tables.

### AC3 — Provider instructions
**Given** a server reporting RDS settings (simulated), **then** the instructions name `rds.logical_replication` and `rds_replication`.

| Test | Location | Verifies |
|---|---|---|
| `TestReadiness_AC1` | `internal/connectors/source/postgres/cdc/readiness_integration_test.go` | AC1 |
| `TestReadiness_AC2_Script` | same | AC2 |
| `TestProvider_AC3_RDS` | `internal/connectors/source/postgres/cdc/provider_test.go` | AC3 |
| `ReplicationSetup.test.tsx` | `web/src/features/replication/ReplicationSetup.test.tsx` | UI |
