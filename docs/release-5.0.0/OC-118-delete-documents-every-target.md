# OC-118 — Delete documents on every target kind

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E16 Continuous Replication | P0 | 3 | — | Not started |

## User story
As a **data engineer**, I want rows deleted in the source to disappear from the target, whatever kind of target I chose, so that replication doesn't leave orphan documents behind.

## Scope
**In:** a `target.DocDeleter` port ([architecture D6](00-architecture.md#d6--deletes-become-a-target-capability)), implemented by every target kind, returning one `WriteResult` per key like `DocWriter`:
- Couchbase: `Remove` (with the job's durability); "not found" counts as done.
- Directory: unlink the file (via the layout's path codec), update the manifest and integrity records (OC-039/041) in the same batch; empty sub-folders are kept.
- Edge Server: `DELETE` with the current `_rev` (fetched in batch, as for upserts, 3.0 D3); a conflict is retried once.
- Split pages (OC-046): deleting a parent deletes its page documents; rebuilding a parent with fewer pages deletes the surplus (`Doc.Page` keys known from the plan).
- Target-parity test: the same delete sequence leaves the same set of keys on every kind.

**Out:** anything that triggers deletes (OC-119).

## Acceptance criteria & unit tests

### AC1 — Deletes on all kinds
**Given** 1,000 written documents (with split pages for 10 of them), **when** 300 keys are deleted (including 20 that don't exist), **then** every kind reports 300 done and holds the same 700 keys, with no page documents left for deleted parents.

### AC2 — Directory integrity
**Given** a directory target after deletes, **then** verification (OC-041) passes and the manifest count equals the file count.

| Test | Location | Verifies |
|---|---|---|
| `TestDelete_AC1_Parity` | `internal/connectors/target/delete_parity_integration_test.go` | AC1 |
| `TestDirectoryDelete_AC2_Integrity` | `internal/connectors/target/directory/delete_test.go` | AC2 |
