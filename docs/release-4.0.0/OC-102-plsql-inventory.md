# OC-102 — PL/SQL inventory: discover functions, procedures and packages

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 5 | OC-095 | Not started |

## User story
As a **migration architect**, I want a complete list of the PL/SQL code in my Oracle schemas (what each unit is, its signature, what it touches and whether it compiles), so that I can scope the work of moving that logic to Couchbase.

## Scope
**In:**
- Dictionary reader (no execution), set-based, for the project's schemas:
  - Units from `ALL_OBJECTS`: `FUNCTION`, `PROCEDURE`, `PACKAGE`, `PACKAGE BODY`, `TRIGGER`, `TYPE BODY`, with status (`VALID`/`INVALID`) and last DDL time.
  - Package members and overloads from `ALL_PROCEDURES` (with `DETERMINISTIC`, `PIPELINED`, `AGGREGATE`, `PARALLEL_ENABLE`, `AUTHID`).
  - Signatures from `ALL_ARGUMENTS` (position, name, `IN`/`OUT`/`IN OUT`, type including `%TYPE`/`%ROWTYPE` resolution, defaults present).
  - Source from `ALL_SOURCE` (or `DBA_SOURCE`); wrapped units detected (`wrapped` header) and kept without text.
  - Dependencies from `ALL_DEPENDENCIES`: tables, views, sequences, synonyms (resolved), other units, `SYS` packages (`DBMS_*`, `UTL_*`) and database links.
- Stored as `plsql_objects` ([architecture §3](00-architecture.md#3-domain-model-additions-metadata-store)); a re-scan updates changed units (new `sourceSha256`) and marks their conversions stale.
- **Functions area → Inventory** page: a table with filters (kind, schema, package, status, has-dependencies-on-unmigrated-tables), counts by kind, and a detail drawer with the signature, read-only source (syntax highlighted with the editor added in OC-107, or plain monospace until then), and dependencies linked to the schema explorer. Triggers and type bodies are listed but marked "not converted to UDFs" with the reason (triggers map to Couchbase Eventing, which is out of scope for 4.0).
- Export the inventory as CSV.

**Out:** parsing and ratings (OC-103).

## Acceptance criteria & unit tests

### AC1 — Complete inventory
**Given** the seed's PL/SQL corpus (standalone functions, a package with overloads, a procedure with `OUT` parameters, a trigger, a wrapped function, an invalid function), **then** the inventory lists every unit and member with the right kind, overload number, signature, status and flags.

### AC2 — Dependencies
**Given** a package function that reads `ORDERS`, calls another package and uses `DBMS_LOB`, **then** its dependencies list all three, and `ORDERS` links to the explorer.

### AC3 — Re-scan
**Given** a function changed with `CREATE OR REPLACE` after a scan, **when** the inventory is re-scanned, **then** only that unit's `sourceSha256` changes and its conversions show **Stale**.

| Test | Location | Verifies |
|---|---|---|
| `TestInventory_AC1_Corpus` | `internal/plsql/inventory_integration_test.go` | AC1 |
| `TestInventory_AC2_Dependencies` | same | AC2 |
| `TestInventory_AC3_Rescan` | same | AC3 |
| `InventoryPage.test.tsx` | `web/src/features/functions/InventoryPage.test.tsx` | AC1 (UI) |
