# OC-051 — Headless runner (`odb run --bundle`)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 8 | OC-050, OC-040, OC-043, OC-029 | Not started |

## User story
As an **operator running migrations in production**, I want to run an exported bundle with a single binary, with no web app, no metadata database and no network access to the tool, so that I can migrate data inside locked-down environments, including Kubernetes, and get JSON output and logs that I can automate around.

## Scope
**In:**
- An `odb run` command that executes a bundle with the same pipeline code as the server (reader → relations → transform → writer).
- Local ports for checkpoint, errors and progress ([architecture §3](00-architecture.md#3-system-architecture-20)).
- Resume from the state directory, graceful shutdown on signals, and the exit codes of [§6.4](00-architecture.md#64-headless-runner-contract-odb-run).
- `odb run preflight`, `odb run status`, `odb run verify`.
- Static binaries for linux and darwin (amd64 and arm64), published with checksums and signatures.

**Out:** a daemon or scheduler (use cron or a Kubernetes CronJob), reporting back to the tool automatically (the user imports the report, OC-055), and Windows binaries (post-2.0).

## Implementation details

### Command line
```
odb run --bundle bundle.odb.json [--state-dir ./odb-state] [--mapping orders ...]
        [--target-path /data/out] [--readers N] [--writers N] [--throttle DOCS_PER_S]
        [--resume | --restart] [--allow-drift] [--log-format json|text] [--progress-interval 5s]
odb run preflight --bundle …        # connect, fingerprint, target checks; no writes
odb run status --state-dir …        # summarize checkpoint.json / report.json
odb run verify --bundle … --state-dir …   # OC-030/OC-041 verification against the output
```
- Overrides are limited to operational settings (paths, parallelism, throttle). Mappings can't be edited from the command line.
- Credentials: see OC-052 (`--passphrase-file`, `ODB_BUNDLE_PASSPHRASE`, and so on).

### Local ports (`internal/headless`)
| Port | Implementation |
|---|---|
| `CheckpointStore` | `<state>/checkpoint.json`, written as temp + fsync + rename after each committed batch window (1 s), holding the plan hash, per-mapping and per-partition positions, and counters |
| `ErrorSink` | `<state>/errors.jsonl`, append-only, one OC-026 error record per line (sample ≤ 4 KB); rotated at 100 MB |
| `ProgressSink` | JSON lines on stdout every `--progress-interval`, plus `start`, `mapping_done`, `warning` and `done` events |
| `ReportStore` | `<state>/report.json` at the end: counts per mapping, duration, throughput, error summary, verification results |
| `Lock` | `<state>/.lock` (flock); a second runner on the same state dir exits 4 `STATE_LOCKED` |

- The pipeline packages depend only on these interfaces. Server mode keeps its Couchbase-backed implementations. A lint test fails if `internal/pipeline` imports `internal/store`.

### Resume semantics
- By default, if `checkpoint.json` exists and its plan hash matches the bundle, the runner resumes. If the plan hash differs, it exits 4 `STATE_PLAN_MISMATCH` unless `--restart` (which archives the old state to `state/archive/<ts>/`).
- At-least-once with idempotent writes, as in 1.0: an upsert of the same key or file converges.

### Signals & exit
- `SIGINT`/`SIGTERM`: stop reading, drain in-flight batches (at most 30 s), write the checkpoint and report, and exit 5. A second signal exits immediately (the checkpoint is still valid, at-least-once).
- Kubernetes sends SIGTERM on eviction, so the Job restarts and resumes from the PVC (OC-054).

### Output & logs
- stdout: progress JSON lines only, so tools can pipe them (`| jq`). stderr: slog JSON logs (text with `--log-format text` for humans), with the 1.0 secret redaction.
- `--quiet`: only the `done` event. `--progress-interval 0`: no periodic progress.

### Binaries
- `CGO_ENABLED=0` static builds, the same `odb` binary as the server (the `run` subcommand), about 30 MB.
- Published on each release: `odb_<ver>_<os>_<arch>.tar.gz` plus `SHA256SUMS` plus a cosign signature. The container image is unchanged (`odb run` works in it).

> **From OC-058 (AC7):** the `start` event and `report.json` carry `{version, commit}` from `buildinfo.Get()`; `TestRun_AC7_BuildInfoInStartAndReport` (`internal/headless/progress_test.go`) lands with this story.

## Acceptance criteria & unit tests

### AC1 — End-to-end run without the tool
**Given** a bundle for `orders` (with items and products) to a directory target and a Postgres fixture, **when** `odb run` runs with no metadata store configured, **then** it exits 0, writes one file per order, and `report.json` counts match.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_AC1_DirectoryEndToEnd` *(integration)* | `internal/headless/run_integration_test.go` | |
| `TestRun_AC1_CouchbaseEndToEnd` *(integration)* | same | |

### AC2 — Parity with the server
**Given** the same fixture and bundle, **then** the documents from `odb run` are byte-identical to those from a server job using the same mappings.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_AC2_ParityWithServer` *(integration)* | `internal/headless/parity_integration_test.go` | |

### AC3 — Resume & plan mismatch
**Given** a run killed with SIGKILL at ~50%, **when** re-run, **then** it resumes and the output is complete. **Given** a changed bundle, **then** exit 4 `STATE_PLAN_MISMATCH`, and `--restart` archives the state.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_AC3_ResumeAfterKill` *(integration)* | `internal/headless/resume_integration_test.go` | Subprocess |
| `TestRun_AC3_PlanMismatch` | `internal/headless/state_test.go` | |

### AC4 — Signals & exit codes
**Given** SIGTERM mid-run, **then** the checkpoint is written and the exit code is 5. **Given** the error threshold is exceeded, **then** 3. **Given** errors below the threshold, **then** 2. **Given** an unreachable source at preflight, **then** 4.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_AC4_ExitCodes` | `cmd/odb/run_test.go` | Table-driven with fakes |
| `TestRun_AC4_SigtermDrains` *(integration)* | `internal/headless/signal_integration_test.go` | |

### AC5 — Machine-readable output
**Given** a run, **then** every stdout line parses as JSON with the documented fields, no log lines appear on stdout, and no secret appears on stdout or stderr.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_AC5_StdoutContract` | `internal/headless/progress_test.go` | |
| `TestRun_AC5_NoSecretsInOutput` | same | Shared scanner |

### AC6 — No metadata-store dependency
**Given** the `internal/pipeline` package, **then** it doesn't import `internal/store`, `internal/httpapi` or `gocb` except through the target port.

| Test | Location | Verifies |
|---|---|---|
| `TestImports_AC6_PipelineIsPortable` | `internal/pipeline/imports_test.go` | `go list -deps` |

### AC7 — Single-instance lock
**Given** a runner holding the state dir, **when** a second runner starts on it, **then** exit 4 `STATE_LOCKED`.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_AC7_StateLock` | `internal/headless/lock_test.go` | |
