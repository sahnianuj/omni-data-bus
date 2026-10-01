# OC-079 — Directory targets default to durability "None"

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E8 Directory Target | P1 | 1 | OC-038, OC-040 | Implemented |

## User story
As a **data engineer** migrating into a folder of JSON files, I want the fastest durability level by default, so that a directory migration isn't several times slower than the same migration into Couchbase unless I ask for per-file syncing.

## Background (2026-10-01)
The same 12k-document migration took 16 s into Couchbase (800 docs/s) and 54 s into a directory (226 docs/s). The directory default was `durability: file`: every file is fsynced before it counts as written, and on macOS Go's `Sync` is `F_FULLFSYNC`, a full device-cache flush of ~4–5 ms that the drive serializes (OC-040 measured ~220 files/s on APFS with `file`, ~5,600 with `none`). Couchbase's own default (`none`) acknowledges in memory, so it was the weaker guarantee that made it look faster.

## Scope
**In:**
- `durability` defaults to `none` for new directory connections, on the server (`ParseConfig`) and in the connection form; the connection summary shows *None* when unset. The hint says files are still written whole (temp file + rename) and only a power cut can lose the most recent ones, which a re-run writes again.
- `file` and `file+dir` are unchanged and can be picked as before.

**Out:** changing saved connections (they keep the level they were saved with); a faster `file` level that fsyncs per file but does the full device flush once per batch (possible future story).

## Acceptance criteria & unit tests

### AC1 — New connections default to None
**Given** a directory config without `durability`, **then** it parses as `none`. **Given** the Target step's directory form, **then** *None* is preselected and a test-connection request sends `durability: "none"`.

| Test | Location | Verifies |
|---|---|---|
| `TestParseConfig_*` (defaults) | `backend/internal/connectors/target/directory/config_test.go` | |
| `TargetKindPicker.test.tsx` › directory test-connection body | `web/src/features/targets/TargetKindPicker.test.tsx` | |
| `TestWriter_SyncFaultNeverPublishes` (now sets `file` explicitly) | `backend/internal/connectors/target/directory/writer_test.go` | Sync path still covered |
