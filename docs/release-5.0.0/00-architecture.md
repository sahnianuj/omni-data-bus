# Omni-Data-Bus 5.0.0 — Architecture & Technical Decisions

This document is the shared reference for every story in `docs/release-5.0.0`. It is a **delta** on [the 4.0.0 architecture](../release-4.0.0/00-architecture.md) and the releases before it. Story IDs continue from 4.0 (OC-115 onwards).

---

## 1. Release summary

| # | Capability | What the user gets | Stories |
|---|---|---|---|
| 1 | **Continuous replication** | Instead of a one-shot migration, start a **replication**: the tool loads every mapped table once, then keeps the target up to date as rows are inserted, updated and deleted in the source, until the user stops it (typically at cut-over) | OC-115 – OC-119, OC-123 – OC-126 |
| 2 | **PostgreSQL change capture** | Replication from PostgreSQL through logical replication (`pgoutput`), with readiness checks and a setup assistant for self-managed and cloud PostgreSQL | OC-120 – OC-122 |

Oracle change capture follows in [6.0.0](../release-6.0.0/00-architecture.md) and plugs into the same engine (§4). The engine is source-neutral by design.

The project workflow is unchanged up to Review. On the Migrate step the user chooses **Migrate once** (today's job) or **Replicate continuously**.

---

## 2. Why this is harder than it looks: documents are not rows

A one-shot migration reads each driving table and builds documents. A change stream delivers **row** changes. In our mappings a row can feed many documents, and a document can be built from many rows:

| Mapping feature (release) | A change to … | … affects |
|---|---|---|
| Flat table → document (1.0) | a row of that table | one document (its key) |
| Embedded one-to-many, multi-level (OC-021, OC-043) | a child or grandchild row | the root document(s) it is embedded in. The path upwards may need a lookup (grandchild → child → root) |
| Many-to-one lookup, `expandForeignKey` (OC-064) | a looked-up row (a product) | **every** root document embedding it: fan-out, possibly millions |
| Many-to-many through a junction (OC-044) | a junction row or a target row | the parent of the junction row; for a target row, fan-out as above |
| Filters, aggregates, array shapes, per-key limits (OC-045) | any related row | the parent: a count or a "latest 5" may change even if the row is no longer in the array |
| Group rows into one document (OC-063) | any row of the group | the group's document; a row moving between groups affects two documents |
| Split arrays into page documents (OC-046) | any child row | the parent and its pages; page counts may shrink, so stale pages must be deleted |
| Key template on non-key columns | an update of those columns | the **old** document (deleted) and the new one |

So the engine has two halves: **impact resolution** (row change → affected document keys, OC-117) and **re-materialization** (rebuild those documents, OC-119).

---

## 3. Approaches considered

| Approach | How it works | Verdict |
|---|---|---|
| **A. Apply row images to documents** | Patch the target document from the change's before and after values (set field, splice array item) | Rejected as the general mechanism: it needs per-feature patch logic for every mapping feature above (aggregates, limits, ordering, pages, grouping); TOAST-unchanged and LOB values may be absent from the change; it diverges silently when a patch is wrong. Kept only as the **flat fast path** (§5 D3) |
| **B. Re-materialize affected documents from the source** ✅ | Collect affected root keys, then rebuild exactly those documents with the **same `ExecutionPlan`** the migration uses (`ReadKeys` + related reads + transform), and upsert or delete them | **Chosen.** One code path for migration and replication, so every mapping feature works with no new transform logic. Replays are idempotent (rebuilding twice gives the same document), which makes at-least-once delivery safe. Cost: extra source reads, bounded by coalescing |
| **C. Periodic full re-migration or timestamp diffing** | Re-run the job, or read rows with `updated_at > last` | Rejected: misses deletes, needs a reliable timestamp column, and costs a full read per cycle. Possibly offered later as "scheduled refresh" for sources where CDC is impossible |
| **D. External CDC platform (Debezium + Kafka)** | Customer runs Debezium; we consume its topics | Not the default: it adds a JVM and Kafka to every install. Offered as an **adapter** for customers who already run one (6.0, OC-134). It feeds approach B |

---

## 4. Engine architecture

```
 source change stream                    replication engine (source-neutral)                         target
┌────────────────────┐  ChangeEvent  ┌───────────────┐ keys  ┌──────────────┐ keys  ┌─────────────────────┐  docs / deletes
│ postgres: pgoutput │──────────────▶│ impact        │──────▶│ coalescing   │──────▶│ re-materializer      │───────────────▶ DocWriter
│ oracle:  LogMiner  │  (table, op,  │ resolver      │       │ window       │       │ ExecutionPlan        │                 DocDeleter
│ (6.0)              │   old/new key │ (OC-117)      │       │ (dedupe,     │       │ ReadKeys + related   │
└────────┬───────────┘   cols, pos)  └──────┬────────┘       │ order, cap)  │       │ (OC-119)             │
         │                                  │ lookups        └──────────────┘       └──────────┬──────────┘
         │ position (LSN / SCN)             ▼ (source)                                         │ acks
         └───────────────────────────── checkpoint low watermark ◀────────────────────────────┘
```

### Ports (`internal/replication`)
```go
// ChangeEvent is one committed row change, source-neutral.
type ChangeEvent struct {
    Table    source.TableRef
    Op       Op            // Insert | Update | Delete | Truncate
    OldKey   []any         // key columns before (update, delete); nil for insert
    NewKey   []any         // key columns after (insert, update)
    Columns  map[string]any // values present in the change (may be partial: TOAST, LOB)
    Position Position      // opaque, totally ordered per stream (LSN, SCN+sub-position)
    Commit   Position      // the commit position of the transaction
    TxID     string
}

type ChangeStream interface {
    // Start streams committed changes after from; Ack confirms everything ≤ pos is applied.
    Start(ctx context.Context, from Position) (<-chan ChangeEvent, error)
    Ack(pos Position) error
    Close() error
}

type ChangeSource interface { // implemented by source sessions that support CDC
    Readiness(ctx context.Context, tables []source.TableRef) (ReadinessReport, error) // OC-120 / OC-128
    // Prepare creates the stream's server-side objects and returns the position and a snapshot
    // handle that the initial load reads at, so load and stream meet exactly (§5 D2).
    Prepare(ctx context.Context, name string, tables []source.TableRef) (Handoff, error)
    Open(ctx context.Context, name string) (ChangeStream, error)
    Drop(ctx context.Context, name string) error // removes slot/publication (or mining state)
}
```

---

## 5. Key decisions

### D1 — A replication is a long-running job
A new job type `replication` (OC-116) reuses the worker, claim, heartbeat and fencing machinery (1.0 §6.3) and adds its own states:

```
created ─▶ preparing ─▶ initial_load ─▶ streaming ◀──▶ paused
                │            │             │  ▲
                │            │             ▼  │
                │            └────────▶ catching_up        (lag above threshold)
                ▼                          │
             failed ◀──────────────────────┤
                                           ├──▶ needs_resync   (slot lost, drift, gap)
                                           └──▶ stopping ─▶ stopped (server-side objects dropped)
```

Rules: one active replication per project (a migration job can't start while one runs, and the reverse); a mapping change while streaming is refused unless the replication is paused, and then it needs a resync of the changed mappings (OC-122). A replication survives server restarts and worker failover: a new worker reclaims it and continues from the checkpoint.

### D2 — The initial load and the stream meet exactly
The initial load reads at the snapshot where the stream begins, so no change is missed or applied twice:
- PostgreSQL: create the replication slot with `EXPORT_SNAPSHOT`; the slot's consistent point is the stream start, and the exported snapshot name is fed to the existing OC-024 read session (`SET TRANSACTION SNAPSHOT`). The replication connection stays idle until every partition reader has imported the snapshot.
- Oracle (6.0): take the SCN; the initial load reads `AS OF SCN` (4.0 D2); mining starts at that SCN.

Because re-materialization is idempotent, a crash during the initial load can resume with a newer snapshot (OC-024's resnapshot path) and the stream replays from the slot's position: documents converge.

### D3 — Re-materialize by default, flat fast path when exact
For each window of affected keys, the re-materializer runs the mapping's `ExecutionPlan` for those keys (`ReadKeys` + related reads) inside one source read snapshot and upserts the result; a key whose root row no longer exists is **deleted** (with its split pages). A mapping with no relations, grouping or pages whose change carries every projected column in full (no TOAST-unchanged or LOB placeholders) is applied **directly** from the change (no source read). The result is byte-identical to the re-materialized document, and a parity test proves it.

### D4 — Delivery is at-least-once, and documents converge
- Within a window, keys are deduplicated; each key is built at most once at a time (per-key serialization).
- The checkpoint is a **low watermark**: the stream is acked up to the commit position below which every affected document is written. A crash replays from there, and replays rebuild documents from the current source state, so the end state is correct.
- The target reflects the source **as of a recent read**, not each intermediate state. Intermediate versions of a row that changed several times within a window are not written. This is documented as the guarantee.
- Writes from other applications to the replicated target collections are not supported while a replication runs (they are overwritten). This is documented and checked in the preflight when it can be detected (documents without our run metadata).

### D5 — Fan-out is a choice, made visible
Lookup and many-to-many target tables can affect huge numbers of documents. For each relation, OC-117 classifies the fan-out (measured with the existing `KeyCounter`/`RelatedCounter`) and the user picks a **change policy** at setup time:

| Policy | Behavior | Default for |
|---|---|---|
| `refresh` | Rebuild every affected document (streamed, in key batches, throttled) | embedded children, junction rows, low fan-out lookups (≤ 10,000 documents per key) |
| `refresh-capped` | Rebuild up to N documents per change; beyond that, mark the relation **stale** and suggest a scheduled resync | high fan-out lookups |
| `ignore` | Changes to this table never update documents (reference data) | — (explicit choice only) |

### D6 — Deletes become a target capability
Every target kind gets a `DocDeleter` port (OC-118): Couchbase `Remove`, directory unlink and manifest update, Edge Server delete with the current `_rev`. A delete of a document that doesn't exist counts as done.

### D7 — We still never change a customer's database without consent
PostgreSQL logical replication needs server settings (`wal_level`) and objects (publication, slot). The setup assistant (OC-120) shows exact SQL and provider steps. The tool creates the publication and slot itself only when the configured user has the rights **and** the user clicks **Create for me** (audited). Settings that require a restart are never changed by the tool. Stopping a replication drops the slot and publication it created; a slot left behind is the most dangerous thing a CDC tool can do to a primary (§6).

---

## 6. PostgreSQL logical replication: facts and risks

| Topic | Fact | Consequence |
|---|---|---|
| Library | `github.com/jackc/pglogrepl` (pure Go, pgx v5, which we already use) speaks the replication protocol and decodes `pgoutput` | No new runtime dependency, no server plugin to install |
| Plugin | `pgoutput` is built in (PG 10+). `wal2json` is not needed | Works on every managed service |
| Server settings | `wal_level = logical` (restart), `max_replication_slots`, `max_wal_senders` | Readiness check (OC-120) |
| Privileges | `REPLICATION` attribute (or the provider's role: `rds_replication` on RDS/Aurora, `cloudsqlsuperuser` with `cloudsql.logical_decoding` on Cloud SQL, `azure.replication_support = logical` on Azure Flexible Server); `CREATE` on the database for publications; table ownership for `FOR TABLE` publications | Readiness check, provider-specific snippets |
| Publication | `CREATE PUBLICATION odb_<id> FOR TABLE …` with `publish_via_partition_root = true` for partitioned tables (PG 13+) | One publication per replication, covering every table the mappings read (driving and related) |
| Replica identity | Updates and deletes carry the old key only with a primary key (`DEFAULT`) or `REPLICA IDENTITY FULL`/`USING INDEX` | Keyless tables need `REPLICA IDENTITY FULL` (snippet) |
| TOAST | Unchanged large values arrive as "unchanged" markers in updates | Re-materialization reads them; the fast path is skipped for those changes |
| Slot retention | A slot keeps WAL until its `confirmed_flush_lsn` advances. **A stalled or forgotten slot fills the primary's disk** | WAL retention guard (OC-124): monitor `pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)`, alert, auto-pause and, by policy, drop. Recommend `max_slot_wal_keep_size` (PG 13+) |
| Idle databases | With no changes in published tables, the slot doesn't advance while other WAL grows (an RDS heartbeat writes WAL every few minutes) | Advance the confirmed position to the server's WAL end when nothing is pending (keepalive), and optionally `pg_logical_emit_message` heartbeats (PG 14+) |
| DDL | Not replicated. A changed table sends a new `Relation` message before its next change | Schema-drift detection (OC-122) |
| `TRUNCATE` | Sent by `pgoutput` (PG 11+) | Policy: pause (default), or delete all documents of the affected mappings |
| Large transactions | Protocol v1 delivers a transaction at commit; the server spills to disk | Process in windows as events arrive; memory is bounded by the window, not the transaction |
| Failover | A slot exists only on the primary unless failover slots are used (PG 17 `failover = true`, plus `sync_replication_slots` on the standby) | After a failover without failover slots, the slot is gone → `needs_resync`. Create slots with `failover = true` when the server supports it |
| Read replicas | Logical decoding on a standby is possible from PG 16 | Possible later option to take load off the primary; not in 5.0 |

### Managed PostgreSQL support matrix (to confirm in OC-115)

| Service | Logical replication | Notes |
|---|---|---|
| Self-managed 12–17 | Yes | PG 12 is the floor (identity and protocol details); 13+ recommended (`max_slot_wal_keep_size`, partition root publishing) |
| Amazon RDS / Aurora PostgreSQL | Yes | Parameter group `rds.logical_replication = 1` (reboot); role `rds_replication` |
| Google Cloud SQL / AlloyDB | Yes | Flag `cloudsql.logical_decoding = on` (AlloyDB: `alloydb.logical_decoding`); user with `REPLICATION` |
| Azure Database for PostgreSQL Flexible Server | Yes | `wal_level = logical` via server parameter; `azure.replication_support` |
| Supabase, Neon, Crunchy Bridge | Yes (to confirm) | Neon: enable logical replication in project settings |

---

## 7. Domain model additions

| Collection | Document key | Main fields |
|---|---|---|
| `replications` | `<replicationId>` | projectId, workspaceId, status (§5 D1), mappingIds, policies (per relation: D5), window options, source objects created (slot, publication, `createdByTool`), handoff (snapshot, start position), createdBy, timestamps |
| `replication_state` | `<replicationId>` | checkpoint (acked position, low watermark, per-mapping initial-load checkpoints), lag (time, bytes/positions), counters (events, documents written/deleted, refetches, fast-path), last error, written every second by the owner |
| `replication_events` | `<replicationId>::<seq>` | sampled recent changes for the monitor (table, op, key, documents affected, latency), with expiry (24 h default) |

`job_errors` gains `replicationId` (dead-lettered documents keep the 1.0 shape, and a failed document is retried on its next change and by **Retry failed**).

---

## 8. Non-functional requirements
- Sustained apply rate on the reference environment: ≥ 2,000 row changes/s for flat mappings (fast path) and ≥ 500 changes/s for the 2.0 golden relation mappings, with p95 end-to-end lag (source commit → target write) under 5 s at that rate.
- Memory bounded by the window settings (default 5,000 events or 500 ms), independent of source transaction size.
- No lost changes across `kill -9` of the worker, PostgreSQL restart, network partition and worker failover (convergence check, OC-126).
- The replication never holds more than the configured WAL budget on the source without alerting (default warn at 5 GB, auto-pause at 20 GB).

## 9. Definition of Done additions
- Every replication story is tested with the **convergence check**: a randomized change generator runs inserts, updates (including key and foreign key changes) and deletes against the seed, then the replicated target is compared with a fresh one-shot migration of the same final source state. They must be identical.
- Integration tests run against PostgreSQL 13 and 17 containers with `wal_level=logical`.
