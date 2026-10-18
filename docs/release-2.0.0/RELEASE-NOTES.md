# Omni-Data-Bus 2.0.0 — release notes

**Any document shape, a second kind of target, and migrations that run without the tool.** Build one document from many tables, write it to Couchbase or to a folder of JSON files, and hand the migration to operations as a folder they run themselves.

## New

- **A directory of JSON files as the target** (OC-036, OC-038–OC-041, OC-079).
  - A project's target is a Couchbase cluster or a directory. Test that the tool can write there, and pick or create a sub-folder for each mapping.
  - One complete JSON file per document, at a path derived from its key (`orders/1001.json`), so any document opens directly by key. Layouts `flat`, `sharded` or `key-path`, an optional manifest, and `odb doc path` to find a key's file.
  - Files are written whole (temp file + rename), with the same write modes, pause, resume, crash recovery and error handling as a Couchbase migration. Durability is `none` by default; `file` and `file+dir` sync each file.
  - The verification report checks the files, and the job's **Output** tab finds, shows and downloads a document by its primary key.
  - Switch a project between a directory and Couchbase after you've created mappings, and they move with it (OC-068).
- **One document from many tables** (OC-037, OC-042–OC-046).
  - Related tables are shown across several hops and through junction tables. Add a relationship yourself when the database has no foreign key.
  - Embeds inside embeds: an order with `items[]`, each item holding its `product`. Pull single values from a related table without a nested object.
  - Many-to-many in one step: `order.products[]` through `order_items`, each product carrying the junction's quantity and price.
  - Arrays of objects, of plain values (`productIds: [1, 2]`) or keyed by a column. Filter the rows (only active lines), and write `count`, `sum`, `min`, `max`, `avg`, `first` or `last` onto the parent (`itemCount`, `orderTotal`).
  - Very large relations: `sorted-merge` streams parent and child rows together instead of looking them up in batches (`auto` picks the strategy). A parent with too many children is handled by `truncate`, `fail` or `split`, and a size guard checks every document before it's written.
- **The document composer** (OC-047–OC-049, OC-059–OC-061, OC-077).
  - Sketch the document as a tree, drag tables onto objects and arrays, and drag columns onto fields, with a live sample document beside it.
  - **Import the customer's design**: a sample, several samples, NDJSON or a JSON Schema, keeping its names, nesting, order, types and example values.
  - Suggested bindings: the driving table and the relation behind each object and array, with a reason and a confidence. Then auto-map the fields inside each one.
  - Re-import a revised design without losing your mappings. Differences from the design are shown, with a **strict** mode that blocks migration until they're fixed, and a field-by-field mapping sheet (CSV or Markdown) goes to the customer.
- **Shape the document the way the customer wants it** (OC-063–OC-066, OC-069, OC-081, OC-082, OC-145, OC-147).
  - **One document per group of rows**: a detail table becomes one order per `order_id` with its lines as an array. Set a key such as `order::{order_id}` and the tool offers to combine the rows that share it.
  - Each array item reads its own row, never the parent's.
  - **Expand a foreign key into its object** (`productId` → `product`) and back, from the Canvas or the Composer. The key field goes away once the object is embedded, and embedding only some of the child's fields keeps a small copy.
  - Add a field anywhere (at the root, in an object, in an array's items) and rename one by clicking its name, in every view.
- **Chained embeds** (OC-141–OC-144, OC-151, OC-152, OC-157). Under each embedded object, the Sources pane offers its own foreign keys and child tables, so order → items → product → brand → supplier builds one click at a time. Every foreign-key field, also inside an embedded object, offers **Embed … here**. Related tables are split into **References** and **Referenced by**, each with its cardinality, and a table embedded in several places is read once per batch.
- **Computed fields** (OC-136, OC-138–OC-140). A field whose value is fixed or a template over the document's other fields: `${doc.firstName} ${doc.lastName}`, `ORD-${self.orderId}`. Autocomplete as you type, results on real sample documents, and checks for unknown fields and cycles. Renaming, moving or removing a field updates the computed fields that read it. They work the same in the app, in exports and in the all-in-one image.
- **Run a migration without the tool** (OC-050–OC-053, OC-055, OC-154–OC-160).
  - **Export** downloads one folder: `migrate.sh`, `config.json` (the connection details), `mappings.json` (what the migration does) and a `README.md` for this export. *Include odb for a machine without internet access* adds the binary.
  - `migrate.sh` is the same script for every export. It reads the two JSON files from its folder, finds or downloads a checksum-verified `odb` (or uses Docker), and runs `preflight`, `run`, `resume`, `status`, `verify` or `inspect`.
  - Edit `config.json` to run against another environment or with rotated credentials, without exporting again. Passwords are stored as `base64:…`, and a password typed in plain text is encoded on the next run. base64 isn't encryption: anyone with the folder can read the credentials, and the file is written with mode 0600.
  - `odb run` needs no web app, metadata database or network access to the tool, and prints JSON events and logs to automate around.
  - The Export tab records who exported what and when. Upload a run's `report.json` to see production results next to your test runs.
- **All-in-one image** (OC-072–OC-074, OC-080). One Docker image with the app and a single-node Couchbase Server CE as its metadata store, and as a target through **Use the bundled Couchbase**. It sets itself up on first start, keeps all state on one volume and shuts down cleanly. `make appliance-save` produces one archive with a checksum and a customer guide with `run.sh` and `compose.yml`, also for machines without internet access. A directory target writes into a folder on the host, and the folder picker says which.
- **Release and build on every page** (OC-058). The footer shows the release, with the full build details on hover and **Copy build info** for support tickets. It warns when API and worker nodes run different versions. `odb --version` and `GET /api/v1/version` show the same.

## Changes

- **Views**: Canvas, Composer and Design are reachable from the mapping list, each explained (OC-062). The Table view is hidden (OC-067).
- Each mapping card says what stops it from being ready and takes you there, instead of just *Draft* (OC-083).
- **Review & migrate** shows pre-flight warnings, failures and the row estimate on the page, and **Migrate** starts the job without a confirmation dialog (OC-078).
- Fields of an imported design that you haven't mapped are info, not warnings (OC-075). The sample's Reference box lists differences only in strict mode (OC-076). Fields you added on purpose resolve in one click (OC-070), and every difference says where it is and offers a fix (OC-071).
- Small tables show their exact row count instead of `~0` (OC-146).
- The Couchbase target shows which bucket and scope each mapping writes to, and moves them (OC-150).
- Edits in Design & map, embeds included, are kept when you go to another step (OC-153).
- Each related table is listed once (OC-151).
- The Sample pane has its own background (OC-077).

## Compatibility

- **Upgrade in place** (OC-057). Projects, repositories, mappings and job history are kept. See [Upgrading from 1.0 to 2.0](../upgrade/1.0-to-2.0.md).
  - Run 1.0.1 or later first: its workers skip jobs that need 2.0, and its API refuses to edit a mapping saved by 2.0 (`409 MAPPING_NEWER_VERSION`).
  - `odb admin migrate` (the Helm pre-upgrade hook) adds the `relationships` and `exports` collections and the `idx_jobs_claim` index. It takes seconds.
  - 1.0 mappings are upgraded to mapping spec v2 when read, and stored as v2 when next saved.
  - During a rolling upgrade, jobs that use 2.0 features (directory targets, relations, composed documents, grouping, aggregates, computed fields) wait for 2.0 workers.
  - Downgrading after 2.0 has saved mappings isn't supported: back up the metadata first.
- New optional Helm values: `directoryTarget` (a volume at `/data/odb-out`), `exports` (the image and download URL that exports use) and `worker.memoryLimitMB`.
- 2.0 sets a Go soft memory limit, `OC_MEMORY_LIMIT_MB` (default 640). Raise it together with the pod's memory limit.
- The API is backward compatible under `/api/v1`: 2.0 only adds fields and routes.
- Exported migrations need `odb` 2.0 (`odbVersion >=2.0.0 <3.0.0`). Runners still open sealed, plaintext and external bundles made by earlier 2.0 builds.

## Descoped

- **Kubernetes execution kit** (OC-054). Run the export's `odb run` in your own Job instead.

## Quality gate

The new paths (directory target, nested and many-to-many relations, the composer, exports and the all-in-one image) run as end-to-end tests. `make bench-gate RELEASE=2.0.0` judges the gates in [`bench/gates-2.0.0.json`](../../bench/gates-2.0.0.json) on reference-environment runs. That run is still pending.

Indicative laptop runs ([`bench/results/2.0.0/local/`](../../bench/results/2.0.0/local/README.md), scaled-down datasets):

| Gate | Target | Laptop |
|---|---|---|
| G1 Directory throughput | ≥ 15,000 files/s | 12,981 ❌ (Docker Desktop's virtual disk) |
| G2 Nested relations throughput | ≥ 8,000 docs/s | 54,493 ✅ |
| G3 Sorted-merge vs batched | ≥ 1.5× | 1.00× ❌ (local, idle PostgreSQL) |
| G4 Worker memory | < 768 MB | 257–630 MiB ✅ |
| G5 Runner cold start | < 5 s | 0.45 s ✅ |
| G6 1.0 regression | within 5 % | pending (no 1.0 reference result) |
| G7 No loss | yes | ✅ in every scenario |
