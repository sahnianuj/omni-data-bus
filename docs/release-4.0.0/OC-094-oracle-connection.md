# OC-094 — Oracle connection: configure, test, save

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 5 | OC-091, OC-093 | Not started |

## User story
As a **data engineer**, I want to set my project's source to an Oracle database, test that the tool can reach it and read what it needs, and save it, so that I can migrate Oracle tables the same way I migrate PostgreSQL ones.

## Scope
**In:**
- Source connector `oracle` in the registry and `GET /connectors`, with the config in [architecture §7](00-architecture.md#7-connection-config-type-oracle). Validation without network access.
- **Test connection** checks, each pass/warn/fail with a message users can act on:
  1. Reachable (TCP, TLS or mTLS with the wallet; `ORA-12514`/`ORA-12505` → "service or SID not known to the listener").
  2. Authenticated (`ORA-01017` → `CONNECTION_AUTH_FAILED`; expired or locked account named as such).
  3. Server: version and edition; warn below 19c, fail below 12.1. Multitenant: warn when connected to `CDB$ROOT` instead of a PDB.
  4. Catalog access: can read `ALL_*` (required) and `DBA_*` (optional; better row estimates and sizes).
  5. Consistent reads: `FLASHBACK` on the tables (or `FLASHBACK ANY TABLE`) → pass; otherwise warn "one reader per table, still consistent" ([D2](00-architecture.md#d2--consistency-comes-from-one-scn)).
  6. PL/SQL access: can read `ALL_SOURCE`/`ALL_ARGUMENTS` for the selected schemas (needed by the Functions area); warn if not.
- **Privilege report** (existing `PrivilegeReport`): unreadable tables, plus a **grant snippet** the DBA can run (D4). We never run it.
- UI: an **Oracle** card in the Source step's connector picker; `OracleForm` with mode (service / SID / descriptor), host, port, service, user, password, TLS mode and wallet dropzone (reusing the certificate dropzone), and an EZConnect paste box that fills the fields (like `parsePgUri`). Summary card fields for the saved repository.
- Sessions set `NLS_SORT=BINARY`, `NLS_COMP=BINARY`, `TIME_ZONE='+00:00'` and a `MODULE` of `omni-data-bus` (visible in `V$SESSION`).

**Out:** discovery (OC-095), reads (OC-099).

## Acceptance criteria & unit tests

### AC1 — Config validation
**Given** `mode: sid` without `sid`, a port of 0, `mtls` without a wallet, or a descriptor with a host the egress policy blocks, **then** each is rejected with a field-level `VALIDATION_FAILED` message.

### AC2 — Test connection
**Given** the Oracle Free container and a user with `CREATE SESSION` and `SELECT` on the seed schema, **then** checks 1–4 pass and 5 warns. **Given** a wrong password, **then** check 2 fails with `CONNECTION_AUTH_FAILED` and later checks are skipped. **Given** a wrong service name, **then** check 1 fails with the listener hint.

### AC3 — Grant snippet
**Given** a user missing `SELECT` on two seed tables, **then** the privilege report lists them and the snippet contains exactly the two `GRANT SELECT` statements.

### AC4 — UI
**Given** the Source step, **then** Oracle is offered beside PostgreSQL; pasting `db.example.com:1522/ORCLPDB1` fills host, port and service; saving a passing connection unlocks Target.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC1` | `internal/connectors/source/oracle/config_test.go` | AC1 |
| `TestTest_AC2_*` | `internal/connectors/source/oracle/test_test.go` (integration) | AC2 |
| `TestPrivileges_AC3_GrantSnippet` | `internal/connectors/source/oracle/privileges_test.go` (integration) | AC3 |
| `OracleForm.test.tsx` › `OC-094 AC4` | `web/src/features/connections/OracleForm.test.tsx` | AC4 |
