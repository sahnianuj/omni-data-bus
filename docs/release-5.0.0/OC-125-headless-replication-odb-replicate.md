# OC-125 — Headless replication with `odb replicate`

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E16 Continuous Replication | P1 | 5 | OC-121, OC-124 | Not started |

## User story
As a **platform engineer**, I want to run a replication from the exported bundle in production, without the tool's UI or metadata store, so that long-running replication fits our deployment rules like the one-shot runner does.

## Scope
**In:**
- `odb replicate --bundle <file> --state-dir <dir>`: the same engine with the headless memstore (OC-051) and a state directory holding the checkpoint, handoff and errors; resumes from the state directory after a restart.
- Bundle export option **Include replication** (policies, window options, slot naming); the setup checklist (OC-120) is printed by `odb replicate --check`.
- Operation: SIGTERM drains the current window and acks; `--stop` drops the source objects; `/healthz` and `/metrics` on an optional port; JSON logs.
- Run kits: `migrate.sh replicate` subcommand, a systemd unit and a Docker Compose example in the bundle README; the all-in-one appliance can run a replication from the UI as usual.
- WAL guard (OC-124) runs in-process with the same thresholds.

**Out:** Kubernetes operator (the 2.0 Kubernetes kit was descoped).

## Acceptance criteria & unit tests

### AC1 — Resume from state dir
**Given** `odb replicate` killed and restarted with the same state dir, **then** it continues from the acked LSN and the convergence check passes.

### AC2 — Graceful stop
**Given** SIGTERM, **then** the process exits within 10 s after acking the last complete window; `--stop` drops slot and publication.

| Test | Location | Verifies |
|---|---|---|
| `TestReplicate_AC1_Resume` | `internal/headless/replicate_integration_test.go` | AC1 |
| `replicate.bats` | `backend/internal/export/testdata/bats/replicate.bats` | AC2 |
