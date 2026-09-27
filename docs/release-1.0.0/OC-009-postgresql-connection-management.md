# OC-009 — PostgreSQL source repository (per project): configure, test, save

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E2 Projects & Source | P0 | 8 | OC-005, OC-006, OC-007, OC-008 | Implemented |

## User story
As a **data engineer**, I want to set up my project's PostgreSQL source repository, test it with a **Test connection** button that gives clear feedback, and save it once to the project, so that every mapping and migration in the project reuses those credentials without asking me again.

## Implementation details

### Config model
```go
type PostgresConfig struct {
    Host           string `json:"host" validate:"required,hostname_rfc1123|ip"`
    Port           int    `json:"port" validate:"min=1,max=65535"`          // default 5432
    Database       string `json:"database" validate:"required,max=63"`
    Username       string `json:"username" validate:"required,max=63"`
    SSLMode        string `json:"sslMode" validate:"oneof=disable prefer require verify-ca verify-full"` // default require
    ApplicationName string `json:"applicationName"`                        // default "omni-data-bus"
    ConnectTimeoutSec int `json:"connectTimeoutSec" validate:"min=1,max=60"` // default 10
    Options        map[string]string `json:"options"` // allow-listed extra params: search_path, statement_timeout
}
// Secrets (sealed, OC-006): password, caCertPem, clientCertPem, clientKeyPem
```
- A **paste a connection URI** helper (`postgres://user:pass@host:5432/db?sslmode=require`) is parsed client-side into the form fields. The password goes to the secret input, and the URI is never sent as-is.
- Server-side, `pgx.ParseConfig` is **never** fed a user-provided raw string. The `pgconn.Config` is built field by field, with `TLSConfig` built from the sslmode and the certs, and with `DialFunc` wrapped by `netguard` (OC-033).
- Session defaults set on connect: `default_transaction_read_only=on`, `statement_timeout=30s` for metadata queries (the pipeline overrides this, see OC-024), and `application_name`.

### Test connection
`POST /api/v1/connections/test` tests unsaved config (with secrets in the body), and `POST /api/v1/connections/{id}/test` tests a saved connection. Both run these steps and return a step list:
```json
{ "ok": false, "latencyMs": 42,
  "steps": [
    {"id":"resolve","label":"Resolve host","status":"ok","ms":3},
    {"id":"connect","label":"Open TCP/TLS connection","status":"ok","ms":18},
    {"id":"auth","label":"Authenticate","status":"ok","ms":15},
    {"id":"version","label":"Server version","status":"ok","detail":"PostgreSQL 16.3"},
    {"id":"privileges","label":"Read privileges","status":"warn",
     "detail":"No SELECT on 3 tables in schema 'billing'","hint":"GRANT SELECT ON ALL TABLES IN SCHEMA billing TO omni_ro;"}
  ]}
```
- The overall timeout is `connectTimeoutSec + 5s`. Steps after a failure are `skipped`.
- **Privileges check:** it counts the tables where `has_table_privilege(current_user, oid, 'SELECT')` is false, across non-system schemas. If the role is a superuser or has write privileges, a warning (not a failure) suggests using a read-only role.
- The connection record stores `last_test_status` and `last_tested_at`.

### Project-scoped API
The source connection belongs to exactly one project ([architecture §5](00-architecture.md#5-domain-model-metadata-db)). There is no workspace-level connection list.

| Method | Path | Notes |
|---|---|---|
| POST | `/connections/test` | Tests **unsaved** config and secrets from the form. Nothing is persisted. Used by the *Test connection* button before the first save |
| PUT | `/projects/{projectId}/connections/source` | `{type:"postgres", name?, config, secrets}`. Creates the project's source, or replaces its config (same connection ID). Omitted secrets keep the stored ones (OC-006). Returns 200/201 without secrets |
| GET | `/projects/{projectId}/connections` | `{source?, target?}`, each with status and last test (no secrets) |
| GET | `/connections/{id}` | Single connection (no secrets) |
| POST | `/connections/{id}/test` | Re-tests the **stored** credentials. The user doesn't re-enter the password |
| DELETE | `/projects/{projectId}/connections/source` | Removes the source (requires typed confirmation in the UI). 409 `PROJECT_HAS_RUNNING_JOB` while a job runs |

- **Changing a saved source.** Editing host, port, or database on a project that already has mappings shows a warning: "Mappings in this project were built against `shop@db1`. Changing the database will re-run discovery and may mark mappings stale." After save, discovery (OC-010) re-runs automatically and the drift diff marks affected mappings `stale` (OC-007).
- PUT is rejected with 409 `PROJECT_HAS_RUNNING_JOB` while a job in the project is running, so credentials cannot change under a live migration.
- `name` defaults to `"<database>@<host>"`.

Audit events: `connection.create`, `connection.update`, `connection.delete`, and `connection.test`, each carrying `projectId`.

### Frontend (`src/features/connections`, rendered inside the project wizard's *Source* step)
- **Source step layout:**
  - If no source is configured: the ConnectorPicker cards (PostgreSQL enabled; others "Coming soon").
  - Then the **PostgresForm** with the sections *Connection* (host, port, database, username, password), *Security* (SSL mode segmented control; CA / client cert dropzones appear for `verify-*`), and *Advanced* (collapsed: timeout, application name, options).
  - *Paste connection string* link above the form.
- **Test connection button** (secondary button next to *Save*): calls `/connections/test` with the current form values, then renders a live checklist (steps animate from pending to ok, warn, or fail) with hints and a copy button for SQL hints. The button is available while creating **and** while editing.
- **Save to project** (primary): persists config and sealed secrets on the project. Saving an untested or failed configuration asks for confirmation. After save the step collapses into a **Source summary card**: DB logo, `database@host:port`, user, SSL mode, and a StatusDot (Connected / Failed / Untested with relative time). It has the actions *Test connection* (re-tests the stored credentials with no password prompt), *Edit*, and *Explore schema →*.
- On edit, secret inputs show `•••••••• (stored)` with *Change* (OC-006), so the user never needs to retype a password that is already saved.
- A successful save shows the toast "Source saved to project" and enables the *Next: Target* button.

## Acceptance criteria & unit tests

### AC1 — Config validation
**Given** an invalid port (0 or 70000), an empty host, or an unknown sslMode, **when** saving or testing, **then** a 422 with field errors is returned, and the form shows inline errors without a network call for client-detectable cases.

| Test | Location | Verifies |
|---|---|---|
| `TestPostgresConfig_AC1_Validation` | `internal/connectors/source/postgres/config_test.go` | Table-driven |
| `TestPostgresConfig_AC1_Defaults` | same | port 5432, sslMode require, timeout 10 |
| `PostgresForm.test.tsx` › `AC1: zod errors shown inline` | `src/features/connections/PostgresForm.test.tsx` | |

### AC2 — Connection string paste
**Given** `postgresql://app:s3cr%40t@db.example.com:6543/shop?sslmode=verify-full`, **when** it is pasted, **then** the host, port, database, username, and sslMode fields are filled, the password field holds `s3cr@t` (URL-decoded), and the raw URI is not kept in state.

| Test | Location | Verifies |
|---|---|---|
| `parsePgUri.test.ts` › `AC2: parses all parts incl. encoded password` | `src/features/connections/parsePgUri.test.ts` | |
| `parsePgUri.test.ts` › `AC2: rejects non-postgres scheme` | same | |
| `parsePgUri.test.ts` › `AC2: supports IPv6 host [::1]` | same | |

### AC3 — pgx config is built safely from fields
**Given** a config and secrets, **then** the built `pgconn.Config` has the exact host, port, database, and user, TLS matching the sslMode (`disable` → nil TLS; `require` → `InsecureSkipVerify` with no hostname check; `verify-ca` → RootCAs set with a custom verify that skips the hostname check; `verify-full` → RootCAs + ServerName), `RuntimeParams` containing `default_transaction_read_only=on` and `application_name`, and extra options only from the allow-list.

| Test | Location | Verifies |
|---|---|---|
| `TestBuildConnConfig_AC3_FieldsCopied` | `internal/connectors/source/postgres/connconfig_test.go` | |
| `TestBuildConnConfig_AC3_TLSModes` | same | Table over 5 modes |
| `TestBuildConnConfig_AC3_RejectsNonAllowlistedOption` | same | `options: {"host":"evil"}` → error |
| `TestBuildConnConfig_AC3_ReadOnlyDefault` | same | |

### AC4 — Test connection reports steps
**Given** reachable DB, valid credentials, and full SELECT privileges, **then** `ok:true` and every step is `ok`. **Given** wrong credentials, **then** `auth` is `fail` with code `SOURCE_AUTH_FAILED`, and `version` and `privileges` are `skipped`. **Given** missing SELECT on some tables, **then** `privileges` is `warn` with a GRANT hint and `ok:true`.

| Test | Location | Verifies |
|---|---|---|
| `TestTester_AC4_AllOK` | `internal/connectors/source/postgres/tester_test.go` | Uses a `session` fake |
| `TestTester_AC4_AuthFailSkipsRest` | same | |
| `TestTester_AC4_PrivilegeWarning` | same | |
| `TestTester_AC4_SuperuserWarning` | same | |
| `TestTester_AC4_Integration` *(integration)* | `…/tester_integration_test.go` | Real PG container: read-only role vs superuser |
| `TestChecklist.test.tsx` › `AC4: renders steps with statuses and hints` | `src/features/connections/TestChecklist.test.tsx` | |

### AC5 — Stored once per project and reused
**Given** a project without a source, **when** `PUT /projects/{p}/connections/source` succeeds, **then** a connection with `projectId=p, role=source` exists and the response has no secrets. **Given** a second PUT without `secrets.password`, **then** the same connection ID is kept, the stored password is unchanged, and `lastTestStatus` resets to `untested` if the config changed. **Given** `POST /connections/{id}/test` with no body, **then** the stored secret is used. **Given** a running job, **then** the PUT returns 409 `PROJECT_HAS_RUNNING_JOB`.

| Test | Location | Verifies |
|---|---|---|
| `TestConnectionsService_AC5_UpsertCreatesForProject` | `internal/connections/service_test.go` | |
| `TestConnectionsService_AC5_UpsertKeepsIDAndSecret` | same | |
| `TestConnectionsService_AC5_ConfigChangeResetsTestStatus` | same | |
| `TestConnectionsService_AC5_TestStoredUsesSealedSecret` | same | Sealer fake asserts Open called; request had no password |
| `TestConnectionsService_AC5_BlockedWhileJobRunning` | same | |
| `TestConnectionsService_AC5_CrossProjectAccessNotFound` | same | Connection of project A via project B's route → 404 |
| `SourceStep.test.tsx` › `AC5: summary card after save with re-test using stored credentials` | `src/features/projects/SourceStep.test.tsx` | MSW asserts the test request has no password |
| `SourceStep.test.tsx` › `AC5: changing database on project with mappings shows stale warning` | same | |

### AC6 — Save flow UX
**Given** the user clicks *Test connection* in the form, **then** the unsaved values are tested and nothing is persisted. **Given** the user clicks *Save to project* without a successful test, **then** a confirm dialog "Save without a successful test?" appears. **Given** save succeeds, **then** the form collapses to the summary card, the toast "Source saved to project" appears, and *Explore schema →* navigates to `/projects/{p}/source/schema`.

| Test | Location | Verifies |
|---|---|---|
| `SourceStep.test.tsx` › `AC6: test button posts unsaved config and persists nothing` | `src/features/projects/SourceStep.test.tsx` | |
| `SourceStep.test.tsx` › `AC6: confirms when untested` | same | |
| `SourceStep.test.tsx` › `AC6: collapses to summary card and links to schema` | same | |

### AC7 — Audit
**Given** create, update, delete, or test, **then** an `audit_log` row with the action, connection ID, and project ID is written, and it contains no secrets.

| Test | Location | Verifies |
|---|---|---|
| `TestConnectionsService_AC7_AuditEvents` | `internal/connections/service_test.go` | Audit fake captures events; JSON contains no password |

## Implementation notes (as built)
- The pgx config is built field by field from a fixed, trusted base DSN; user input is never parsed as a connection string. `sslmode=verify-ca` verifies the chain in `VerifyConnection` (which also runs on resumed TLS sessions).
- The privileges step checks both `has_schema_privilege(…, 'USAGE')` and `has_table_privilege(…, 'SELECT')`, and the hint grants both.
- Saving never trusts a client-supplied test result: `PUT` stores the settings as `untested`, then the UI calls `POST /connections/{id}/test`, which tests with the stored credentials and records `ok` / `warn` / `failed`.
- Path IDs such as `p1::source` arrive percent-encoded from browsers; handlers decode them with `httpapi.URLParam`.
- **Verified against real PostgreSQL 16:** `TestTester_AC4_Integration` passes both against the dev stack's seeded `source-pg` (`OC_IT_PG_HOST=localhost OC_IT_PG_PORT=5434`) and against a fresh testcontainers PostgreSQL (`make test-integration`).
