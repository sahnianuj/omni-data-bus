# Getting started

A walkthrough of one migration in the app, from signing in to running the migration somewhere else. It assumes a running Omni-Data-Bus: locally with `make dev` ([development guide](development.md)), or the [all-in-one image](deployment.md#all-in-one-image-app--couchbase-ce). The examples use the local dev stack's sample database.

1. **Sign up / sign in.** The first sign-up creates your personal workspace.
2. **Create a project:** Projects → *New project* (name and optional description). Everything lives inside a project; it owns one source and one target.
3. **Source repository:** choose PostgreSQL, fill in host, port, database, user, password and SSL mode (or *Paste connection string*), click **Test connection**, then **Save to project**. The checklist covers DNS, TLS, authentication, version and read privileges, with a ready-to-copy `GRANT` fix. The password is encrypted and never shown again; re-testing later uses the stored secret.
   *Local example:* host `localhost`, port `5434`, database `shop`, user `omni_ro`, password `omni_ro`, SSL mode `disable`. You'll see a warning about schema `billing`, by design.
4. **Target repository:** choose *Couchbase* (Self-managed or Capella) or *Directory of JSON files*, enter the connection, **Test connection**, **Save to project**. A Couchbase card shows what the credential may do (write documents, manage scopes/collections, create buckets).
5. **Explore the source schema:** *Explore schema →* on the saved source card. The first visit discovers the schema (schemas, tables, views, columns, keys, indexes, row estimates); **Refresh** re-discovers and lists what changed. Search tables and columns (`cust email`), filter by *Tables / Views / Has PK / No PK*, and open a table's **Columns**, **Relationships**, **Indexes** and **Preview** (sample rows, encoded exactly as the migration will write them). Tables the source user can't read show a lock.
6. **Browse or create target keyspaces:** *Browse keyspaces →* on the saved target card: pick bucket → scope → collection, see the document count, and create a bucket (self-managed only; Capella buckets are created in the Capella UI), scope or collection when the credential is allowed to.
7. **Tables → Design & map → Review → Migrate:**
   - pick tables and a target location per table;
   - design each document: from the table, a JSON sample or schema, or the customer's own design, which proposes the whole mapping ([start from the customer's design](user-guide/design-first.md));
   - review the score, map the remaining fields on the canvas, embed related tables, add computed fields, and preview real documents;
   - press **Migrate** and watch the progress bar. A migration can be paused, resumed and verified, and its report downloaded.
8. **Export** *(optional)*: Review → *Export…* → *New export* builds a folder that the `odb` command runs on another machine (macOS, Windows or Linux) without this tool. It uses the project exactly as configured, and asks only for the run options and, for a machine without internet access, which machine's odb to add. The connection details go in the export's `config.json`, which operators edit per environment. Afterwards *Import report* adds the run's `report.json` to the project's runs. See [running migrations outside the tool](user-guide/running-outside-the-tool.md).

Roles: **owner** and **editor** can change things; **viewer** is read-only.

The [user guide](user-guide/README.md) covers each feature in depth, and the **?** next to a feature in the app links to its chapter.
