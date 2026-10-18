# OC-181 — Operator commands on `odb`: preflight, run, resume, status, verify, inspect

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 5 | OC-180 | Implemented |

## User story
As an **operator**, I want the commands I used through `migrate.sh` to be odb commands with the same names, output and exit codes, so that I learn one tool and don't need a script to run a migration.

## Background (2026-10-17)
Today the operator-facing names are only in `migrate.sh`. They map to odb as follows:

| `migrate.sh` | odb today |
|---|---|
| `preflight` | `odb run preflight` |
| `run` | `odb run` |
| `resume` | `odb run --resume` |
| `status` | `odb run status` |
| `verify` | `odb run verify` |
| `inspect` | `odb bundle inspect FILE` |

The script also prints a one-line summary for each exit code. odb's default log format is JSON, which is right for pipelines and hard to read in a terminal.

## Scope
**In:**
- **Top-level commands** with the same names as `migrate.sh`: `odb preflight`, `odb run [--mapping M]`, `odb resume`, `odb status`, `odb verify` and `odb inspect`. Each uses the OC-180 defaults, so in the export folder none of them needs a path. `odb inspect` reads `mappings.json` by default.
- **The current forms keep working**: `odb run preflight|verify|status|target-path`, `odb run --resume` and `odb bundle inspect FILE`. 2.0 `migrate.sh` scripts call these forms. Their help points to the new names.
- **Same exit codes** as today: 0 completed, 2 completed with errors, 3 failed, 4 preflight or configuration error, 5 cancelled.
  - On a terminal, the last line explains the code in the words `migrate.sh` uses, for example "cancelled after saving the checkpoint: `odb resume` continues". Those words now name odb commands.
- **Readable on a terminal**: when stderr is a TTY, `--log-format` defaults to `text` (progress, then the summary). When it isn't a TTY, the default stays `json`, so pipelines and Kubernetes logs don't change. An explicit `--log-format` always wins.
- **`odb help`** lists the operator commands first: what each does, the files it reads and the exit codes. The server and admin commands are listed under them, so the text reads like `migrate.sh help`.
- **Ctrl-C** saves the checkpoint and exits with 5, as `odb run` does today. `odb resume` continues the run.

**Out:** Windows builds (possible now that bash isn't needed: a follow-up). Renaming the binary.

## Implementation details
- `backend/cmd/odb/main.go`: `preflight`, `resume`, `status`, `verify` and `inspect` dispatch to the existing `runCmd` and `bundleCmd` code paths with the subcommand filled in. There's no second implementation.
  - `odb` with no arguments still starts the server (role `all`). None of the new names clashes with `admin` or `healthcheck`.
- The summary text moves from `migrate.sh`'s `summary()` into `run.go`, as one function used by every command.
- TTY detection: `golang.org/x/term.IsTerminal(int(os.Stderr.Fd()))`.

## Acceptance criteria & unit tests

### AC1 — Same names, same codes
**Given** the OC-173 export, **then** `odb preflight|run|resume|status|verify|inspect` give the same exit codes, state files and report as the matching `migrate.sh` commands.

| Test | Location | Verifies |
|---|---|---|
| `TestCLI_OC181_AC1_CommandParity` | `backend/cmd/odb/main_test.go` | Table over the six commands, with the old forms as the oracle |

### AC2 — Old forms still work
**Then** `odb run preflight`, `odb run --resume`, `odb run status` and `odb bundle inspect FILE` behave exactly as in 2.0, and a 2.0 `migrate.sh` runs against a 2.5 odb.

| Test | Location | Verifies |
|---|---|---|
| `TestCLI_OC181_AC2_LegacyForms` | `backend/cmd/odb/main_test.go` | |
| `migrate_runtime.bats` (2.0 script, 2.5 odb) | `backend/internal/export/testdata/bats/` | |

### AC3 — Text on a terminal, JSON in a pipe
**Given** a TTY, **then** `odb run` prints text progress and the summary line. **Given** stdout and stderr redirected, **then** it prints JSON events as in 2.0.

| Test | Location | Verifies |
|---|---|---|
| `TestCLI_OC181_AC3_LogFormatByTTY` | `backend/cmd/odb/run_test.go` | |

### AC4 — Cancel and resume
**Given** a run interrupted with SIGINT, **then** it exits with 5 and the message says to run `odb resume`. `odb resume` finishes the run with no duplicate documents.

| Test | Location | Verifies |
|---|---|---|
| `TestCLI_OC181_AC4_CancelResume` | `backend/cmd/odb/run_test.go` | |

### AC5 — Help reads like the old script's
**Then** `odb help` lists the six operator commands, the files and the exit codes before the server commands.

| Test | Location | Verifies |
|---|---|---|
| `TestCLI_OC181_AC5_Help` (golden) | `backend/cmd/odb/main_test.go` | |

## Implementation notes (2026-10-17)
- **`backend/cmd/odb/main.go`**:
  - `preflight`, `status` and `verify` dispatch to `runRun` with that subcommand, and `resume` to `runRun --resume`;
  - `inspect [FILE]` (`runInspect`) finds `mappings.json` as OC-180 does, then calls `bundle inspect`;
  - `help` prints `operatorUsage`: the operator commands, the files and the exit codes first, then the other commands. An unknown command prints the same text.
- **`odb` with no command in an export folder** prints the help (exit 4) instead of trying to start the server. This only happens when `OC_META_CONNSTR` and `OC_DEV` aren't set, so it can't change a server start.
- **Terminal output**:
  - `--log-format` defaults to `text` when stderr is a terminal, else `json`.
  - With text logs and a terminal on stdout, run events are printed as lines (`headless.Event.Text`, `ProgressSink.Text`), for example `progress  12,345 read · 12,300 written · 45 failed · 4,100 docs/s · about 2m10s left`.
  - Piped or redirected output keeps the 2.0 JSON lines. An explicit `--log-format json` gives JSON on stdout too.
- **Exit summary**: after `odb run` or `odb resume`, a terminal gets `odb: <sentence>` (`exitSummary`, `migrate.sh`'s `summary()` naming `odb resume`).
- **SIGINT** is handled by the existing drain/abort code: the checkpoint is saved and the exit code is 5.
- **Tests**:
  - `TestCLI_OC181_AC1…AC5` in `backend/cmd/odb/run_test.go` (parity is checked against the legacy forms);
  - `TestEvent_OC181_Text` in `internal/headless/headless_test.go`;
  - the 2.0 `migrate.sh` bats suite passes against this odb, including the end-to-end run against the dev stack's Postgres (`OC_IT_PG_PORT=5434`).
- **AC4's interrupt itself** is covered by the existing `TestWatch_AbortAndDrain` and the resume integration tests. The CLI test covers `odb resume` without a checkpoint and the messages.
- **Corrected during OC-184 (2026-10-17)**: a released 2.0 `migrate.sh` accepts only an odb of exactly its own release (`use_bin`), so in practice it runs odb 2.0, not 2.5. The bats suites build the script and odb as the same `dev` version. What they show is that the `odb run` forms the 2.0 script calls still work in this odb. odb 2.5 also runs a 2.0 export folder directly (`odb preflight` / `odb run` in it, OC-180), and the 2.0 bundle range `>=2.0.0 <3.0.0` allows it.
