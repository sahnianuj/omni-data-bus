# OC-159 — One generic `migrate.sh` that reads `config.json` and `mappings.json`

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 5 | OC-053, OC-155, OC-158 | Implemented |

## User story
As an **operator**, I want `migrate.sh` to be the same script for every export, reading `config.json` and `mappings.json` from its folder, so that I can install it once on each machine and get a different migration just by giving it a different pair of JSON files.

## Background (2026-10-16)
Each export generates its own `migrate.sh` (427 lines of template): the project name, mapping list, bundle sha256, target kind, target path, default runtime and credential notes are written into it, and the bundle (and optionally a gzip'd `odb` binary) is appended as base64. Two exports of the same project give two different scripts, and the script can't be reused or reviewed once.

## Scope
**In:**
- **Byte-identical for every export from the same odb release.** The script holds only release values: the odb version, the docker image, the download URL, the per-platform checksums and the cosign identity. No project, mapping, bundle, target or credential content, and no payload.
- **Reads its files from its own folder:** `config.json` (`ODB_CONFIG` overrides) and `mappings.json` (`ODB_MAPPINGS` overrides), passed to `odb run --config … --mappings …`. A missing file stops with exit 4 and names it.
- **Finding odb**, each only when it is the script's odb version: `ODB_BIN`; an `odb` file beside the script (an export "with odb included" puts it there); `odb` on `PATH`; Docker; a checksum-verified download. `ODB_RUNTIME` is `auto` (that order), `binary` (no Docker) or `docker`. `embedded` is gone: the included binary is an ordinary file.
- **Docker runs in place:** the script's folder is mounted at the same path in the container, so the paths in `config.json` and `ODB_STATE_DIR` mean the same thing inside and out. A directory target outside the folder is mounted too: the script asks `odb run target-path --config config.json` where it is (`ODB_TARGET_PATH` wins).
- **Commands:** `preflight`, `run`, `resume`, `status`, `verify`, `inspect` (prints the mappings and their outline, no secrets), `help`. `extract` is removed (nothing is embedded).
- The script's header and `help` say which files it reads. The export's `README.md` stays per export (it describes these mappings and connections).

**Out:** Windows; reading JSON in bash (the script never parses the files: odb does).

## Implementation details
- `internal/export/templates/migrate.sh.tmpl` is rewritten (356 lines, from 427 plus a base64 payload); `scriptData` keeps only the release fields and `export.GenericScript(cfg, version)` renders it. `RenderScript` adds `mappings.json` and, when odb is included, `odb` (0755, the raw binary from the dist tarball or the server's own executable). README.md has its own data (`RenderReadme`).
- `odb run target-path --config FILE` prints the resolved directory target path, or nothing for another target kind (exit 0); exit 4 for an invalid config.
- Docker: `-v "$HERE:$HERE" -w "$HERE"`, plus the state directory and the target path when they're outside `$HERE`.

## Acceptance criteria & unit tests

### AC1 — The same script for every export
**Given** two projects with different sources, targets and mappings, **when** both are exported (one with odb included), **then** their `migrate.sh` files are byte-identical, contain no project name, table name, sha256 of a bundle or base64 payload, and pass shellcheck.

| Test | Location | Verifies |
|---|---|---|
| `TestScript_OC159_AC1_SameForEveryExport` | `backend/internal/export/script_test.go` | |
| `TestScriptTemplate_AC2_ShellcheckAllVariants` | same | `bash -n`, and shellcheck in CI |

### AC2 — Reads its files
**Given** an unzipped export, **when** I run `./migrate.sh run`, **then** odb gets `--mappings <folder>/mappings.json --config <folder>/config.json --state-dir <folder>/odb-state`; `ODB_MAPPINGS` and `ODB_CONFIG` replace the paths; a missing file is exit 4 naming it.

| Test | Location | Verifies |
|---|---|---|
| `migrate_files.bats` › `OC-159 AC2: passes mappings.json and config.json from the script's folder` | `backend/internal/export/testdata/bats/migrate_files.bats` | |
| `migrate_files.bats` › `OC-159 AC2: ODB_CONFIG and ODB_MAPPINGS use other files, e.g. one per environment` | same | Relative paths too |
| `migrate_files.bats` › `OC-159 AC2: a missing mappings.json or config.json stops with exit 4` | same | |
| `migrate_run.bats` › `commands pass their options to odb run` | `backend/internal/export/testdata/bats/migrate_run.bats` | Every command |

### AC3 — Finds odb, including the one beside it
**Given** an `odb` of the right version beside the script, **then** it is used; **given** one of another version, **then** it's skipped with a note. **Given** `ODB_RUNTIME=docker`, **then** docker runs the image with the folder mounted at the same path and `-w` set to it.

| Test | Location | Verifies |
|---|---|---|
| `migrate_runtime.bats` › `OC-159 AC3: uses the odb beside the script, before odb on the PATH` | `backend/internal/export/testdata/bats/migrate_runtime.bats` | |
| `migrate_runtime.bats` › `OC-159 AC3: an odb of another version beside the script is skipped with a note` | same | |
| `migrate_runtime.bats` › `OC-159 AC3: docker mounts the folder in place` | same | State and target outside the folder mounted too |
| `migrate_runtime.bats` › `OC-159 AC3: without ODB_TARGET_PATH, docker asks odb where a directory target writes` | same | |
| `TestRunTargetPath` | `backend/cmd/odb/main_test.go` | `odb run target-path` |
