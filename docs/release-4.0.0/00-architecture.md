# Omni-Data-Bus 4.0.0 — Architecture & Technical Decisions

This document is the shared reference for every story in `docs/release-4.0.0`. It is a **delta** on [the 3.0.0 architecture](../release-3.0.0/00-architecture.md), which is itself a delta on 2.0 and 1.0. The stack, API conventions, UX principles, testing strategy and Definition of Done from those releases still apply unless a section below changes them. Story IDs continue from 3.0 (OC-091 onwards).

---

## 1. Release summary

| # | Capability | What the user gets | Stories |
|---|---|---|---|
| 1 | **Oracle Database source** | Pick Oracle as a project's source, discover its schemas, preview data, map tables (including embeds, many-to-many, grouping and the composer), and migrate into any target kind with the same resume, verification and export features PostgreSQL has | OC-091, OC-093 – OC-101 |
| 2 | **PL/SQL → Couchbase UDFs** | Inventory every PL/SQL function, procedure and package in the source, see how much of each can be converted automatically, convert functions to Couchbase SQL++ user-defined functions (UDFs), review and edit them, and deploy them to the target | OC-092, OC-102 – OC-108, OC-112, OC-113 |
| 3 | **Side-by-side verification** | Run a PL/SQL function on Oracle in the left pane and its UDF on Couchbase in the right pane with the same arguments, compare the results, save test cases and verify every converted function in one batch | OC-109 – OC-111 |

The project workflow is unchanged (Source → Target → Tables → Design & Map → Review → Migrate). The Source step gains **Oracle** next to PostgreSQL. A project whose source is Oracle also gets a new **Functions** area (PL/SQL inventory → convert → run side by side → deploy). It needs a Couchbase target (§5 D7).

Continuous replication (change data capture) is planned for later releases: [5.0.0](../release-5.0.0/00-architecture.md) for PostgreSQL and [6.0.0](../release-6.0.0/00-architecture.md) for Oracle. 4.0 design choices that make them easier are marked **(CDC)**.

---

## 2. Oracle facts that shape the design

| Topic | Fact | Consequence for us |
|---|---|---|
| Versions in the field | 19c is the long-term support release most customers run; 21c is innovation-only; 23ai (renamed 26ai) is current | Support 19c, 21c, 23ai/26ai. 12.2/18c are best effort (`FETCH FIRST` needs 12.1+). The spike confirms each |
| Driver | `github.com/sijms/go-ora/v2` is a pure-Go implementation of the Oracle wire protocol: no Instant Client, no cgo | It fits `CGO_ENABLED=0` and the static `odb` binary. The OCI-based `godror` is ruled out |
| Connect strings | EZConnect (`host:port/service`), SID, full TNS descriptors, TCPS with a wallet (Autonomous Database requires mTLS or TLS) | Config offers host/port/service, SID, or a raw descriptor, plus an optional wallet upload (OC-094) |
| Schemas | A schema is a user (`OWNER`). Oracle-maintained users (`SYS`, `SYSTEM`, `XDB`, `MDSYS`, …) are flagged `ORACLE_MAINTAINED = 'Y'` (12c+) | Discovery excludes them by default (OC-095) |
| Identifiers | Unquoted names are stored upper-case (`CUSTOMER_ID`); quoted names keep their case | Automap and key templates handle both (OC-098); generated SQL always quotes |
| Empty string | `''` **is** `NULL` in `VARCHAR2`/`CHAR` | A JSON document never has `""` from Oracle; converted UDFs must reproduce this (OC-104) |
| `DATE` | Carries a time to the second; no time zone | Maps to a timestamp, **not** a date-only string (OC-096) |
| `NUMBER` | Up to 38 significant digits; `NUMBER` without precision is common even for integer IDs | Encoding keeps precision (string when it can't be a safe JSON number); integers are inferred from scale 0 and from data (OC-096) |
| Consistent reads | No exported snapshots. A query can read `AS OF SCN n` (Flashback Query), limited by `UNDO_RETENTION` (`ORA-01555 snapshot too old`) | Every partition reader reads `AS OF SCN` of one SCN taken at start (§5 D2). **(CDC)** That SCN is also where change capture starts |
| Row-value comparison | `(a, b) > (:1, :2)` is not supported | Keyset predicates are expanded (`a > :1 OR (a = :1 AND b > :2)`) (OC-099) |
| Sorting | `ORDER BY` on character columns follows `NLS_SORT`, which may be linguistic | Reader sessions set `NLS_SORT = BINARY` and `NLS_COMP = BINARY`, so key order matches Go byte order (sorted-merge, OC-046) |
| Tables without a key | Every heap row has a `ROWID`, stable inside one `AS OF SCN` read | Keyless tables are read in resumable `ROWID` ranges instead of a non-resumable cursor (OC-099) |
| Statement timeouts | No per-statement timeout | Probes use context cancellation (driver break/reset). The spike confirms it is reliable |
| PL/SQL source | `ALL_SOURCE`/`DBA_SOURCE` lines; signatures in `ALL_ARGUMENTS`; dependencies in `ALL_DEPENDENCIES`; `ALL_PROCEDURES` flags (deterministic, pipelined, aggregate); wrapped (obfuscated) units are unreadable | Inventory reads the dictionary only (OC-102). Wrapped units are listed as not convertible |
| Running a function | `SELECT f(:1) FROM dual` works for SQL-callable functions. Functions with `BOOLEAN`/record/collection parameters (before 23ai), `OUT` parameters, or DML need an anonymous PL/SQL block | The runner picks the call form per signature (OC-109) |
| Same data as the target | `DBMS_FLASHBACK.ENABLE_AT_SYSTEM_CHANGE_NUMBER(scn)` makes a whole session see the database as of an SCN | The Oracle pane can run "as of the data we migrated" (OC-110) |

### Couchbase UDF facts (confirm in the spike, OC-092)

| Topic | Understanding today | To confirm |
|---|---|---|
| Inline SQL++ UDFs | `CREATE [OR REPLACE] FUNCTION scope.fn(a, b) LANGUAGE INLINE AS (expr)`. The expression may contain subqueries. Server 7.0+ and Capella | Subquery limits; behavior on Server 8.0 and Capella |
| JavaScript UDFs | `CREATE FUNCTION scope.fn(a) LANGUAGE JAVASCRIPT AS "fn" AT "lib"`, with library code uploaded to `/evaluator/v1/libraries/{lib}` (scoped by `bucket`/`scope` since 7.1). The JS can run embedded SQL++ (7.1+) | Inline JS body form; library size limits; execution timeout; whether **Community Edition** (our appliance) and **Capella** allow JS UDFs, and how Capella manages libraries |
| Calling | `SELECT RAW scope.fn(...)`, or `EXECUTE FUNCTION scope.fn(...)`, which is required when the function changes data | Error shape when a JS UDF throws; `readonly` behavior |
| Privileges | Scope roles for managing and executing functions, with separate "external" (JavaScript) variants | Exact role names on 7.6/8.0/Capella, for the preflight (OC-108) |

---

## 3. Domain model additions (metadata store)

| Collection | Document key | Main fields |
|---|---|---|
| `plsql_objects` | `<projectId>::<owner>.<package>.<name>::<overload>` | owner, package (or empty), name, kind (`function`, `procedure`, `package`, `trigger`, `type_body`), overload, signature (params with name, mode, Oracle type, default), returnType, flags (deterministic, pipelined, aggregate, parallelEnabled, wrapped, autonomous), status (`VALID`/`INVALID`), sourceSha256, lineCount, dependencies (tables, views, sequences, other units, `SYS` packages), discoveredAt |
| `plsql_assessments` | `<objectId>` | rating (`automatic`, `assisted`, `manual`, `unsupported`), findings[] (construct, line, rule code, message, effort weight), effortPoints, parserVersion |
| `conversions` | `<objectId>::v<n>` | objectId, version, target (`inline`, `javascript`), udfName, sqlpp (CREATE statement), jsLibrary + jsSource, todos[] (line, rule code, message), origin (`rules`, `ai`, `manual`), basedOnSourceSha256, mappingVersions (the mapping revisions the SQL translation used), createdBy, createdAt |
| `udf_deployments` | `<projectId>::<udfName>` | conversion id and version, keyspace scope, library name, deployedSha256, deployedAt, deployedBy, lastDriftCheck |
| `udf_tests` | `<objectId>::<testId>` | args (JSON), expected (optional), compare options, origin (`manual`, `generated`, `run`), label |
| `udf_runs` | `<runId>` | objectId, conversion version, per case: oracle {result, error, ms}, couchbase {result, error, ms}, verdict (`match`, `mismatch`, `error`), diff; asOfScn; startedBy. Expiry = the run-history retention period |

All new documents carry `workspaceId` and `projectId`, and follow the 1.0 tenant-isolation rules. The `plsql_objects` keys keep re-discovery idempotent. A source change (a new `sourceSha256`) marks existing conversions **stale**; it never overwrites them.

---

## 4. Components

```
                    ┌──────────── source/oracle (go-ora) ─────────────┐
 Source step ──────▶│ connector · discover · preview · read sessions  │──▶ existing pipeline / plan / preview
                    │ dialect (SQL rendering, keys, filters, encode)  │
                    │ plsql: dictionary reader, call runner           │
                    └───────────────────────────┬─────────────────────┘
                                                │ source text, signatures, deps
                    ┌───────────────────────────▼─────────────────────┐
 Functions area ───▶│ internal/plsql                                  │
                    │  parse (ANTLR PL/SQL grammar, Go target)        │
                    │  assess (rules → rating, findings)              │
                    │  translate → JS UDF | inline SQL++ UDF          │
                    │   ├─ builtins (Oracle built-ins → odb runtime)  │
                    │   └─ sqlmap (embedded SQL → SQL++ via mappings) │
                    │  deploy (Couchbase query + evaluator REST)      │
                    │  compare (run both, normalize, diff)            │
                    └─────────────────────────────────────────────────┘
```

`internal/plsql` depends on the mapping store (to translate table and column references) and on the Couchbase target connector (to deploy and run). It never depends on the migration pipeline.

---

## 5. Key decisions

### D1 — Pure-Go Oracle driver
`go-ora/v2`, dialed through the existing `netguard` dialer (egress policy, OC-033). No Instant Client, no cgo, nothing extra in the image or the static `odb` binary. If the spike finds a blocking gap in go-ora, the fallback is a separate, optional cgo build of the Oracle connector, decided with the user. It is not planned.

### D2 — Consistency comes from one SCN
A mapping's read session takes `CURRENT_SCN` once and every partition, related-row and count query reads `AS OF SCN :scn`. This replaces PostgreSQL's exported snapshot behind the same `ReadSession` port (`SnapshotID()` returns the SCN as text). Without `FLASHBACK` privilege the session falls back to one reader inside a `READ ONLY` transaction, like PostgreSQL without snapshot export, and says so in the preflight. When `ORA-01555` hits a long read, the job pauses with `SOURCE_SNAPSHOT_TOO_OLD` and an explanation (raise `UNDO_RETENTION`, or resume, which takes a new SCN with the OC-024 "resnapshotted" warning). **(CDC)** The SCN is recorded in the job checkpoint so 6.0 can start change capture exactly there.

### D3 — A dialect seam, not `if oracle` branches
Shared packages stop assuming PostgreSQL (OC-093). Each source connector provides a `source.Dialect`:

| Concern | Today (PostgreSQL-only) | 4.0 |
|---|---|---|
| Filter arguments (OC-045) | "PostgreSQL input text" | `Dialect.ParseLiteral(column, text)` validates; bind values are typed |
| Partition key kinds (`pipeline/partition.go`) | PG type names | `Dialect.KeyKind(column)` |
| Sorted-merge guarantee (`plan/strategy.go`) | "sorts the same in Go and PostgreSQL" | `Dialect.ByteOrderedKey(column)`: per type and collation |
| Value encoding (`encode`) | `pgtype` cases | `encode` gets neutral cases (decimal string, interval, time of day); each connector converts driver values into them |
| Preview sampling, timeouts | `TABLESAMPLE`, `statement_timeout` | `Dialect` renders the sample and the timeout strategy |
| Export/run descriptions, headless registry | `case "postgres"` | Driven by connector descriptors; `odb run` registers every source in the build |

PostgreSQL behavior must not change: the 2.0 golden-mapping parity test (OC-056) is the guard.

### D4 — We never change a customer's database
As with Edge Server (3.0 D2), the tool only reads the source. Grants, `ALTER` statements or wallet steps a check needs are shown as **exact SQL snippets** for the DBA, and the check is re-run after they apply them. Running a PL/SQL function to compare results is a read: the Oracle pane runs in a transaction that is always rolled back, and units that write (DML, `COMMIT`, autonomous transactions) are blocked unless the user explicitly allows it per run (OC-109).

### D5 — Two UDF targets, picked per function
| Conversion target | Chosen when | Why |
|---|---|---|
| **Inline SQL++ UDF** | The function body is a single expression or `CASE`, optionally with one `SELECT … INTO` that maps to a subquery, with no loops, exceptions or variables beyond the parameters | Runs inside the query engine, so it is fastest and works wherever SQL++ UDFs do |
| **JavaScript UDF** (default for everything else) | Procedural code: variables, `IF`/`LOOP`/`WHILE`/`FOR`, cursors, collections, exceptions, embedded SQL | JavaScript expresses PL/SQL control flow directly. Embedded SQL becomes SQL++ run from JS |

Every JavaScript UDF imports one shared library, **`odb_plsql`** (OC-104). It reproduces Oracle semantics: `NVL`, `DECODE`, 1-based `SUBSTR`/`INSTR`, `''`-is-null, `TO_CHAR`/`TO_DATE` format models, date arithmetic, `NUMBER` rounding with decimal (not binary float) arithmetic, and named exceptions (`NO_DATA_FOUND`, `TOO_MANY_ROWS`, `ZERO_DIVIDE`, `VALUE_ERROR`, user `RAISE_APPLICATION_ERROR`). Inline UDFs use SQL++ built-ins only and are chosen only when every built-in they need has an exact SQL++ equivalent.

### D6 — Embedded SQL is translated through the mappings
A PL/SQL function reads Oracle tables, but after migration those rows live in Couchbase documents whose shape the project's mappings define. The translator (OC-106) resolves each table and column reference through the mappings: table → collection (and key template), column → field path, a child table embedded into its parent → an `UNNEST` over the embedded array, a looked-up row → the embedded object. References the mappings can't resolve (an unmigrated table, a column mapped through an expression) become **TODO findings**, never guesses. A conversion records the mapping versions it used, and a mapping change marks it stale.

### D7 — UDFs need a Couchbase target
Directory and Edge Server targets have no query engine to host UDFs. The Functions area still inventories and assesses PL/SQL for any target, but convert, deploy and run are enabled only when the project's target is Couchbase (Server or Capella). Where the target is Community Edition and the spike finds JS UDFs unavailable there, the UI says so and offers inline-only conversions.

### D8 — AI assistance is optional and off by default
Rule-based translation is the product. AI-assisted conversion (OC-113) is an opt-in workspace setting that sends PL/SQL source to a configured model provider. When it is off, nothing leaves the server. Every AI conversion is labeled as such, starts as a draft, and is accepted only after the side-by-side test cases pass (OC-111).

---

## 6. Oracle type normalization (`jsonType`)

| Oracle | jsonType | Default JSON encoding |
|---|---|---|
| `NUMBER(p, 0)`, p ≤ 15; `INTEGER`, `SMALLINT` | integer | number |
| `NUMBER(p, 0)`, 16 ≤ p ≤ 38 | integer | number when \|v\| ≤ 2^53−1, else string (same option as PostgreSQL `bigint`) |
| `NUMBER(p, s)`, s > 0 | number | number when p ≤ 15, else string (same option as `numeric`) |
| `NUMBER` (no precision), `FLOAT(b)` | number | number when it round-trips exactly, else string; automap may suggest integer (OC-098) |
| `BINARY_FLOAT`, `BINARY_DOUBLE` | number | number (NaN/±Inf → null + warning) |
| `BOOLEAN` (23ai+) | boolean | true/false |
| `VARCHAR2`, `NVARCHAR2`, `VARCHAR` | string | string (`''` arrives as null) |
| `CHAR`, `NCHAR` | string | right-trimmed (same option as PostgreSQL `char`) |
| `CLOB`, `NCLOB`, `LONG` | string | string; size guard (OC-046) applies |
| `BLOB`, `RAW`, `LONG RAW` | string | base64 |
| `DATE` | datetime | ISO-8601 local timestamp `YYYY-MM-DDTHH:MM:SS` (option: assume UTC, or date-only when every value has a zero time) |
| `TIMESTAMP(n)` | datetime | ISO-8601 without zone |
| `TIMESTAMP WITH TIME ZONE` | datetime | ISO-8601 UTC `Z` (option: keep the offset) |
| `TIMESTAMP WITH LOCAL TIME ZONE` | datetime | ISO-8601 UTC `Z` (session time zone set to UTC) |
| `INTERVAL YEAR TO MONTH`, `INTERVAL DAY TO SECOND` | string | ISO-8601 duration |
| `JSON` (21c+), or a `CLOB`/`VARCHAR2` with an `IS JSON` check constraint | any | embedded JSON value |
| `ROWID`, `UROWID` | string | text form |
| `XMLTYPE`, `SDO_GEOMETRY`, object types, `VARRAY`, nested tables, `BFILE` | string | text form + "unsupported type, sent as text" warning (`SDO_GEOMETRY` → GeoJSON is a possible follow-up) |

---

## 7. Connection config (`type: "oracle"`)

| Field | Default | Rules |
|---|---|---|
| `mode` | `service` | `service` \| `sid` \| `descriptor` |
| `host`, `port` | —, 1521 | Required unless `mode = descriptor` |
| `serviceName` / `sid` | — | One, matching `mode`. For a multitenant database, the PDB's service |
| `descriptor` | — | Full TNS descriptor `(DESCRIPTION=…)`; hosts in it still pass the egress policy |
| `tls` | `off` | `off` \| `tls` \| `mtls`; `mtls` needs a wallet |
| `wallet` | — | Uploaded `cwallet.sso` or `ewallet.p12` (+ password), sealed as a secret |
| `schemas` | all non-maintained | Default discovery include list |
| `fetchSize` | 1,000 | 100 – 50,000 rows per round trip |
| secret: `username`, `password` | — | Sealed like every other secret |

---

## 8. Additional non-functional requirements
- The Oracle read throughput on the reference environment is at least 70% of PostgreSQL's for the same dataset and mapping (OC-114 benchmark gate), with no document lost or duplicated on resume.
- Discovery of a 2,000-table schema completes in under 60 s (dictionary queries are set-based, never one per table).
- Assessing 1,000 PL/SQL units completes in under 2 minutes. A conversion of one unit takes under 2 s, without AI.
- A side-by-side run of one test case returns both results in under 5 s plus the functions' own run time. Each pane has its own timeout (default 30 s).

## 9. Definition of Done additions
- Oracle integration tests run against `gvenzl/oracle-free` pinned by digest (`make test-integration-oracle`), with a nightly 19c leg when a 19c image is available to CI (spike OC-091 decides).
- Oracle dev seed `deploy/dev/oracle/` mirrors the PostgreSQL seed (same `shop` and `sales` shapes) plus a PL/SQL corpus of at least 40 units covering every assessment rule. It is used by unit, integration and E2E tests.
- Every converter rule has a golden test: PL/SQL in, generated SQL++/JS out, plus a side-by-side equivalence test that runs in the integration suite.
- PostgreSQL parity is unchanged: the OC-056 golden-mapping test passes byte-for-byte.
