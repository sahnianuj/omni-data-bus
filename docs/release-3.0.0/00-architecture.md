# Omni-Data-Bus 3.0.0 — Architecture & Technical Decisions

This document is the shared reference for every story in `docs/release-3.0.0`. It is a **delta** on [the 2.0.0 architecture](../release-2.0.0/00-architecture.md) (itself a delta on [1.0.0](../release-1.0.0/00-architecture.md)): the stack, API conventions, UX principles, testing strategy and Definition of Done there still apply unless a section below changes them. Story IDs continue from 2.0 (OC-084 onwards).

---

## 1. Release summary

| # | Capability | What the user gets | Stories |
|---|---|---|---|
| 1 | **Couchbase Edge Server target** | Migrate PostgreSQL tables into a Couchbase Edge Server database (`database.scope.collection`), check the setup before Migrate, then browse and query the migrated documents inside the tool, since Edge Server has no UI of its own | OC-084 – OC-090 |

The workflow is unchanged (Source → Target → Tables → Design & Map → Review → Migrate). The Target step gains a third kind, **Edge Server**, next to Couchbase and Directory.

---

## 2. What Edge Server is, and what it means for us

Couchbase Edge Server (1.1.0 at the time of writing) is a small, headless database server built on Couchbase Lite. It's meant for edge hardware: a store, a vehicle, a kiosk. What we know, partly confirmed against a live 1.1.0 instance in the `brewpos` demo (`demo/brewpos/edge-server`, `webapp/src/edge`):

| Topic | Fact | Consequence for us |
|---|---|---|
| Interface | One REST port (`interface`, default `0.0.0.0:59840`), HTTP or HTTPS. **No web UI**, no admin port | We are the UI: browsing and querying (OC-088) |
| Keyspaces | `database.scope.collection`, like Couchbase Server; default scope/collection may be omitted | Same location model as Couchbase: database ≈ bucket |
| Scopes & collections | Declared in `config.json` under the database's `collections` (with optional SQL++ `indexes`); a change needs a **restart**. No REST endpoint creates them | We can't create collections over the API: generate the config (OC-087) or manage the server ourselves (OC-090) |
| Users & roles | A `users` JSON file (bcrypt hashes); built-in roles `admin` and `replicate`; custom collection-level `read`/`write` grants. No REST endpoint for users | A migration user needs `write` on each target keyspace (`admin` works) |
| Writes | `enable_client_writes` **defaults to false**; `PUT /{keyspace}/{id}` creates (201); an update must carry the current `_rev` (in the body or `?rev=`), else it conflicts | Overwrite needs the current revision of each document (OC-086) |
| Reads & queries | `GET /{keyspace}/{id}` (body includes `_rev`); `POST /{keyspace}/_query` with `{"query": "...", "params": {}}` and `FROM _`, only when `enable_adhoc_queries` is true | Explorer and verification (OC-088, OC-086) |
| Changes | `GET /{keyspace}/_changes?feed=sse&since=now` | Not needed by the migration itself |
| CORS | None | Irrelevant to us: the tool's server calls Edge Server, never the browser |
| Replication | `replications[]` push/pull to Capella App Services or Sync Gateway | Migrated documents may flow upstream: a setup-guide decision (OC-089) |

**To confirm in the spike (OC-084):** a bulk write endpoint and how it reports per-document conflicts, a bulk read (by keys) for revisions, how to list a database's scopes and collections, the document size limit, reserved top-level names (`_id`, `_rev`, `_deleted`, `_attachments`, others?), and whether the `couchbase/edge-server` image may be redistributed in our appliance.

---

## 3. Key decisions

### D1 — Edge Server is a third target kind behind the same ports
It implements the 2.0 target-side ports (OC-036) and nothing in the pipeline, preview, plan or composer changes:

| Port | Couchbase | Directory | Edge Server (3.0) |
|---|---|---|---|
| `Connector` (validate, test) | cluster ping + permission probe | path checks, write probe, free space | `GET /{db}` with the user's credentials, writes-enabled probe, version |
| `LocationBrowser` | bucket → scope → collection | root → sub-folder | database → scope → collection (read-only listing; create = config snippet, OC-087) |
| `WriterOpener` / `DocWriter` | KV upsert/insert/replace | atomic file write | REST writes in batches, revision-aware (OC-086) |
| `DocReader` (verification) | KV get | file read | REST get by key |
| `Preflighter` | keyspace, permissions, durability | writable, space | collection exists, writes enabled, user may write, ad-hoc queries (warn only) |
| `DocLimiter` | 20 MiB | `maxDocBytes` | Edge's limit (spike) |

Parity: the same mapping writes the **same document bodies and keys** as the other kinds (the target-parity rule from 2.0). The document key becomes the Edge document ID.

### D2 — We never edit a customer's Edge Server configuration
A customer's Edge Server belongs to them: we read through its REST API, and for anything only the config file can do (collections, indexes, users, `enable_client_writes`) we **generate the exact snippet** and check after their restart that it took effect. Only an Edge Server **we run** inside the all-in-one appliance (OC-090) has its config and users managed by the tool.

### D3 — Overwrite is revision-aware
Edge rejects an update without the current `_rev`. Write modes keep their 1.0 meaning:

| Mode | Edge behavior |
|---|---|
| `insert` | Write without `_rev`; an existing document is reported as `TARGET_DOC_EXISTS` |
| `upsert` (default) | Fetch revisions for the batch's keys first, write with `_rev` where one exists; on a conflict, refetch and retry once |
| `replace` | Like `upsert`, but a key with no existing document is reported as `TARGET_DOC_MISSING` |

Top-level fields named like Edge's reserved properties are refused at validation time (`EDGE_RESERVED_FIELD`), before a job starts.

---

## 4. Connection config (`kind: "edge-server"`)

| Field | Default | Rules |
|---|---|---|
| `url` | — | `http(s)://host:port`, no path |
| `database` | — | Edge database name (`store`) |
| `tls.caPem` | — | Optional CA or self-signed certificate to trust (reuses the 1.0 certificate dropzone) |
| `tls.insecureSkipVerify` | `false` | Allowed only with a warning badge on the connection |
| `batchSize` | 200 | 1 – 5,000 (tuned by OC-089's benchmark) |
| `concurrency` | 4 | 1 – 32 |
| secret: `username`, `password` | — | Basic auth, sealed like every other secret |

---

## 5. Additional non-functional requirements
- Throughput target on the reference Edge host (to be set by OC-089): at least 1,000 small documents/s with the default batch size, and no document lost or duplicated on resume after a crash (OC-029 rules apply).
- Every Edge Server call has a timeout and retries idempotent requests with backoff; a 401/403 stops the job with `TARGET_AUTH_FAILED`, not a retry loop.

## 6. Definition of Done additions
- Each Edge story's integration tests run against a real `couchbase/edge-server` container pinned by digest (`make test-integration-edge`).
- The parity test (OC-089) proves the same documents in Couchbase, Directory and Edge Server for the reference mappings.
