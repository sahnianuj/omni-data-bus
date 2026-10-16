# Omni-Data-Bus 2.0.0 — Architecture & Technical Decisions

This document is the shared reference for every story in `docs/release-2.0.0`. It is a **delta** on [the 1.0.0 architecture](../release-1.0.0/00-architecture.md): the stack, API conventions, UX principles, testing strategy and Definition of Done there still apply unless a section below changes them. Story IDs continue from 1.0 (OC-036 onwards), so a reference such as "OC-021" always means the 1.0 story.

---

## 1. Release summary

1.0 moves PostgreSQL tables into Couchbase collections through the web app. 2.0 adds six customer-driven capabilities:

| # | Capability | What the user gets | Stories |
|---|---|---|---|
| 1 | **Directory target** | Migrate into a folder of JSON files, one file per document, named by the document's primary key, so any document can be opened directly by key | OC-036, OC-038 – OC-041 |
| 2 | **Relationship (array) mapping** | Build one big document from several tables: an order with an `items[]` array, each item carrying its product, across multi-level and many-to-many (junction-table) relations | OC-037, OC-042 – OC-046 |
| 3 | **Portable runner** | Export a finished mapping as a self-contained bash script with the mapping metadata, source credentials and target baked in, and run it in production without the tool; or hand the customer the whole tool as one Docker image with a bundled single-node Couchbase Server CE (no Capella needed) | OC-050 – OC-053, OC-055, OC-072 – OC-074 (OC-054 Kubernetes kit descoped) |
| 4 | **Document composer** | Design any target JSON structure from scratch and bind its objects, arrays and fields to source tables and relations | OC-047 – OC-049 |
| 5 | **Computed fields** (added 2026-10-06) | Add any field whose value is fixed, a template (`${doc.firstName} ${doc.lastName}`) or a JavaScript expression over the document's other fields (`doc.email.substring(0, doc.email.indexOf("@"))`), with autocomplete and live results | OC-136 – OC-140 |
| 6 | **Chained embeds** (added 2026-10-06) | After embedding an object (an item's `product`), its own foreign keys and child tables are offered under it on the source side and on its foreign-key fields, at any depth up to the limit, including parent hierarchies | OC-141 – OC-144 |

Stories added to 2.0 after 3.0 – 6.0 were planned take the next free IDs (OC-136 onwards), so 2.0's IDs aren't contiguous.

The workflow in the tool stays the same (Source → Target → Tables → Design & Map → Review → Migrate). Two things are new: the Target step can pick a **directory**, and the Migrate step gains an **Export** tab.

---

## 2. Key decisions

### D1 — Targets are pluggable kinds; `directory` is the second one
In 1.0 the target was Couchbase everywhere: the writer, preflight, the keyspace browser and verification. 2.0 adds a **target kind** on the project's target connection (`couchbase` | `directory`). Each kind implements the target-side ports the pipeline already uses (OC-036):

| Port | Couchbase (1.0) | Directory (2.0) |
|---|---|---|
| `Connector` (validate, test) | cluster ping + permission probe | path allowlist, write/read/delete probe, free space |
| `LocationBrowser` | bucket → scope → collection | root → sub-folder |
| `WriterOpener` / `DocWriter` | KV upsert/insert/replace | atomic file write (temp + rename) |
| `DocReader` (verification) | KV get | file read |
| `Preflight` | keyspace exists, permissions, durability | writable, free space and inodes, orphan temp cleanup |

A project still owns **exactly one** target. Couchbase and directory targets never mix within a project.

### D2 — Mapping spec v2: one relation tree for embeds and the composer
1.0 has `embeds[]`: a flat list, one level deep, many-to-one or one-to-many only. Features 2 and 4 both need nested, multi-hop, many-to-many relations, so 2.0 replaces it with a **relation tree** (`relations[]`, each with an optional `parentId`). OC-037 defines the model, upgrades 1.0 specs on read, and compiles every spec into one **execution plan**. The pipeline (OC-024/025), the preview (OC-022) and the headless runner (OC-051) all run that same plan, so a document looks the same wherever it is built.

- The **mapping canvas** (1.0 workbench) edits the relation tree by embedding related tables (OC-043 – OC-045).
- The **document composer** (OC-047 – OC-049) edits the same tree from the target side: you draw the JSON shape first and bind each object or array to a source relation.

Both editors work on one model, so a mapping made in one can be opened in the other.

### D3 — The exported "script" is a bash wrapper around the same Go binary
The customer asked for a bash or Python script. We ship a **bash** script (`migrate.sh`) that carries the mapping bundle and runs `odb run` (OC-051), the same pipeline code as the server, in headless mode. We chose this over a Python re-implementation of the pipeline because a second implementation would drift from the tool: type coercion, key templates, transforms and relation assembly would each exist twice, and throughput would drop about 10× for the same hardware.

- `migrate.sh` runs the binary in one of two ways, chosen at run time: a local static binary (downloaded with checksum verification, or embedded for air-gapped sites), or a container image pinned by digest. (A generated Kubernetes kit, OC-054, was descoped on 2026-10-01.)
- The script needs bash ≥ 3.2 (macOS default) plus standard coreutils, and must pass `shellcheck`.

### D4 — Credentials in exports are sealed by default
The customer wants credentials baked into the script. We support that, but a script file gets copied, emailed and committed, so the **default** is a sealed credential block (OC-052):

| Mode | What is in the file | How the runner unlocks it | Default |
|---|---|---|---|
| `sealed` | Credentials encrypted with AES-256-GCM under a key derived with argon2id from an export passphrase | Passphrase from a prompt, `ODB_BUNDLE_PASSPHRASE`, or a passphrase file (e.g. a mounted secret) | Until 2026-10-16 |
| `plaintext` | Credentials in clear text | Nothing to unlock | Explicit opt-in: typed confirmation, step-up re-authentication, audit event, warning banner in the file |
| `external` | Only the names of env vars or secret files | Resolved at run time | |
| `config` | Nothing: the connection details and credentials are in `config.json` beside the script, passwords base64-encoded (`base64:…`, not encryption) | `odb run --config` reads the file, laid over the bundle's connections; plain-text secrets are encoded and saved back (OC-155, OC-156) | ✅ |

**Update 2026-10-16 (user decision, OC-154 – OC-156):** operators run one export against several environments by editing the connection details, so the API now exports `config` mode only, with no passphrase and no password confirmation. `config.json` is written with mode 0600 and the README warns that base64 isn't encryption. Runners still open `sealed`, `plaintext` and `external` bundles.

Exporting needs the `editor` role. Every export writes an audit event with its credential mode.

### D5 — Directory layout and file names are deterministic and reversible
A document's file path is a pure function of its key and the target configuration (OC-039), so whoever holds the primary key can compute where the file is:

```
<root>/<folder>/<encode(key)>.json                        layout: flat (default)
<root>/<folder>/<h0h1>/<h2h3>/<encode(key)>.json          layout: sharded (h = sha1(key) hex)
<root>/<folder>/<seg1>/<seg2>/…/<encode(last)>.json       layout: key-path (key split on "::")
```

`encode` percent-encodes characters that are unsafe on Linux, macOS or Windows file systems, and appends a hash when a name would exceed 255 bytes. It is reversible, and it is implemented in Go and TypeScript against one shared fixture file. The key template defaults the same way for every target kind (`<table>::{pk}`: key `orders::1001` is `orders%3A%3A1001.json` in the flat layout and `orders/1001.json` with `key-path`): a mapping builds identical keys and documents whether it writes to Couchbase or to a directory (user decision, 2026-09-29).

### D6 — Computed fields read the assembled target document, in JavaScript
A computed field (OC-136) is a target field whose value is derived from the document's **other target fields**, not from source columns. It's evaluated after the document is assembled (relations, aggregates, shapes) and before null handling and the size guard, in dependency order, with `doc`, `self`, `parent`, `index` and `row` in scope.

- **Templates** (`${path}` only) run natively in Go and cost almost nothing.
- **JavaScript** (OC-137) runs on `github.com/dop251/goja`: pure Go, so `CGO_ENABLED=0` and the static `odb` binary still hold. Each worker owns one runtime, expressions are compiled once, and documents are exposed through lazy read-only proxies. The sandbox has no I/O, no `eval`/`Function`, no randomness, and `Date` fixed to the job's start, so re-runs, resume and server-vs-runner parity give identical documents. Each evaluation has a time budget.
- We chose JavaScript over extending the 1.0 expr-lang `expression` step because it's what Couchbase users already write (Eventing, JavaScript UDFs, and 4.0's PL/SQL conversion), and its string methods are the ones customers ask for. Existing `expression` steps keep working.
- Document keys stay source-based: a key computed from target fields would break resume and directory look-up by key.

### D7 — Relationships are fetched per table, on demand
The 2.0 relationship graph is enumerated as paths from the driving table (depth 4, 200 paths, no table twice on a path), which hides an embedded object's own relationships on large schemas and every self-reference. From OC-141 the web app asks for **one table's direct hops** when it needs them (`GET /connections/{id}/relations/hops?table=…`) and caches them per table. The source side then shows, under each embedded **relation instance**, what it can embed next (OC-142), and any foreign-key field at any depth can be expanded into its object (OC-143). The relation depth limit (`OC_MAX_RELATION_DEPTH`) is unchanged and shown in the UI.

---

## 3. System architecture (2.0)

```
┌──────────────────────────── Browser (React SPA) ─────────────────────────────┐
│ Projects │ Schema │ Designer │ Canvas │ Composer (new) │ Monitor │ Export (new)│
└─────┬────────────────────────────────────────────────────────────▲───────────┘
      │ REST                                                        │ SSE
┌─────▼─────────────────────────────────────────────────────────────┴───────────┐
│ odb server (api | worker | all)                                               │
│   mapping/ ── relations/ (new) ── plan/ (new: spec v2 → ExecutionPlan)        │
│   export/ (new: bundle, credential sealing, script generation)                │
│   worker pipeline: Reader ─▶ RelationResolver (multi-level) ─▶ Transformer    │
│                                         ─▶ Writer (couchbase | directory)     │
└──────┬─────────────────────────┬────────────────────────┬─────────────────────┘
       │                         │                        │
  Metadata (Capella)       Source PostgreSQL        Target: Couchbase  OR  directory
                                                          (PVC / local disk mounted into workers)

────────────────────────── outside the tool (production) ───────────────────────────
 migrate.sh ──▶ odb run --bundle (headless: no metadata store, no UI)
                 state dir: checkpoint.json, errors.jsonl, report.json, manifest
                 stdout: JSON-lines progress   stderr: JSON logs
                 runtimes: local binary │ docker
```

**Headless mode** (OC-051) swaps the three metadata-store dependencies of the pipeline for local ports:

| Port | Server mode (1.0) | Headless mode (2.0) |
|---|---|---|
| Checkpoint store | `jobs.checkpoint` (CAS) | `<state>/checkpoint.json` (write temp, fsync, rename) |
| Error sink | `job_errors` collection | `<state>/errors.jsonl` |
| Progress sink | `job_progress` doc → SSE | JSON lines on stdout every 5 s, and a final `report.json` |

---

## 4. Repository changes

```
backend/internal/
├── relations/              # relationship graph, junction detection, virtual relationships (OC-042)
├── plan/                   # spec v2 → ExecutionPlan compiler, shared by pipeline, preview, runner (OC-037, OC-047)
├── related/                # (1.0) batched lookups → extended to multi-level, m:n, sorted-merge (OC-043/044/046)
├── connectors/target/
│   ├── couchbase/
│   └── directory/          # directory target: config, test, browser, writer, reader (OC-038 – OC-041)
├── pathcodec/              # reversible key → file-name encoding (OC-039); mirrored in web/src/lib/pathcodec
├── export/                 # bundle, sealing and script generation (OC-050, OC-052, OC-053, OC-055)
│   └── templates/          # migrate.sh.tmpl, README.md.tmpl
└── headless/               # odb run: local checkpoint/error/progress ports (OC-051)
api/bundle.schema.json      # JSON Schema of the portable bundle (OC-050)
web/src/features/composer/  # document composer (OC-048, OC-049; JSON design import, binding suggestions, conformance OC-059..061; grouping, FK embed OC-063..064)
web/src/features/export/    # export dialog, export history (OC-055)
```

---

## 5. Domain model changes (metadata store)

| Collection | Change |
|---|---|
| `connections` | `type` adds `directory`. A directory connection has `config` only, no `secret` (OC-038) |
| `mappings` | `spec.specVersion: 2`, `relations[]` replaces `embeds[]`, `mode: canvas \| composed`. v1 specs upgrade on read and are rewritten on the next save (OC-037) |
| `relationships` *(new)* | `<projectId>::<relId>`: user-defined (virtual) relationships where the source has no foreign key (OC-042) |
| `exports` *(new)* | `<exportId>`: projectId, workspaceId, createdBy, createdAt, mappingIds, credentialMode, runtime, bundleSha256, odbVersion. **Never** holds the bundle or any credential (OC-055) |
| `jobs` | `options.target` records the target kind. External run reports imported by users are stored as jobs with `origin: external` (OC-055) |
| `system` | provisioning version bumped to create the new collections and indexes (OC-057) |

---

## 6. Canonical models

### 6.1 Mapping spec v2 (`mapping.Spec`)

```json
{
  "specVersion": 2,
  "mode": "composed",                                   // canvas | composed
  "root": { "table": { "schema": "public", "name": "orders" } },
  "docSchema": { "root": { "id": "fld_root", "name": "$", "type": "object", "children": [ … ] }, "source": "manual" },
  "key": { "template": "{order_id}" },
  "typeField": { "enabled": false },
  "nullHandling": "include",
  "relations": [
    { "id": "rel_items", "parentId": null, "kind": "one-to-many",
      "table": { "schema": "public", "name": "order_items" },
      "join": [ { "parent": "order_id", "child": "order_id" } ],
      "targetFieldId": "fld_items", "shape": "objects",
      "orderBy": [ { "column": "line_no" } ], "limit": 1000, "filter": [],
      "overflow": "truncate", "strategy": "auto", "empty": "array" },
    { "id": "rel_product", "parentId": "rel_items", "kind": "many-to-one",
      "table": { "schema": "public", "name": "products" },
      "join": [ { "parent": "product_id", "child": "product_id" } ],
      "targetFieldId": "fld_items_product" },
    { "id": "rel_tags", "parentId": null, "kind": "many-to-many",
      "table": { "schema": "public", "name": "tags" },
      "via": { "table": { "schema": "public", "name": "order_tags" },
               "parentJoin": [ { "parent": "order_id", "via": "order_id" } ],
               "childJoin":  [ { "via": "tag_id", "child": "tag_id" } ],
               "columns": [ "tagged_at" ] },
      "targetFieldId": "fld_tags", "shape": "scalars" },
    { "id": "rel_cust", "parentId": null, "kind": "many-to-one", "lookupOnly": true,
      "table": { "schema": "public", "name": "customers" },
      "join": [ { "parent": "customer_id", "child": "customer_id" } ] }
  ],
  "aggregates": [ { "id": "agg_1", "relationId": "rel_items", "fn": "sum", "column": "line_total", "targetFieldId": "fld_total" } ],
  "fieldMappings": [
    { "id": "map_1", "targetFieldId": "fld_items_qty", "sources": [ { "relation": "rel_items", "column": "quantity" } ] },
    { "id": "map_2", "targetFieldId": "fld_customer_name", "sources": [ { "relation": "rel_cust", "column": "name" } ] },
    { "id": "map_3", "targetFieldId": "fld_order_ref", "sources": [],
      "computed": { "lang": "template", "expr": "ORD-${self.orderId}" } },
    { "id": "map_4", "targetFieldId": "fld_items_label", "sources": [],
      "computed": { "lang": "js", "expr": "self.sku.concat(' ', self.product.name.substring(0, 20))", "onError": "null" } }
  ],
  "ignoredColumns": []
}
```

- `kind`: `many-to-one` (object) | `one-to-many` (array) | `many-to-many` (array, through `via`).
- `lookupOnly: true`: the relation creates no field of its own, but its columns can be mapped anywhere in its scope (for example, flattening `customers.name` into `customerName`).
- **Scope rule:** a field can be mapped from the root table, any relation on the path from the root to that field's nearest bound ancestor, and lookup-only relations attached to that path (OC-047).
- Source refs use `{ "relation": "<id>", "column": … }`. The 1.0 form `{ "embed": … }` is still accepted on input.
- Depth: at most 4 relation levels by default (`OC_MAX_RELATION_DEPTH`, hard cap 6).
- `computed` (OC-136/137, D6): `{ lang: template | js, expr, nullIfAnyMissing?, onError?: fail-row | null | omit }` on a mapping with no sources. Paths in `expr` are resolved to field ids at compile time; a spec with computed fields needs a worker at the computed-fields level (OC-140).

### 6.2 Directory target connection config

```json
{ "type": "directory",
  "config": { "path": "/data/odb-out", "layout": "flat", "pretty": false,
              "compression": "none", "fileMode": "0640", "durability": "none", "manifest": true,
              "maxDocBytes": 20971520 } }
```

A mapping's target location is `{ "folder": "orders" }` (the equivalent of bucket/scope/collection).

### 6.3 Portable bundle (`*.odb.json`, schema `api/bundle.schema.json`)

```json
{
  "bundleVersion": 1, "odbVersion": ">=2.0.0 <3.0.0",
  "createdAt": "2026-10-01T10:00:00Z", "createdBy": "user@example.com",
  "project": { "id": "…", "name": "Shop → docs" },
  "source": { "type": "postgres", "config": { "host": "…", "port": 5432, "database": "shop", "sslMode": "verify-full" },
              "credentials": { "mode": "sealed", "kdf": { "alg": "argon2id", "salt": "…", "t": 3, "m": 65536, "p": 1 },
                               "nonce": "…", "ciphertext": "…" } },
  "target": { "type": "directory", "config": { "path": "/data/out", "layout": "flat" } },
  "mappings": [ { "id": "…", "name": "orders", "location": { "folder": "orders" }, "spec": { "specVersion": 2 } } ],
  "jobOptions": { "batchSize": 1000, "readers": 4, "writers": 16, "errorThreshold": { "maxErrors": 1000 } },
  "sourceFingerprint": { "sha256": "…", "tables": { "public.orders": { "sha256": "…", "columns": [ { "name": "id", "udtName": "int4", "nullable": false } ] } } },
  "integrity": { "sha256": "…", "contentSha256": "…" }
}
```

Canonical JSON (RFC 8785: sorted keys, shortest numbers) makes `integrity` reproducible; the file is that JSON indented, so exports diff cleanly. `integrity.sha256` covers the whole bundle without `integrity`; `contentSha256` also leaves out the credentials and is the sealed blocks' AAD (OC-050, OC-052). A fingerprint lists the referenced columns so `SCHEMA_DRIFT` can name them offline.

### 6.4 Headless runner contract (`odb run`)

| Exit code | Meaning |
|---|---|
| 0 | completed |
| 2 | completed_with_errors (below the threshold) |
| 3 | failed: error threshold exceeded or fatal write error |
| 4 | preflight or configuration error (bad bundle or `config.json`, schema drift, wrong passphrase or credentials, target not writable, bad command line) |
| 5 | cancelled (SIGINT or SIGTERM after draining and checkpointing) |

stdout carries one JSON object per line: `{"ts","event":"start|progress|mapping_done|warning|done","jobId","mapping","read","written","failed","docsPerSec","etaSec"}`.

---

## 7. Additional non-functional requirements (release gate, validated by OC-056)

| NFR | Target |
|---|---|
| Directory target throughput | ≥ 15,000 files/s for a 1 KB document on local NVMe, 1 worker (8 vCPU), `durability: file` |
| Nested relation throughput | ≥ 8,000 docs/s for an order with 10 items, each with a product (3 tables, 2 levels) |
| Memory with relations | Worker RSS < 768 MB with default settings, including relation buffers (`OC_RELATION_MEMORY_MB`, default 256) and the Go soft memory limit (`OC_MEMORY_LIMIT_MB`, default 640, added by OC-056) |
| Computed fields (G8, OC-140) | The nested scenario plus 2 template and 2 JavaScript fields keeps ≥ 85% of the nested relation throughput |
| Chained embeds (G9, OC-144) | ≥ 6,000 docs/s for an order with 10 items, each with product → brand → supplier → country (5 tables, 4 levels) |
| Runner parity | The same bundle produces byte-identical documents from the server pipeline and `odb run` |
| Runner cold start | `migrate.sh preflight` finishes in < 5 s after the binary is present |
| Exported file safety | No credential in plain text in any export unless the mode is `plaintext`; checked by a scanner test over every generated artifact |

---

## 8. Testing additions

| Level | Tooling | Scope |
|---|---|---|
| Golden documents | `testdata/golden/*.json` shared by Go and TS | Relation assembly, composer output, and pathcodec |
| Script tests | `shellcheck`, `bats-core` against bash 3.2 (macOS) and bash 5 (Linux) | `migrate.sh` commands, argument parsing, runtime detection |
| Parity | Same fixture DB through server pipeline and `odb run`, then a byte diff | D3 guarantee |

Test naming is unchanged from 1.0 (`Test<Unit>_<AC-id>_<behavior>`).

---

## 9. Definition of Done additions

In addition to the [1.0 Definition of Done](../release-1.0.0/00-architecture.md#11-global-definition-of-done):
- [ ] Any change to the spec or bundle format updates `api/bundle.schema.json` and the golden fixtures, and keeps v1 specs and bundle version 1 readable.
- [ ] Generated scripts pass `shellcheck` in CI.
- [ ] New pipeline behavior works in both server and headless modes (parity test).
