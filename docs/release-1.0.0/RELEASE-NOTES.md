# Omni-Data-Bus 1.0.0 — release notes

**PostgreSQL to Couchbase, end to end in the browser.** Connect both sides once, design the documents your application reads, map the columns into them, migrate, and prove the result.

## New

- **Projects that remember their connections** (OC-005–OC-007, OC-035).
  - Sign up and sign in with email and password. Your projects are private to your workspace, with `owner`, `editor` and `viewer` roles.
  - A project owns one PostgreSQL source and one Couchbase target. You enter, test and save each one once, and every discovery, preview, mapping and job reuses them.
  - A guided workflow (Source → Target → Tables → Design & map → Review & migrate) that you can leave and come back to.
  - Passwords and certificates are encrypted at rest, never shown back and never logged. `odb admin rotate-secrets` rotates the key.
- **PostgreSQL source** (OC-008–OC-011).
  - **Test connection** gives clear feedback before you save.
  - Discovery finds every schema, table, view, column, key and index, with types, row estimates and sizes.
  - The schema explorer searches the schema, shows each table's structure and relationships, and previews sample rows.
  - Source connectors are pluggable, so other databases can be added later.
- **Couchbase target, self-managed or Capella** (OC-012–OC-014).
  - **Test connection** shows what the credentials are allowed to do. Capella connections use TLS and the WAN profile, and a custom CA is accepted.
  - Browse buckets, scopes and collections, and pick where documents land.
  - Create a missing bucket, scope or collection from the app.
- **Design the document, not a copy of the table** (OC-015, OC-016).
  - Generate the design from the table (camelCase names, `billing_street` + `billing_city` nested as `billing {street, city}`), or start from a sample document, a JSON Schema, existing documents in the collection, or by hand.
  - Document keys follow Couchbase conventions: `customer::{customer_id}` for a primary key, `order_item::{order_id}::{line_no}` for a composite one, with filters such as `{order_no|pad:8}`. Keys are checked for uniqueness, and a table without a primary key is flagged.
  - An optional type field (`"type": "customer"`) on every document.
- **Most of the mapping done for you** (OC-017, OC-018).
  - The auto-mapper matches columns to fields with a confidence score and the reasons behind it. It's deterministic, and re-running it never overwrites your mappings or brings back a pair you rejected.
  - The mapping shows how much is done (*10 of 23 fields automatically mapped*), with suggestions to accept or reject and what's still left.
- **Finish on the canvas** (OC-019, OC-020).
  - Drag lines from source columns to document fields. An equivalent table view works with the keyboard and screen readers.
  - Every PostgreSQL type becomes the right JSON type by default, including `numeric(38,10)`, `int8`, arrays, enums, domains and `jsonb` with large numbers kept exact.
  - Transformations: to string, parse integer / number / boolean, to datetime, format datetime, trim, lower, upper, substring, replace, concat, split, default, null-if, constant, lookup, round, JSON parse, mask and hash.
- **Embed related tables** (OC-021). A many-to-one row becomes a nested object (`customer.address`), and one-to-many rows become an array (`customer.orders[]`) with an optional order and limit.
- **Check before you migrate** (OC-022). Validation lists every problem by severity, with *Go to* and quick fixes, and the preview shows the real documents that sample rows produce.
- **Migrations you can trust** (OC-023–OC-029).
  - **Migrate** runs pre-flight checks first (and creates the collection if you asked it to), then starts a durable job.
  - Rows are read from a consistent snapshot, in parallel for large tables, with bounded memory.
  - Write mode `upsert`, `insert` (skips existing keys) or `replace` (skips missing ones), with retries for transient failures.
  - A bad row is recorded with its reason, field and key (sensitive columns masked) instead of failing the run. An error threshold stops the job when too much goes wrong, and you can retry just the failed rows.
  - Pause, resume and cancel. After a pause, a restart or a crash, the job continues from its last checkpoint on any worker, with nothing lost.
- **Live progress** (OC-027, OC-028). Rows read, written and failed, rows per second, ETA and per-table progress, updated within a couple of seconds from any server node, with errors as they happen.
- **A verification report** (OC-030). After the run, counts are reconciled, key collisions are detected, and a random sample of documents is compared with its source rows. The report can be downloaded for stakeholders.
- **Ready for production** (OC-031–OC-033).
  - Prometheus metrics, OpenTelemetry traces and logs correlated with them, for the API and the workers.
  - A container image under 40 MB that runs as non-root, a production Docker Compose file and a Helm chart. `odb admin gen-key`, `odb admin migrate` and `odb admin rotate-secrets` for operators.
  - Database hosts are checked against an egress policy at connect time (cloud metadata addresses are always refused, private networks only when allowed), together with security headers, request limits, login rate limiting and an audit log of sensitive actions.

## Known limits

These are out of 1.0 and planned for later releases:

- One level of embedding: embeds inside embeds, and many-to-many arrays through junction tables.
- Couchbase is the only target, and migrations run only inside the tool.
- PostgreSQL is the only source.
- No SSO, invitations, MFA or password reset by email.

## Quality gate

Every user journey (the golden-path migration, pause and resume, error flows) runs as an end-to-end test in CI. `odbbench` (OC-034) judges the release gates:

| Gate | Target |
|---|---|
| Sustained throughput (`standard`: 5M rows, 20 columns, ~1 KB documents) | ≥ 20,000 docs/s on one 8 vCPU worker |
| Worker memory | RSS < 512 MB |
| Progress latency | p95 ≤ 2 s |
| Zero data loss | Documents written = source rows, and verification passes |

An indicative laptop run of the `smoke` dataset (500k rows) measured 89,945 docs/s sustained, 224 MiB peak RSS and 965 ms p95 latency, with verification passing. The reference-environment result isn't committed under `bench/results/1.0.0/` yet.
