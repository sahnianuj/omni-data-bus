# OC-149 — `migrate.sh help` shows only what's needed to run it

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P2 | 1 | OC-053 | Implemented |

## User story
As an **operator** running an exported script, I want `./migrate.sh help` to list only the commands I need, so that I'm not left wondering which of seven environment variables I must set (none).

## Background (2026-10-15)
The help printed an `environment:` section (`ODB_STATE_DIR`, `ODB_TARGET_PATH`, `ODB_BUNDLE_PASSPHRASE`, `ODB_RUNTIME`, `ODB_BIN`, `ODB_DOCKER_ARGS`, `ODB_CACHE_DIR`). Every one has a default or is asked for, so it read as setup the user had to do.

## Scope
**In:**
- `help` drops the environment list and says in one line: "Nothing needs to be set to run it. Optional ODB_* settings (e.g. to run unattended): README.md."
- The bundled README's **Settings** table stays the reference (with this export's defaults).

**Out:** changing any variable or its default.

## Acceptance criteria & unit tests

### AC1 — Short help
**Given** an exported script, **when** I run `./migrate.sh help`, **then** it lists the commands and exit codes, points to README.md for optional settings, and lists no environment variables.

| Test | Location | Verifies |
|---|---|---|
| `go test ./internal/export/...` (script rendering) | `backend/internal/export/` | Template renders |
| Manual: `./migrate.sh help` on an export | — | AC1 |
