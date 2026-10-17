# OC-158 — `mappings.json`: what the migration does, in its own readable file

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 5 | OC-050, OC-155 | Implemented |

## User story
As an **operator**, I want an export's mappings in a `mappings.json` next to `config.json`, holding what the migration does and nothing about where it runs, so that I can read the source-to-target logic, and run the same mappings anywhere by pairing that file with a different `config.json`.

## Background (2026-10-16)
OC-155 moved connection details into `config.json`, but the mappings still travel inside the bundle (`bundle.odb.json`), and that bundle is base64-encoded inside `migrate.sh`. The bundle also still holds every connection's settings, which `config.json` overrides. Reading what a migration does means running `./migrate.sh extract` and opening a file where each target field is a `fld_…` ID that a field mapping refers to.

## Scope
**In:**
- **Every export holds `mappings.json`** (mode 0644: it has no secrets). It is the bundle (same format, version 1, same schema) without connection settings: `source` and `target` keep their `type` and `name`, with `config: {}` and credential mode `config`. All connection settings are in `config.json`, including a directory target's `path` and `layout`.
- **A readable outline per mapping.** Each mapping in `mappings.json` gets an `outline`: one line per document field, saying where its value comes from. It is generated from the spec, the runner ignores it, and the integrity hash covers it like the rest:
  ```
  "outline": [
    "key = order::{order_id}",
    "orderId ← orders.order_id",
    "customer ← customers (object, customer_id = customer_id)",
    "customer.name ← customers.name",
    "items[] ← order_items (array, order_id = order_id, at most 1000)",
    "items[].quantity ← order_items.quantity | toInteger",
    "source = \"pos\"",
    "type = \"order\""
  ]
  ```
  Sources are `table.column`; relations show their table, shape, join and limit; transforms are listed after `|` in pipeline order; a constant or expression field shows `=`; an unmapped field or a pending suggestion isn't listed.
- **`config.json` also describes a directory target**: `"target": {"type": "directory", "path": "…", "layout": "…"}`. A relative `path` is resolved against the folder `config.json` is in. `--target-path` (`ODB_TARGET_PATH`) still wins.
- **`odb run --mappings FILE`** reads `mappings.json` (`--bundle` stays as another name for it, for bundles exported before this story). `odb bundle validate|inspect|diff` read it too, and `inspect` says the connections are in `config.json`.
- A connection section missing from `config.json` when `mappings.json` has no settings for it is a configuration error (exit 4) that names the file and the section.
- The plan hash and the file's sha256 don't depend on connection settings, since they're not in the file: running against another environment keeps `resume`, `verify` and report import working.
- Bundles exported before this story (with their connection settings) keep running, with or without `--config`.

**Out:** editing `mappings.json` by hand (the integrity check rejects a changed file: export again); parsing the outline; JSON Schema documentation for the spec format.

## Implementation details
- `export.Builder.Build` (config mode): `config.json` is built from the project's connections first, then the bundle's `source.config` and `target.config` are emptied before the content hash and `Finalize`. A directory target gets a `target` section without secrets.
- `mapping.Outline(spec, driving)` (`internal/mapping/outline.go`) builds on the mapping sheet (OC-061), so both describe a mapping the same way: fields in tree order with their sources and transforms, a bound relation with its shape, join and limit, the key template first and the type field last. `Mapping.Outline` is set at build time. `api/bundle.schema.json` (and its copy in `internal/export`) allows `outline: string[]`, and `odb bundle inspect` prints it under each mapping.
- `headless.Config.connection`: an empty bundle config with no section is an error; a directory section is merged like the others, and a relative `path` is joined to the config file's folder.
- `odb run`: `--mappings` and `--bundle` set the same path; usage and errors say `--mappings`.

## Acceptance criteria & unit tests

### AC1 — The export carries `mappings.json` without connection settings
**Given** a PostgreSQL → Couchbase project, **when** I export, **then** the zip holds `mappings.json` (0644) whose `source.config` and `target.config` are `{}`, with credential mode `config`, no host, user or password, and `config.json` holds the settings and secrets. **Given** a directory target, **then** `config.json` has a `target` section with `type`, `path` and `layout`.

| Test | Location | Verifies |
|---|---|---|
| `TestExportsAPI_AC1_ZipContents` | `backend/internal/export/handlers_test.go` | Files and modes; `source.config` and `target.config` are `{}`, no host in mappings.json, an outline per mapping |
| `TestExportsAPI_ConfigFileCarriesCredentials` | same | config.json holds the settings and base64 secrets; mappings.json's credential mode is `config` |
| `TestReadmeTemplate_AC7_Golden` | `backend/internal/export/script_test.go` | A directory target's path is read from config.json |

### AC2 — The outline reads like the mapping
**Given** an orders mapping with a key template, a renamed column, an embedded customer object, an items array with a limit, a transformed field and a type field, **then** the outline is the lines shown in Scope, in document order.

| Test | Location | Verifies |
|---|---|---|
| `TestOutline_OC158_AC2` | `backend/internal/mapping/outline_test.go` | |

### AC3 — Runs from `mappings.json` + `config.json`
**Given** `mappings.json` and a `config.json` for `localhost/shop`, **when** I run `odb run --mappings mappings.json --config config.json`, **then** it connects with config.json's details. **When** config.json lacks the source section, **then** exit 4 names the file and section. **Given** a directory target with `"path": "out"` in `/x/config.json`, **then** the files are written to `/x/out`.

| Test | Location | Verifies |
|---|---|---|
| `TestRunConfig_OC158_AC3_EmptyBundleConfig` | `backend/internal/headless/runconfig_test.go` | Section required, directory section, relative path |
| `TestRun_MappingsFlag` | `backend/cmd/odb/main_test.go` | `--mappings` and `--bundle` accepted, usage text |
| `migrate_run.bats` › `AC1: run end to end (binary runtime)` | `backend/internal/export/testdata/bats/migrate_run.bats` | A real odb build migrates `shop.orders` (10,000 rows) from a mappings.json with no connection settings and a config.json (dev stack: `OC_IT_PG_HOST`) |

### AC4 — Older bundles
**Given** a bundle exported before this story (connection settings inside), **when** I run it with or without `--config`, **then** it behaves as before.

| Test | Location | Verifies |
|---|---|---|
| `TestRunConfig_AppliesOverBundle` | `backend/internal/headless/runconfig_test.go` | Unchanged |
| `TestRun_Unit_EndToEnd` | `backend/internal/headless/memsource_test.go` | A sealed bundle without `--config` runs as before |
