# OC-133 — RAC and standby mining

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E18 Oracle Change Capture | P1 | 5 | OC-130 | Not started |

## User story
As a **data engineer at an enterprise customer**, I want replication to work with Oracle RAC, and optionally to mine on a standby, so that we can use it on our real production topology without adding load to the primary.

## Scope
**In:**
- RAC: mine every redo thread (`V$LOG.THREAD#`, `V$ARCHIVED_LOG.THREAD#`), merged by SCN; a stopped instance's thread is included through its archived logs; readiness shows threads.
- Standby mining: the change reader can connect to an Active Data Guard physical standby (read-only, open) for mining while the initial load and refetches read from a configured connection (primary or standby), per the spike's findings on what 19c+ allows. Lag accounts for apply lag on the standby.
- Test environment: a two-instance RAC is not available in CI, so this uses simulated multi-thread redo via the spike's approach, plus a documented manual test on a customer-like environment before release.

**Out:** downstream mining databases.

## Acceptance criteria & unit tests

### AC1 — Thread merge
**Given** redo from two threads with interleaved SCNs (simulated), **then** events are emitted in global commit-SCN order.

### AC2 — Manual RAC test
**Given** the documented RAC test plan, **then** it has been executed and its results recorded before release.

| Test | Location | Verifies |
|---|---|---|
| `TestMiner_AC1_ThreadMerge` | `internal/connectors/source/oracle/cdc/threads_test.go` | AC1 |
| RAC test record | `docs/release-6.0.0/tests/rac.md` | AC2 |
