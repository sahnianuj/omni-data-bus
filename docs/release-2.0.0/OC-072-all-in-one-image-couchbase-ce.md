# OC-072 — All-in-one image: Omni-Data-Bus with a bundled Couchbase Server CE node

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 8 | OC-032, OC-036, OC-058 | Implemented |

## User story
As a **solutions engineer handing Omni-Data-Bus to a customer**, I want one Docker image that holds the app **and** a single-node Couchbase Server Community Edition (CE) that the app uses as its metadata store (and, optionally, as a migration target), so that the customer runs one `docker run` command and has a working tool without setting up Capella, a Couchbase cluster or any other service.

## Scope
**In:**
- A second image, the **appliance** (`omni-data-bus-appliance:<version>`), built from `deploy/appliance/Dockerfile`. The existing distroless `omni-data-bus` image is unchanged.
- Couchbase Server CE (single node: data, index, query) and the same `odb` binary as the regular image, in one container.
- First start initializes everything without user input: the cluster, the metadata bucket, a `sandbox` target bucket, the generated secrets. Later starts reuse them.
- All state on one volume, so the container can be removed and recreated (and upgraded) without losing projects, users or migrated data.
- Clean shutdown on `docker stop`: the app drains and checkpoints running jobs before Couchbase stops.
- A **Use the bundled Couchbase** shortcut on the Target step, so a project can migrate into the bundled node without typing its address or password.
- Proof that the app works on CE: the metadata-store and pipeline integration suites run against the CE node.

**Out:** clustering or more than one node, Couchbase Enterprise features, TLS inside the container (put a proxy in front, as for the regular image), Kubernetes or Helm for the appliance. Building, packaging and sharing the image is OC-073; the customer run kit and README are OC-074.

## Implementation details

### Image layout (`deploy/appliance/Dockerfile`)
- Stages 1–2 are the web and binary stages of `deploy/docker/Dockerfile`, reused (shared through a `--target` or a common include), so the `odb` binary is byte-identical to the one in the regular image for the same `VERSION`/`COMMIT`.
- Runtime base: `couchbase/server:community-<8.0.x>`, the newest 8.0.x community tag when the story is implemented, **pinned by digest** in an `ARG CB_IMAGE`. Must publish both `linux/amd64` and `linux/arm64`; if the chosen tag does not, the Dockerfile fails the build for the missing platform with a clear message.
- Adds `tini` as PID 1, `/usr/local/bin/odb`, and `/usr/local/bin/odb-appliance` (the entrypoint script below).
- Labels: the OCI labels of the regular image, plus `io.omnidatabus.couchbase.edition=community`, `io.omnidatabus.couchbase.version=<x.y.z>`.
- `EXPOSE 8080 8091`. Only 8080 needs publishing; 8091 (Couchbase web console) is for support and is documented as `127.0.0.1`-only.
- `VOLUME /opt/couchbase/var` holds everything: Couchbase data plus `/opt/couchbase/var/odb/` (secrets, appliance state, directory-target output under `odb/out`).

### Entrypoint and process supervision (`deploy/appliance/odb-appliance.sh`)
We use our own small supervisor under `tini` rather than adding `odb` as a runit service in the Couchbase image: runit's `runsvdir` exits on SIGTERM without stopping its services in order, and we need the app to stop **before** Couchbase so jobs checkpoint into the metadata store.

1. Start Couchbase with the base image's own entrypoint in the background; wait for `http://127.0.0.1:8091/ui/index.html` (timeout `ODB_CB_START_TIMEOUT`, default 180 s).
2. **First start** (no `/opt/couchbase/var/odb/initialized`):
   - Generate secrets into `/opt/couchbase/var/odb/secrets/` (mode 0600): `master_key` (`odb admin gen-key`), `cb_admin_password` (32 random characters). Values given through `ODB_CB_ADMIN_PASSWORD(_FILE)` or `OC_MASTER_KEY(_FILE)` win over generation.
   - `couchbase-cli cluster-init` (services data, index, query; cluster name `omni-data-bus`), RAM quotas from the container's memory limit (cgroup), with the table below.
   - Create bucket `omniconnect` (metadata; 256 MB) and bucket `sandbox` (target; the rest of the data quota), replicas 0. The same idempotent logic as `deploy/compose/couchbase/init.sh`; share the script rather than copying it.
   - Write `initialized` last, so an interrupted first start is redone, not left half-done.
3. Export the app's settings and start `odb` in the foreground as uid 1000 (`couchbase` user), not root:

   | Setting | Value |
   |---|---|
   | `OC_META_CONNSTR` | `couchbase://127.0.0.1` |
   | `OC_META_USERNAME` / `OC_META_PASSWORD_FILE` | `Administrator` / `…/secrets/cb_admin_password` |
   | `OC_META_BUCKET` / `OC_META_SCOPE` / `OC_META_AUTO_PROVISION` | `omniconnect` / `meta` / `true` |
   | `OC_MASTER_KEY_FILE` | `…/secrets/master_key` |
   | `OC_ROLE`, `OC_HTTP_ADDR`, `OC_LOG_FORMAT` | `all`, `:8080`, `json` |
   | `OC_ALLOW_PRIVATE_NETWORKS` | `true` (the bundled node is on loopback) |
   | `OC_PUBLIC_URL` | `ODB_PUBLIC_URL` if set, else `http://localhost:8080` |

   Any `OC_*` variable passed to `docker run` overrides these defaults, except `OC_META_*` (the appliance always uses its bundled store; a warning is logged if one is passed).
   Also, as built: `OC_SESSION_COOKIE_SECURE=false` unless `OC_PUBLIC_URL` is https (Secure cookies are dropped over plain http, so signing in at `http://<server>:8080` would fail); `HOME=/opt/couchbase/var/odb/home`, so a directory target's allowed area is on the volume; `odb` only starts once the `omniconnect` bucket is healthy and the query service answers (the server doesn't retry the metadata store at startup).
4. On SIGTERM/SIGINT: send SIGTERM to `odb`, wait for it (it drains for up to 30 s, OC-023), then stop Couchbase (`couchbase-server -k`), then exit 0. Documented `--stop-timeout 90`.
5. If either process dies unexpectedly, stop the other and exit non-zero, so Docker's restart policy restarts the whole container.

RAM quotas (container memory limit `M`; with no limit, the host memory is used and capped at 8 GiB):

| `M` | Data quota | Index quota | Left for `odb` and the OS |
|---|---|---|---|
| < 3 GiB (5 % slack for what Docker Desktop reports) | refuse to start: "the appliance needs at least 3 GiB of memory" (exit 78) | | |
| 3–4 GiB | 1024 MB | 256 MB | ≥ 1.7 GiB |
| > 4 GiB | 40 % of `M` | 10 % of `M` | 50 % |

Overridable with `ODB_CB_DATA_RAM_MB` and `ODB_CB_INDEX_RAM_MB`.

### Health
- `HEALTHCHECK CMD odb healthcheck` (this base has a shell, unlike distroless). `/readyz` already fails while the metadata store is unreachable, so it covers both processes.
- Startup logs: one line `appliance ready: open http://localhost:8080` when `/readyz` first passes; the generated Couchbase admin password is **never** logged. `docker exec <c> odb-appliance show-credentials` prints it for support.

### "Use the bundled Couchbase" (Target step)
- The entrypoint sets `OC_BUNDLED_TARGET=couchbase://127.0.0.1`, `OC_BUNDLED_TARGET_USERNAME`, `OC_BUNDLED_TARGET_PASSWORD_FILE` (read like the other `_FILE` secrets), `OC_BUNDLED_TARGET_BUCKETS=sandbox` and `OC_BUNDLED_STORE` (`config.BundledConfig`). `GET /target-kinds` then returns `bundled: {kind: "couchbase", label: "Bundled Couchbase (this container)", buckets: ["sandbox"]}`.
- The Target step shows **Use the bundled Couchbase** (single line, description in the tooltip) above the Couchbase form. It calls `POST /projects/{id}/connections/target/bundled`, which saves a normal Couchbase target named *Bundled Couchbase* with the server's password and tests it, returning `{connection, test}` (`connections.Service.SaveBundled`). The password never passes through the browser; it is stored sealed like every other credential. (Built this way rather than resolving the file at every connect: it reuses the whole stored-connection path unchanged.)
- The regular image sets none of this: no button, and the endpoint answers 404.
- **Deferred to OC-050:** refusing to export a project whose target is the bundled node (`BUNDLED_TARGET_NOT_EXPORTABLE`); there is no export yet.

### Couchbase CE compatibility
- `scripts/integration-ce.sh` (`make test-integration-ce`) starts a throwaway CE node with no published ports and runs `go test -tags=integration` inside its network namespace, so it never clashes with the dev stack's Enterprise node; PostgreSQL is the dev stack's. `IT_CB_IMAGE=…` runs the same tests against another image, to compare.
- Result (2026-10-01): every package that touches Couchbase passes on CE 8.0.2: the target connector, keyspaces, writer, the metadata store (`store/cbstore`) and provisioning, preview, mappings, jobs.
- One CE difference: **collection max expiry (`maxTTL`) is Enterprise-only**. Creating a collection with one on CE now fails with *"Max expiry needs Enterprise Edition … Leave max expiry empty"* instead of Couchbase's raw "invalid argument" (`mapErr`/`enterpriseOnly`).
- Durability: the appliance's buckets have 0 replicas, and levels other than *None* already need replicas (existing preflight check), so nothing CE-specific was needed.

### Licensing check (before release) — open
Shipping Couchbase Server CE inside an image we hand to customers is redistribution. Before the first customer delivery, confirm with Couchbase legal/product that the Community Edition license allows it for this use, and record the answer (and any required notice) in `deploy/appliance/NOTICE`. The image includes the Couchbase CE license text from the base image unchanged.

## Acceptance criteria & unit tests

The image-level checks are `deploy/appliance/test/smoke.sh` (`make appliance-test`, 26 checks against a real container, about 3 minutes) and `deploy/appliance/structure-test.yaml` (11 checks). Results below are from 2026-10-01 on Docker Desktop (arm64); the amd64 image passed the structure test under emulation, and the release job runs the smoke test on amd64.

### AC1 — One command to a working app
**Given** a machine with only Docker, **when** `docker run -d -p 8080:8080 -v odb-data:/opt/couchbase/var --memory 4g omni-data-bus-appliance:<v>` runs, **then** within 4 minutes the container is healthy, the sign-up page works over plain http, and the footer shows the release, build and "Couchbase Server CE <x.y.z> (bundled)".

| Test | Location | Verifies |
|---|---|---|
| `smoke.sh` › AC1 | `deploy/appliance/test/smoke.sh` | Healthy in 58 s; sign-up; cookie not Secure-only; `bundledStore` in /version |
| `TestVersionAPI_OC072_BundledStore` | `backend/internal/httpapi/version_test.go` | |
| `TestLoad_BundledTarget_OC072` | `backend/internal/config/config_test.go` | Settings, password from a file |
| `BuildInfoFooter.test.tsx` › `OC-072 AC1` | `web/src/components/BuildInfoFooter.test.tsx` | Footer and "Copy build info" |

### AC2 — Data survives restarts, recreation and upgrade
**Given** an initialized appliance with a user, **when** the container is stopped and started, **or** removed and recreated on the same volume, **or** upgraded from an archive, **then** the user can sign in and first-start setup doesn't run again.

| Test | Location | Verifies |
|---|---|---|
| `smoke.sh` › AC2 | `deploy/appliance/test/smoke.sh` | Restart 13 s, recreate 28 s, setup ran once, sign-in works |
| `docs_commands.sh` › data round trip | `deploy/appliance/test/docs_commands.sh` | `run.sh upgrade` keeps the account (OC-074) |

### AC3 — Clean shutdown
**Given** a running appliance, **when** `docker stop --time 90` is sent, **then** Omni-Data-Bus stops before Couchbase, the container exits 0 well within 90 s, and no error is logged.

| Test | Location | Verifies |
|---|---|---|
| `smoke.sh` › AC3 | `deploy/appliance/test/smoke.sh` | Order of log lines, exit 0, stopped in 3 s |

Not exercised here: stopping **during** a migration and resuming it. The supervisor's part (SIGTERM to `odb` first, then wait up to 45 s) is checked above; draining and resuming from a checkpoint is the server's existing behavior (OC-023, OC-029), unchanged.

### AC4 — Failures are clear
**Given** a 2 GiB memory limit, **then** the container exits 78 with the memory message. **Given** `odb` crashes, **then** Couchbase is stopped and the container exits 1, saying why. Any unexpected script failure logs `appliance: error: line N failed`.

| Test | Location | Verifies |
|---|---|---|
| `smoke.sh` › AC4 | `deploy/appliance/test/smoke.sh` | Exit 78 + message; `kill -9` of odb → exit 1 + message |

### AC5 — Secrets stay secret
**Given** a first start, **then** the generated Couchbase password (~60 characters) and master key are files of mode 0600 owned by `couchbase`, no secret appears in `docker logs`, `docker inspect` or API answers, the app runs as `couchbase` (uid 1000), and `odb-appliance show-credentials` prints the password for support. `ODB_CB_ADMIN_PASSWORD(_FILE)` and `OC_MASTER_KEY(_FILE)` given on the first start win over generated ones.

| Test | Location | Verifies |
|---|---|---|
| `smoke.sh` › AC5 | `deploy/appliance/test/smoke.sh` | |
| `TestSaveBundled_OC072_AC6_SavesAndTests` | `backend/internal/connections/bundled_test.go` | Password not in the response; stored sealed |

### AC6 — Migrate into the bundled node
**Given** the appliance, **when** the user clicks **Use the bundled Couchbase**, **then** the connection is saved and tests green, `sandbox` is browsable, and a migration from PostgreSQL writes its documents there. **Given** the regular image, **then** there is no button and the endpoint is 404.

| Test | Location | Verifies |
|---|---|---|
| `TestTargetKinds_OC072_AC6_BundledOnlyWhenConfigured` | `backend/internal/connections/bundled_test.go` | |
| `TestSaveBundled_OC072_AC6_SavesAndTests`, `…_NotOnRegularImage` | same | |
| `TargetKindPicker.test.tsx` › `OC-072 AC6` (2 tests) | `web/src/features/targets/TargetKindPicker.test.tsx` | Button, hidden for directory and on the regular image |
| `appliance.spec.ts` › `OC-072 AC6: migrate into the bundled Couchbase` | `web/tests/e2e/appliance.spec.ts` | Full UI flow against the container (`E2E_APPLIANCE=1`): passed, verification "Passed" |
| `smoke.sh` › AC6 | `deploy/appliance/test/smoke.sh` | API: saved, tested, Community Edition |
| *(deferred to OC-050)* `TestExports_AC6_BundledTargetRefused` | | |

### AC7 — Works on Community Edition
**Given** `make test-integration-ce`, **then** the integration suites that use Couchbase pass against CE, and the one CE difference (collection max expiry) gives a clear error.

| Test | Location | Verifies |
|---|---|---|
| `go test -tags=integration` via `scripts/integration-ce.sh` | `backend/internal/...` | Couchbase packages all pass on CE 8.0.2 |
| `TestCreateCollection_AC2_Integration` (`OC_IT_CB_EDITION=community` branch) | `backend/internal/connectors/target/couchbase/keyspace_integration_test.go` | |
| `TestMapErr_OC072_EnterpriseOnlyTTL` | `backend/internal/connectors/target/couchbase/keyspace_test.go` | |

Failures seen in the same run that are **not** CE-related (they fail identically against Enterprise 8.0.3 in this harness, or don't touch Couchbase): `TestEmbeds_AC5_Integration`, `TestVerify_AC3_Integration` (both also fail on Enterprise), `TestDiscover_AC1_SeedSchema` and `TestTester_AC4_Integration` (the dev database now has extra seed tables), `TestSuggestBindings_AC8_Budget` (a 1 s timing budget, 1.27 s inside a container), `TestCheckGen_AC4_DetectsDiff` (needs the repository's git state).
