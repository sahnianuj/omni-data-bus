# OC-180 — `odb` finds its files in the export folder

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 3 | OC-158, OC-160 | Implemented |

## User story
As an **operator**, I want to run `odb` inside an unzipped export folder without passing any file paths, so that running a migration is `odb run` (on macOS, Windows or Linux) and not a long command line.

## Background (2026-10-17)
An export is `migrate.sh`, `config.json` and `mappings.json` (OC-160), plus `odb` when it's included. `migrate.sh` (≈350 lines of bash) does four things:
1. It finds or fetches odb: `ODB_BIN`, then `./odb`, then `PATH`, then Docker, then a download with a checksum.
2. It passes `--config`, `--mappings` and `--state-dir`, with their defaults beside the script and the `ODB_*` overrides.
3. It wraps Docker, mounting every folder.
4. It turns the exit code into a sentence.

odb already does all the migration work (`odb run`, `odb run preflight|verify|status`, `odb bundle inspect`). Now that the export includes odb by default, items 2 and 4 can move into odb. Then the script isn't needed (OC-182). This story covers item 2.

## Scope
**In:**
- `odb run` and its subcommands use these defaults when a flag isn't given:

  | Flag | Environment | Default |
  |---|---|---|
  | `--config` | `ODB_CONFIG` | `./config.json` |
  | `--mappings` | `ODB_MAPPINGS` | `./mappings.json` |
  | `--state-dir` | `ODB_STATE_DIR` | `./odb-state` (as today) |
  | `--target-path` | `ODB_TARGET_PATH` | from `config.json` (as today) |

  A flag beats the environment, and the environment beats the default. The same `ODB_*` names `migrate.sh` reads keep working.
- "`./`" means the **current directory first**. If a file isn't found there, odb looks in the **folder the odb binary is in**, or that folder's parent when it's named `bin` (OC-182's offline binary). So `/opt/export/bin/odb run` works from any directory.
- A missing file stops with exit code 4 and says which file and why: "config.json not found in /home/me or beside odb (/opt/export): it holds the source and target connection details. Use --config or ODB_CONFIG."
- The `start` event and `odb run status` say which files were used (absolute paths).

**Out:** new commands (OC-181); changing the export (OC-182).

## Implementation details
- In `backend/cmd/odb/run.go`: when `--config` or `--mappings` is empty, resolve it with `resolveRunFile(flagVal, envName, base)`. It checks the current directory first, then `filepath.Dir(os.Executable())` after `EvalSymlinks`.
- `--bundle` (older exports) stays an alias of `--mappings`.
- `headless/runconfig.go`'s "pass it with --config (migrate.sh does)" message changes to the new wording.

## Acceptance criteria & unit tests

### AC1 — No flags in the folder
**Given** an unzipped export, **when** `cd export && odb run preflight` runs with an installed odb, **then** it reads `./config.json` and `./mappings.json` and passes, exactly as `./migrate.sh preflight` does.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_OC180_AC1_DefaultsInCwd` | `backend/cmd/odb/run_test.go` | |

### AC2 — Beside the binary
**Given** the current directory has no export files, **when** `/abs/export/bin/odb run status` runs, **then** it uses the files in `/abs/export`.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_OC180_AC2_BesideBinary` | `backend/cmd/odb/run_test.go` | |

### AC3 — Precedence
**Given** `ODB_CONFIG=/tmp/prod.json` and `--config ./dev.json`, **then** `dev.json` is used. Without the flag, `prod.json` is used.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_OC180_AC3_FlagEnvDefault` | `backend/cmd/odb/run_test.go` | |

### AC4 — Missing file is explained
**Given** no `config.json` anywhere, **then** odb exits with 4 and the message names both places it looked and how to point at the file.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_OC180_AC4_MissingFile` | `backend/cmd/odb/run_test.go` | |

## Implementation notes (2026-10-17)
- `backend/cmd/odb/files.go`:
  - `runFile.resolve` follows the order flag → `ODB_*` → the export folders;
  - `exportDirs()` gives the folders: the current directory, then odb's folder after `EvalSymlinks`, with its parent first when that folder is named `bin`;
  - `runFile.missing()` builds the message.
- **State directory default**: `odb-state` **in the folder where mappings.json was found**, not always `./odb-state`. That way a run started from another directory keeps its checkpoint with the export, as `migrate.sh` did. When `--mappings` or `ODB_MAPPINGS` names the file, the default stays `./odb-state` (the 2.0 behavior). `ODB_STATE_DIR` and `ODB_TARGET_PATH` are read by odb itself.
- **config.json is optional** for bundles that carry their own settings (2.0 exports). When a bundle needs it and none was found, the existing `RUN_CONFIG_INVALID` error now carries the OC-180 message (`headless.Config.RunConfigMissing`).
- **The `start` event** has `mappingsFile` and `configFile` (absolute paths). `odb run status` names the state directory it read. The files themselves are in the start event, because a stored run doesn't record them.
- **Tests**:
  - `TestRun_OC180_AC1…AC4` in `backend/cmd/odb/run_test.go`. The tests swap the `executable` variable to place odb.
  - Updated: `TestRun_MappingsFlag` and `TestRunConfig_Errors` (the new message).
- **2.0 `migrate.sh`** passes every flag explicitly, so its behavior is unchanged.
- **Changed during OC-183 (2026-10-17)**: the folder above a `bin/` that holds odb is searched only when it holds an export's `mappings.json` or `config.json` (`holdsExport`). Otherwise an odb installed as `/usr/local/bin/odb`, or the image's, also searched `/usr/local` and named it in the "not found" message. Test: `TestRun_OC180_BinParentOnlyForAnExport`.
