# OC-190 — Connection failures: decided in 5 seconds, explained in plain words

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 3 | OC-181, OC-182 | Implemented |

## User story
As an **operator** running `odb preflight` (or `odb run`) in an export folder, I want a source or target I can't reach to be reported **within 5 seconds**, in **one plain sentence that says what to change and where**, so that I fix `config.json` or the network instead of waiting and decoding SDK errors.

## Background (2026-10-19)
A run against a Couchbase target the machine can't reach prints this after about 20 seconds:

```
WARN  Source repository reachable — This user can modify data. A read-only role is safer for migrations.
FAIL  Target repository reachable — unambiguous timeout | {"operation_id":"WaitUntilReady","time_observed":10000902,"retry_reasons":["NOT_READY","CONNECTION_ERROR"],"retry_attempts":27}
FAIL  Permission to write documents — The target credential can't write documents.
OK    Consistent snapshot export
FAIL  public.carts: target target.oe.carts — Check the host and port, and that this server can reach the database (firewalls, security groups, allowed IPs). (unambiguous timeout | {"operation_id":"WaitUntilReady", …})
…
```

What goes wrong:

| Problem | Cause |
|---|---|
| **Slow**: about 20 s before the verdict | The connection test waits for the whole `connectTimeoutMs` (10 s, the default saved in every export). Then the per-mapping target checks open the cluster again and wait another 10 s. With Capella, the SDK's WAN profile raises this to 20 s per wait. |
| **Cryptic**: the SDK's JSON error is the message | The Couchbase test puts the SDK error into the check's `Detail`. `odb` prints `Detail` and never prints `Hint`. |
| **Misleading**: "can't write documents" | When the test fails, the capabilities it reports are all false. Pre-flight reads them as "no write permission" and adds a second FAIL that isn't true. |
| **Repeated**: the same timeout under every mapping | The per-mapping target checks run even when the target can't be reached, so each one repeats the connection error. |
| **No direction**: it doesn't say what to change | Nothing points at the field in `config.json` (`target.connectionString`, `source.host`/`source.port`) or says what the likely cause is: wrong host, nothing listening, or a firewall dropping packets. |

## Scope
**In:**
- **A reachability step before the driver, with a 5 s budget** (`connectors.ReachTimeout`), in both connectors' *Test*. It resolves the host and opens a TCP connection to the address the driver will use, through the egress guard. It tells three causes apart:
  - **the name doesn't resolve** (DNS);
  - **the connection is refused**: the host answers, but nothing listens on that port;
  - **no answer**: a firewall, security group or IP allow list is dropping the packets, or the host is down.
  - Couchbase: every bootstrap host in `connectionString`, on its port or the scheme's default KV port (11210, or 11207 for `couchbases://`). For one host with no port, the DNS SRV record is used if there is one, as the SDK does (Capella). One reachable node is enough.
  - PostgreSQL: the existing *Resolve host* and *Open TCP connection* steps, now limited to 5 s.
- **The connection test also decides within 5 s once the network is fine**:
  - Couchbase: the SDK's *WaitUntilReady* in the test waits at most 5 s.
  - PostgreSQL: the *Authenticate* step's connection waits at most 5 s.
  - `connectTimeoutMs` / `connectTimeoutSec` still apply to the run itself (the writer and readers), where a slow WAN bootstrap must not fail a migration.
- **Plain messages.** A failed connection check's `Detail` is one sentence naming the host and port and what happened, for example "No answer from cb.example.com:11210 within 5s." Its `Hint` says what to do. The driver's own text is kept in a new `cause` field, for the JSON output and the app's details.
- **`odb preflight` prints the fix.** Under a failed or warning check that has a hint, an indented `→` line shows it. For connection checks, the hint names the `config.json` field to check (`target.connectionString`, `source.host` and `source.port`).
- **No follow-on noise when a repository can't be reached:**
  - the *Permission to write documents* check is only added when the target test succeeded;
  - the per-mapping target checks are skipped (`SKIP … Not checked: the target isn't reachable.`) and don't open the cluster again.
  - Together this keeps `odb preflight` against an unreachable target within about 5 s.

**Out:**
- Changing the run's own timeouts (`connectTimeoutMs`, `kvTimeoutMs`).
- The web app's connection form. It benefits from the faster test and plainer messages without UI changes, and can show `cause` later.
- Retrying a failed connection.

## Implementation details
- `internal/connectors/reach.go` (new):
  - `ReachTimeout = 5 * time.Second`;
  - `Reach(ctx, role, dial, addrs)` dials the addresses concurrently and returns nil as soon as one connects;
  - otherwise it returns a `*ReachError{Role, Addr, Reason}`, with `Reason` one of `dns`, `refused` or `timeout`, and a plain `Message()` and `Hint()`.
  - `Classify` maps a `ReachError` to `KindDNS`, `KindUnreachable` or `KindTimeout`, so codes (`TARGET_TIMEOUT`, `SOURCE_DNS`, …) stay stable.
- `connectors.TestStep` gains `Cause string json:"cause,omitempty"`, and `jobs.Check` gains the same field.
- Couchbase (`connector.go`):
  - a new step `reach` ("Network") runs before `bootstrap`; it uses `bootstrapAddrs(cfg)` (`egress.go`, shared with `checkBootstrap`) plus an SRV lookup;
  - `bootstrap` in *Test* uses `min(connectTimeoutMs, ReachTimeout)`;
  - `failStep` writes a plain detail and keeps the SDK text in `Cause`.
- PostgreSQL (`connector.go`): the `resolve` and `connect` steps run under a `ReachTimeout` context with plain messages, and `auth` opens with `ConnectTimeout` capped at 5 s.
- `internal/jobs/preflight.go`:
  - `targetCheck` returns capabilities only when the check didn't fail;
  - `targetChecks` returns `skipped` checks without calling the target when the target check failed.
- `cmd/odb/run.go`:
  - `printPreflight` prints `SKIP` for skipped checks and a `→ hint` line;
  - `configHint` adds the `config.json` field for the `source` and `target` checks.

## Acceptance criteria & unit tests

### AC1 — An unreachable target is decided within 5 s
**Given** a target whose host drops packets, **when** `odb preflight` runs, **then** it exits with 4 within about 5 s (CI allows 8 s). The target check says "No answer from HOST:PORT within 5s" with a hint, and no other check opens the target.

| Test | Location | Verifies |
|---|---|---|
| `TestReach_OC190_AC1_Timeout` | `internal/connectors/reach_test.go` | A blackhole address fails as `timeout` within the budget |
| `TestCouchbaseTest_OC190_AC1_UnreachableFast` | `internal/connectors/target/couchbase/couchbase_test.go` | The `reach` step fails, `bootstrap` is skipped, and the SDK is never called |
| `TestPreflight_OC190_AC1_TargetDownSkipsTargetChecks` | `internal/jobs/preflight_test.go` | The target is called once; the per-mapping checks are `skipped` |

### AC2 — Each cause is named, with what to do
**Then** DNS failure, connection refused and no answer each give their own sentence and hint, and the codes are `*_DNS`, `*_UNREACHABLE` and `*_TIMEOUT`.

| Test | Location | Verifies |
|---|---|---|
| `TestReach_OC190_AC2_Causes` | `internal/connectors/reach_test.go` | Table over DNS, refused and timeout: message, hint and kind |
| `TestPostgresTest_OC190_AC2_Refused` | `internal/connectors/source/postgres/connector_test.go` | |

### AC3 — No false "can't write documents"
**Given** an unreachable target, **then** there's no *Permission to write documents* check. **Given** a reachable target whose credential can't write, **then** it's still reported.

| Test | Location | Verifies |
|---|---|---|
| `TestPreflight_OC190_AC3_NoWriteCheckWhenUnreachable` | `internal/jobs/preflight_test.go` | |

### AC4 — The terminal shows the fix and where to make it
**Then** `odb preflight` prints `→` and the hint under each failed check, naming `target.connectionString` (or `source.host`/`source.port`) in `config.json`, and skipped checks as `SKIP`. `--json` includes `hint` and `cause`.

| Test | Location | Verifies |
|---|---|---|
| `TestCLI_OC190_AC4_PreflightHints` (golden) | `backend/cmd/odb/run_test.go` | |

### AC5 — The run's own timeouts are unchanged
**Then** the writer and the readers still use `connectTimeoutMs` and `connectTimeoutSec`; only the connection *test* is limited to 5 s.

| Test | Location | Verifies |
|---|---|---|
| `TestCouchbaseTest_OC190_AC5_TestCapsBootstrapOnly` | `internal/connectors/target/couchbase/couchbase_test.go` | The test's WaitReady gets at most 5 s even when `connectTimeoutMs` is 20000 |

## Implementation notes (2026-10-19)
- **The probe sits inside the existing steps.** It runs in Couchbase `bootstrap` and PostgreSQL `connect`, not as a new step, so the web app's step list and existing tests keep their layout.
  - `connectors.Reach` and `ReachError` are in `internal/connectors/reach.go`.
  - `ConnError.Hint()` returns the probe's hint when the probe found the cause.
- **Source down before the checks.** `odb` reads the source schema (the drift check) before pre-flight runs, and that path also waited for the driver's timeout. `headless.discover` now runs the source's connection test first and stops with its plain detail and hint (`ConfigError.Hint`).
- **The snapshot check** now runs after the source check and is skipped when the source can't be reached, because it would open the source again.
- **Deviation from the scope:** the Couchbase test is capped at 5 s *per phase*:
  - an unreachable cluster is decided by the probe within 5 s;
  - a reachable one that doesn't bootstrap (for example a wrong password, which the SDK reports only as a timeout) waits up to 5 s for the SDK, then the existing auth probe tells the cause. That totals about 5–6 s.
- **Measured** with the built `odb` and the real connectors:

  | Case | Before | Now |
  |---|---|---|
  | Blackhole source (`10.255.255.1`) | about 10 s | 5.06 s, `SOURCE_TIMEOUT` |
  | DNS failure | about 10 s | 0.09 s, `SOURCE_DNS` |
  | Refused port | under 1 s | under 1 s, now with a plain message |
  | Blackhole Couchbase | about 10 s, plus 10 s for the per-mapping checks | 5.1 s, `TARGET_TIMEOUT` |
  | Couchbase on a closed port | about 10 s | instant |
  | Local Couchbase, wrong password | about 10 s | 5.3 s, `TARGET_AUTH_FAILED` |

- **Not run here:** the integration suites (Docker), and an `odb preflight` against a Couchbase-target export. The exporttest fixture only builds directory targets, so the target path was checked through the connector and the pre-flight unit tests.
- `api/openapi.yaml` gains `cause` on `TestStep` and `PreflightCheck`, and the web types were regenerated (`make gen`).
