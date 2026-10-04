# OC-091 — Oracle driver and test environment spike

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Oracle Source | P0 | 3 | — | Not started |

## User story
As an **engineer**, I want the Oracle questions the source connector depends on answered against real databases before we build it, so that the stories that follow are built on facts rather than assumptions.

## Scope
**In:** a throwaway branch and a findings note (`docs/release-4.0.0/spikes/oracle-driver.md`). The findings update [architecture §2](00-architecture.md#2-oracle-facts-that-shape-the-design) and the affected stories. Questions:

| # | Question | How |
|---|---|---|
| 1 | Does `go-ora/v2` build and run with `CGO_ENABLED=0` on linux/amd64, linux/arm64 and darwin/arm64, inside the static `odb` binary? | Build matrix |
| 2 | Can go-ora dial through our `netguard` dialer (egress policy, OC-033), including hosts named inside a TNS descriptor? | Prototype |
| 3 | TLS and mTLS: does a wallet (`cwallet.sso`, `ewallet.p12`) work against Autonomous Database (if a free-tier instance is available) and a self-signed TCPS listener? | Prototype |
| 4 | How each type in [§6](00-architecture.md#6-oracle-type-normalization-jsontype) decodes: `NUMBER` precision (scan to string?), `DATE`, all `TIMESTAMP` kinds and session time zone, `INTERVAL`, LOBs (streamed or whole), `JSON`, `BOOLEAN`, `XMLTYPE` | Type matrix table on 19c and 23ai |
| 5 | Does `SELECT … AS OF SCN :scn` work with bind variables across several sessions at once, and what error and timing does `ORA-01555` give with a tiny `UNDO_RETENTION`? | Prototype |
| 6 | Does cancelling a context stop a running statement reliably (break/reset), and does the session stay usable? | Prototype |
| 7 | Which images can CI use? `gvenzl/oracle-free` (23ai/26ai) startup time and memory; a 19c option (`gvenzl/oracle-xe:21` as a stand-in, or a licensed 19c image); RDS for Oracle notes | Measure |
| 8 | Bulk read throughput: rows/s with `fetchSize` 1,000 / 10,000 on a 1M-row table, against PostgreSQL on the same host | Measure |
| 9 | Can `DBMS_FLASHBACK.ENABLE_AT_SYSTEM_CHANGE_NUMBER` be used from go-ora, and does it apply to functions called in that session? (for OC-110) | Prototype |

**Out:** production code.

## Acceptance criteria

### AC1 — Every question answered
**Given** the findings note, **then** each question has an answer, the evidence (test output, measurement, or doc link), and the consequence for a named story.

### AC2 — Go or no-go on the driver
**Given** the answers to questions 1–6, **then** the note states go or no-go for go-ora. A no-go is raised with the user before OC-093 starts ([D1](00-architecture.md#d1--pure-go-oracle-driver)).

### AC3 — Test environment chosen
**Given** the answers to question 7, **then** `make test-integration-oracle` is specified (image, digest, startup wait, seed path) and its runtime in CI is known.

| Test | Location | Verifies |
|---|---|---|
| Spike findings review | `docs/release-4.0.0/spikes/oracle-driver.md` | AC1–AC3 |
