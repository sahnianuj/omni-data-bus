# OC-155 — Source and target connection details in `config.json`

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 5 | OC-051, OC-053, OC-154 | Implemented |

## User story
As an **operator**, I want an exported migration's source and target connection details (host, database, user, password…) in a `config.json` next to `migrate.sh`, so that I can run the same migration against another environment, or with rotated credentials, by editing that file instead of exporting again.

## Background (2026-10-16)
The connection details were baked into the bundle inside `migrate.sh`, and the credentials were sealed with a passphrase. Running the same mappings against staging and then production, or after a password rotation, meant a new export from the tool.

## Scope
**In:**
- Every export zip (both runtimes) holds `config.json` (mode 0600). The "bundle" runtime becomes a zip of `bundle.odb.json` + `config.json` instead of the `.odb.json` alone.
- `config.json` has a `source` section and, for a Couchbase target, a `target` section. Each lists `type`, then the connection's settings flat (e.g. `host`, `port`, `database`, `username`, `sslMode` / `connectionString`, `username`), then its secrets (`password`, and `caCertPem`, `clientCertPem`, `clientKeyPem` when set), as exported from the project.
- The bundle records credential mode `config`: its credentials are in the run's config file. `api/bundle.schema.json` allows it, and `odb bundle inspect` shows "in the run config file (config.json)".
- `odb run`, `run preflight` and `run verify` take `--config FILE`. A section's settings are laid over the bundle's connection config (keys it lists replace the bundle's), and its secrets replace the bundle's. `migrate.sh` passes `config.json` from its own folder (`ODB_CONFIG` overrides the path; in the docker runtime the file's folder is mounted).
- Mistakes are configuration errors (exit 4) that name the field: a missing file for a `config` bundle, invalid JSON, a `type` that differs from the bundle's, an unknown section, or a `target` section for a directory target.
- The bundle, its sha256 and its plan hash don't change when `config.json` is edited, so `resume`, `verify` and importing `report.json` into the tool keep working.

**Out:** changing mappings, the target kind or a directory target's folder through `config.json` (the folder stays `ODB_TARGET_PATH` / `--target-path`); several environments in one file (copy the folder, or point `ODB_CONFIG` at another file).

## Acceptance criteria & unit tests

### AC1 — The export carries `config.json`
**Given** a PostgreSQL → Couchbase project, **when** I export either runtime, **then** the zip holds `config.json` (0600) with the project's source and target settings and secrets, and the bundle's credential mode is `config` with no secret in it.

### AC2 — Edited details are used
**Given** a bundle exported for `localhost/shop` as `omni_ro`, **when** `config.json` says `db.prod/shop_prod` as `migrator` with another password, **then** the run connects with those values, and the plan hash and bundle sha256 are unchanged.

### AC3 — Clear errors
**Given** a `config` bundle, **when** the config file is missing, is not JSON, says `"type": "mysql"`, or adds a `target` section for a directory target, **then** the run stops with exit 4 and a message naming the file and field.

### AC4 — `migrate.sh` passes it
**Given** an exported script, **when** I run `./migrate.sh run` (binary or docker runtime), **then** odb gets `--config` for `config.json` next to the script, or for `ODB_CONFIG` when set.

| Test | Location | Verifies |
|---|---|---|
| `TestExportsAPI_AC1_ZipContents` | `backend/internal/export/handlers_test.go` | AC1 |
| `TestExportsAPI_ConfigFileCarriesCredentials` | same | AC1 |
| `TestRunConfig_AppliesOverBundle` | `backend/internal/headless/runconfig_test.go` | AC2 |
| `TestRunConfig_Errors` | same | AC3 |
| `migrate_run.bats` › `config.json next to the script is passed to odb` | `backend/internal/export/testdata/bats/migrate_run.bats` | AC4 |
| `TestScript_ZipContents` | `backend/internal/export/script_test.go` | AC1 (config.json 0600 in the script zip) |
| `TestReadmeTemplate_ConfigFile` | same | AC4 (script passes `--config`; README explains config.json) |
| `TestBundle_SchemaFileMatchesAPI`, `TestBundle_AC1_SchemaValid` | `backend/internal/export/bundle_test.go` | Schema |
| `TestScriptTemplate_AC2_ShellcheckAllVariants` | `backend/internal/export/script_test.go` | Config-mode scripts in the variant matrix |

The bats case needs bats-core (`make test-scripts` in CI). Its steps were also run by hand on a generated script, with stub `odb`/`docker` and with a real `odb` build: the binary and docker command lines, `ODB_CONFIG`, a missing file (exit 4), a changed `type` (exit 4), and the connection made with config.json's database and user.
