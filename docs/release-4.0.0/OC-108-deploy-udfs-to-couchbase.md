# OC-108 — Deploy UDFs to Couchbase: libraries, functions, drift and export

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 5 | OC-092, OC-107 | Not started |

## User story
As a **developer**, I want to deploy converted functions to my Couchbase target, see what's deployed and whether someone changed it, and take the deployment scripts with me, so that the UDFs are in place where the migrated data lives.

## Scope
**In:**
- Deployment scope: the bucket and scope of the project's target (default: the scope most mappings write to; choosable).
- Deploy one or many units: upload or update the `odb_plsql` library and each unit's JS library (scoped libraries), then `CREATE OR REPLACE FUNCTION`. Dependency order (callee before caller). All-or-nothing per request: on failure, previously deployed versions are restored.
- Preflight: target is Couchbase ([D7](00-architecture.md#d7--udfs-need-a-couchbase-target)), the server supports the UDF kind (spike matrix), the user has the function management roles, name collisions with functions not created by the tool (refuse unless "replace" is ticked).
- `udf_deployments` records; the Convert list shows **Deployed vN**. **Drift check** (on page open and on demand) compares the deployed definition and library hash with the record: "changed outside the tool" with diff and "Redeploy" or "Adopt" (adopt saves the deployed code as a manual version).
- Optional index creation for the OC-106 index advice, one click per suggestion, with the statement shown first.
- Undeploy (drop function; drop library when unused).
- **Export**: a `udfs/` folder in the portable bundle (OC-050) and a standalone ZIP: `odb_plsql.js`, one `.js` per library, `functions.sqlpp`, `indexes.sqlpp`, and a `deploy-udfs.sh` that uses `cbq`/`curl` with the same order and checks. Tested by bats.
- Audit events for deploy, undeploy, adopt.

**Out:** running (OC-109).

## Acceptance criteria & unit tests

### AC1 — Deploy and call
**Given** three converted units where one calls another, **when** deployed together, **then** all three exist in the scope, and `SELECT RAW scope.fn(...)` returns the expected values.

### AC2 — Atomic rollback
**Given** a deploy of two units where the second has a syntax error the server rejects, **then** the first is restored to its previous version (or dropped if new) and the error names the second unit and line.

### AC3 — Drift
**Given** a deployed function replaced by hand in the Query Workbench, **then** the drift check reports it with a diff, and **Adopt** creates a manual version equal to the deployed code.

### AC4 — Exported script
**Given** the export ZIP, **when** `deploy-udfs.sh` runs against an empty scope, **then** the functions behave like the tool-deployed ones (AC1 calls).

| Test | Location | Verifies |
|---|---|---|
| `TestDeploy_AC1_DependencyOrder` | `internal/plsql/deploy/deploy_integration_test.go` | AC1 |
| `TestDeploy_AC2_Rollback` | same | AC2 |
| `TestDeploy_AC3_Drift` | same | AC3 |
| `deploy_udfs.bats` | `backend/internal/plsql/deploy/testdata/bats/deploy_udfs.bats` | AC4 |
