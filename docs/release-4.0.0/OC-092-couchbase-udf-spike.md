# OC-092 — Couchbase UDF spike: what our converted functions can rely on

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 2 | — | Not started |

## User story
As an **engineer**, I want to know exactly what Couchbase user-defined functions support on every target we sell into, so that the converter generates functions that actually deploy and run.

## Scope
**In:** a findings note (`docs/release-4.0.0/spikes/couchbase-udf.md`) that updates the UDF table in [architecture §2](00-architecture.md#couchbase-udf-facts-confirm-in-the-spike-oc-092). Questions, each answered on Server 7.6 EE, Server 8.0 EE, **Server 8.0 CE** (our appliance) and Capella:

| # | Question |
|---|---|
| 1 | Are inline SQL++ and JavaScript UDFs available? Scoped or global only? |
| 2 | How are JS libraries created, updated and deleted (REST path, scoping, auth)? Is there an inline JS body form? Size and count limits? How does Capella manage them? |
| 3 | Can JS UDFs run embedded SQL++ (`SELECT`, iterators, parameters)? When must `EXECUTE FUNCTION` be used instead of `SELECT`? |
| 4 | What does a caller see when a JS UDF throws (error code, message, our custom error name and fields)? |
| 5 | Execution timeout and memory limits; cost of a JS UDF call vs an inline one (micro-benchmark: 10k calls) |
| 6 | The roles needed to create, replace, drop and execute inline and external functions, and how to probe them without side effects |
| 7 | JavaScript number semantics inside UDFs: can we ship a decimal library (for `NUMBER` arithmetic) inside the `odb_plsql` library, and at what cost? |
| 8 | Does `readonly=true` on the query request stop a UDF's embedded DML? |

**Out:** production code.

## Acceptance criteria

### AC1 — Support matrix
**Given** the findings note, **then** it has a matrix (question × server flavor) with the evidence for each cell, and D5/D7 in the architecture are confirmed or changed.

### AC2 — Decision for Community Edition
**Given** the CE answers, **then** the note says what the appliance offers (for example "inline only"), and OC-108 and OC-114 are updated to match.

| Test | Location | Verifies |
|---|---|---|
| Spike findings review | `docs/release-4.0.0/spikes/couchbase-udf.md` | AC1, AC2 |
