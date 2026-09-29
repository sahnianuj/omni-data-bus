# OC-053 — Executable migration script export (`migrate.sh`)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 8 | OC-051, OC-052 | Not started |

## User story
As a **customer's operations engineer**, I want to download one bash script that already contains the mapping, the source credentials and the target (a directory for JSON files, or database credentials), so that I can run the production migration on my own machine or server with a single command, without installing or reaching the Omni-Data-Bus tool.

## Scope
**In:**
- A generated `migrate.sh` with the bundle embedded, which finds or fetches the `odb` binary and runs it.
- Runtimes: `binary` (download with checksum and signature check, or use a local one), `docker` (image pinned by digest), and `embedded` (the binary inside the script, for air-gapped sites).
- Commands: `preflight`, `run`, `resume`, `status`, `verify`, `inspect`, `extract`. Kubernetes commands are added by OC-054.
- A generated `README.md` with run instructions for this specific export.
- Compatibility with bash 3.2 (macOS) and bash 4/5 (Linux), and `shellcheck` clean.

**Out:** a Python variant (decision D3: a bash wrapper around the same binary gives parity and speed), and Windows PowerShell (post-2.0; WSL works).

## Implementation details

### Script structure (`internal/export/templates/migrate.sh.tmpl`)
```bash
#!/usr/bin/env bash
# Omni-Data-Bus migration: "Shop → docs" — exported 2026-10-01 by user@example.com
# Mappings: orders (→ /data/out/orders), customers (→ /data/out/customers)
# Credentials: sealed (passphrase required)   odb version: 2.0.0   bundle sha256: 3f2a…
set -euo pipefail

ODB_VERSION="2.0.0"
ODB_IMAGE="ghcr.io/<org>/omni-data-bus@sha256:…"
ODB_SHA256_linux_amd64="…" ; ODB_SHA256_linux_arm64="…" ; ODB_SHA256_darwin_arm64="…" ; ODB_SHA256_darwin_amd64="…"
: "${ODB_STATE_DIR:=./odb-state}"          # checkpoint, errors, report
: "${ODB_TARGET_PATH:=/data/out}"          # directory targets only; override per environment
: "${ODB_RUNTIME:=auto}"                   # auto | binary | docker | embedded
…
usage() { … }                              # lists commands and env vars
main "$@"; exit $?
__ODB_BUNDLE_BELOW__
<base64 of bundle.odb.json>
__ODB_BINARY_BELOW__                       # only with runtime=embedded
<base64 of gzip'd odb binary for the chosen os/arch>
```
- The bundle is extracted with `sed -n '/^__ODB_BUNDLE_BELOW__$/,/^__ODB_BINARY_BELOW__$/p' | base64 -d` into a `mktemp -d` directory (mode 0700), removed by `trap … EXIT`.
- Only bash 3.2 features are used (no associative arrays, `mapfile` or `${var,,}`). The only external tools needed are `uname`, `mktemp`, `base64`, `sed`, `tar`, and `curl` or `wget` (binary download), plus `shasum` or `sha256sum`. `docker` is needed only for the docker runtime.

### Runtime resolution (`auto`)
1. `ODB_BIN` is set → use it (the version is checked with `odb --version`).
2. An embedded binary is present → extract it.
3. `odb` is on `PATH` with a matching version → use it.
4. `docker` is available → docker runtime.
5. Otherwise, download `odb_<ver>_<os>_<arch>.tar.gz` into `~/.cache/odb/<ver>/`, verify its sha256 against the value **baked into the script**, and, if `cosign` is present, verify the signature too.
- Docker runtime: `docker run --rm -i -v "$ODB_STATE_DIR:/state" -v "$ODB_TARGET_PATH:/out" -e ODB_BUNDLE_PASSPHRASE … "$ODB_IMAGE" run --bundle /work/bundle.odb.json --state-dir /state --target-path /out`, with `--network host` configurable through `ODB_DOCKER_ARGS`.

### Commands
| Command | Does |
|---|---|
| `./migrate.sh preflight` | `odb run preflight`: source reachable, credentials unlock, schema drift, target writable, space estimate |
| `./migrate.sh run [--mapping m]` | Preflight, then run. Exit code passes through from `odb` (§6.4) |
| `./migrate.sh resume` / `status` / `verify` | Pass through to `odb run --resume`, `status` and `verify` |
| `./migrate.sh inspect` | `odb bundle inspect`: what this script will do, with no secrets |
| `./migrate.sh extract [dir]` | Writes `bundle.odb.json` (and the binary, if embedded) for use in other tooling |
| `./migrate.sh help` | Usage, env vars, and exit codes |

- All arguments after `--` go to `odb run` (for example `-- --throttle 5000`).

### Generation
- The API `POST /projects/{projectId}/exports` with `runtime: "script"` returns `odb-export-<project>-<date>.zip` containing `migrate.sh` (mode 0755), `README.md`, and optionally `bundle.odb.json` (when *Include separate bundle file* is ticked).
- `embedded` requires choosing os and arch. It adds about 12 MB (gzip'd) to the script.

## Acceptance criteria & unit tests

### AC1 — Script runs a migration
**Given** an exported script (sealed, directory target) on Linux bash 5 and macOS bash 3.2, **when** `ODB_BUNDLE_PASSPHRASE=… ./migrate.sh run` runs against the fixture DB, **then** it exits 0, and the output folder has one JSON file per order.

| Test | Location | Verifies |
|---|---|---|
| `migrate_run.bats` › `AC1: run end to end (binary runtime)` | `backend/internal/export/testdata/bats/migrate_run.bats` | CI matrix: ubuntu bash 5, macos bash 3.2 |
| `migrate_run.bats` › `AC1: run end to end (docker runtime)` | same | |

### AC2 — Static quality
**Given** every generated variant (runtime × credential mode × target kind), **then** `shellcheck -s bash` reports nothing, and `bash -n` passes.

| Test | Location | Verifies |
|---|---|---|
| `TestScriptTemplate_AC2_ShellcheckAllVariants` | `internal/export/script_test.go` | Skipped locally when shellcheck is missing; required in CI |

### AC3 — Checksum enforcement
**Given** a downloaded binary whose sha256 doesn't match the value baked in, **then** the script deletes it and exits 4 with "checksum mismatch", without running it.

| Test | Location | Verifies |
|---|---|---|
| `migrate_runtime.bats` › `AC3: rejects tampered download` | `backend/internal/export/testdata/bats/migrate_runtime.bats` | Local HTTP fixture server |

### AC4 — Runtime resolution order
**Given** `ODB_BIN` set, **then** it is used. **Given** an embedded binary and `odb` on PATH, **then** the embedded one is used. **Given** neither but docker, **then** docker. **Given** a version mismatch on the PATH binary, **then** it is skipped with a note.

| Test | Location | Verifies |
|---|---|---|
| `migrate_runtime.bats` › `AC4: resolution order` | `backend/internal/export/testdata/bats/migrate_runtime.bats` | Stub binaries |

### AC5 — Payload integrity & cleanup
**Given** the script, **then** `inspect` shows the same bundle sha256 as the header comment, the temp dir is mode 0700, and it is removed on exit, including on Ctrl+C.

| Test | Location | Verifies |
|---|---|---|
| `migrate_payload.bats` › `AC5: sha matches header; tmp removed on INT` | `backend/internal/export/testdata/bats/migrate_payload.bats` | |

### AC6 — Exit codes pass through
**Given** `odb` exits 2, 3, 4 or 5, **then** `./migrate.sh run` exits with the same code, and prints a one-line human summary to stderr.

| Test | Location | Verifies |
|---|---|---|
| `migrate_run.bats` › `AC6: exit codes propagate` | `backend/internal/export/testdata/bats/migrate_run.bats` | Stub odb |

### AC7 — Generated README
**Given** an export, **then** `README.md` lists the mappings, target, credential mode, the exact commands with this export's env vars, and the exit-code table.

| Test | Location | Verifies |
|---|---|---|
| `TestReadmeTemplate_AC7_Golden` | `internal/export/readme_test.go` | |
