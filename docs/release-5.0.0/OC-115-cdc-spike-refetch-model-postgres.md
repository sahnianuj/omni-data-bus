# OC-115 — Continuous replication spike: re-materialization model and PostgreSQL logical replication

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E16 Continuous Replication | P0 | 3 | — | Not started |

## User story
As an **engineer**, I want the replication design proven on a prototype before we build it, so that the engine's riskiest assumptions (snapshot handoff, impact resolution cost, apply throughput) are facts.

## Scope
**In:** a throwaway prototype and a findings note (`docs/release-5.0.0/spikes/cdc.md`) that updates the [architecture](00-architecture.md):

| # | Question | How |
|---|---|---|
| 1 | Does `CREATE_REPLICATION_SLOT … LOGICAL pgoutput (SNAPSHOT 'export')` via `pglogrepl` give a snapshot our OC-024 partition readers can import, and does streaming from the slot then show exactly the changes after it? | Prototype with concurrent writers; compare counts |
| 2 | How long can the replication connection stay idle holding the snapshot? What happens if it drops mid-load? | Prototype |
| 3 | Re-materialization cost: changes/s for a flat mapping and for the golden `orders` mapping (items + products), with window sizes 100 / 1,000 / 5,000 | Measure |
| 4 | Fan-out: time to resolve and rebuild documents for a product embedded in 100k orders | Measure |
| 5 | TOAST-unchanged values, `REPLICA IDENTITY FULL` vs `DEFAULT` old tuples, partitioned tables with `publish_via_partition_root` | Prototype |
| 6 | Managed services: confirm the matrix in §6 on RDS PostgreSQL and Cloud SQL (if accounts are available), including role names | Try |
| 7 | Keepalive-based confirmed-position advance on an idle database: does the slot's `restart_lsn` follow? | Prototype |
| 8 | PG 17 failover slots: creation from `pglogrepl` and behavior after promotion | Prototype (if time allows) |

**Out:** production code.

## Acceptance criteria

### AC1 — Handoff proven
**Given** 10 writers committing continuously during the initial load, **then** the prototype's target equals a fresh migration of the final source state, with zero missing and zero extra documents.

### AC2 — Numbers recorded
**Given** questions 3 and 4, **then** the note records throughput and latency, and the NFRs in §8 are confirmed or revised with the user.

### AC3 — Every question answered
**Given** the note, **then** each question has an answer, evidence and the story it changes.

| Test | Location | Verifies |
|---|---|---|
| Spike findings review | `docs/release-5.0.0/spikes/cdc.md` | AC1–AC3 |
