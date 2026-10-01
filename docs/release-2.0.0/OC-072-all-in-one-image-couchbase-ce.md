# OC-072 — All-in-one image: Omni-Data-Bus with a bundled Couchbase Server CE node

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 8 | OC-032, OC-036, OC-058 | Not started |

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
4. On SIGTERM/SIGINT: send SIGTERM to `odb`, wait for it (it drains for up to 30 s, OC-023), then stop Couchbase (`couchbase-server -k`), then exit 0. Documented `--stop-timeout 90`.
5. If either process dies unexpectedly, stop the other and exit non-zero, so Docker's restart policy restarts the whole container.

RAM quotas (container memory limit `M`; with no limit, the host memory is used and capped at 8 GiB):

| `M` | Data quota | Index quota | Left for `odb` and the OS |
|---|---|---|---|
| < 3 GiB | refuse to start: "the appliance needs at least 3 GiB of memory" (exit 78) | | |
| 3–4 GiB | 1024 MB | 256 MB | ≥ 1.7 GiB |
| > 4 GiB | 40 % of `M` | 10 % of `M` | 50 % |

Overridable with `ODB_CB_DATA_RAM_MB` and `ODB_CB_INDEX_RAM_MB`.

### Health
- `HEALTHCHECK CMD odb healthcheck` (this base has a shell, unlike distroless). `/readyz` already fails while the metadata store is unreachable, so it covers both processes.
- Startup logs: one line `appliance ready: open http://localhost:8080` when `/readyz` first passes; the generated Couchbase admin password is **never** logged. `docker exec <c> odb-appliance show-credentials` prints it for support.

### "Use the bundled Couchbase" (Target step)
- The server learns about the bundled node from `OC_BUNDLED_TARGET=couchbase://127.0.0.1` plus `OC_BUNDLED_TARGET_PASSWORD_FILE`, set only by the appliance entrypoint. `GET /target-kinds` returns `bundled: {kind: "couchbase", label: "Bundled Couchbase (this container)", buckets: ["sandbox"]}` when set.
- The Target step shows a **Use the bundled Couchbase** button (single-line, per the UI height rule) next to the Couchbase connection form. It saves a target connection with `bundled: true`. The password is resolved server-side from the file at connect time, so it is never stored in the project or shown in the UI.
- Exports (OC-050/052) of a project with a bundled target refuse with `BUNDLED_TARGET_NOT_EXPORTABLE`: `127.0.0.1` means nothing outside the container.
- The regular image never sets these variables, so the button never appears there.

### Couchbase CE compatibility
- Add a CE variant of the integration environment: `deploy/compose/ce.yml` (same as `dev.yml` with `couchbase/server:community-<8.0.x>`), plus `make test-integration-ce`.
- Anything the app uses that CE lacks gets a CE-safe path or is documented as unavailable in the appliance. Check in particular: durability levels above `none` on a single node with 0 replicas (writer must fall back to `none` with a preflight warning), auto-provisioning of scopes/collections and the indexes the metadata store creates, and any Enterprise-only query features.

### Licensing check (before release)
Shipping Couchbase Server CE inside an image we hand to customers is redistribution. Before the first customer delivery, confirm with Couchbase legal/product that the Community Edition license allows it for this use, and record the answer (and any required notice) in `deploy/appliance/NOTICE`. The image includes the Couchbase CE license text from the base image unchanged.

## Acceptance criteria & unit tests

### AC1 — One command to a working app
**Given** a machine with only Docker, **when** `docker run -d -p 8080:8080 -v odb-data:/opt/couchbase/var --memory 4g omni-data-bus-appliance:<v>` runs, **then** within 3 minutes the container is healthy, `http://localhost:8080` shows the sign-up page, and the footer shows the release, build and "Couchbase CE <x.y.z> (bundled)".

| Test | Location | Verifies |
|---|---|---|
| `appliance_smoke.bats` › `AC1: first start becomes healthy` | `deploy/appliance/test/appliance_smoke.bats` | CI job `appliance` |
| `TestBuildInfo_AC1_BundledStoreReported` | `internal/buildinfo/buildinfo_test.go` | Footer data includes the bundled edition/version |

### AC2 — Data survives restarts, recreation and upgrade
**Given** a project with a completed migration into `sandbox`, **when** the container is stopped and started, **or** removed and recreated on the same volume, **or** recreated from a newer appliance image, **then** the user can log in with the same password, the project and job history are there, the migrated documents are in `sandbox`, and first-start initialization does not run again.

| Test | Location | Verifies |
|---|---|---|
| `appliance_smoke.bats` › `AC2: restart and recreate keep data` | `deploy/appliance/test/appliance_smoke.bats` | |
| `appliance_smoke.bats` › `AC2: upgrade from previous appliance image` | same | Previous release's image → this build |

### AC3 — Clean shutdown
**Given** a migration is running, **when** `docker stop --time 90` is sent, **then** the log shows the app draining and checkpointing before Couchbase stops, the container exits 0 well within 90 s, and after a start the job resumes from its checkpoint with no duplicate or missing documents.

| Test | Location | Verifies |
|---|---|---|
| `appliance_smoke.bats` › `AC3: stop mid-run then resume` | `deploy/appliance/test/appliance_smoke.bats` | Order of log lines, exit code, document count |
| `odb_appliance_test.sh` › `AC3: SIGTERM stops odb before couchbase` | `deploy/appliance/test/odb_appliance_test.sh` | Entrypoint with stub processes |

### AC4 — Failures are clear
**Given** a 2 GiB memory limit, **then** the container exits 78 with the memory message. **Given** Couchbase never becomes ready, **then** it exits non-zero after `ODB_CB_START_TIMEOUT` with the last Couchbase log lines. **Given** `odb` crashes, **then** Couchbase is stopped and the container exits non-zero.

| Test | Location | Verifies |
|---|---|---|
| `odb_appliance_test.sh` › `AC4: memory floor`, `AC4: couchbase timeout`, `AC4: odb crash stops container` | `deploy/appliance/test/odb_appliance_test.sh` | |

### AC5 — Secrets stay secret
**Given** a first start, **then** the secrets files are mode 0600 and owned by the `couchbase` user, no secret value appears in `docker logs` or `docker inspect` (secrets come from files, not env), and the app runs as a non-root user. **Given** `ODB_CB_ADMIN_PASSWORD_FILE` is mounted, **then** it is used instead of a generated one.

| Test | Location | Verifies |
|---|---|---|
| `appliance_smoke.bats` › `AC5: no secrets in logs or inspect` | `deploy/appliance/test/appliance_smoke.bats` | Secret scanner over logs and inspect output |
| `odb_appliance_test.sh` › `AC5: provided password wins` | `deploy/appliance/test/odb_appliance_test.sh` | |

### AC6 — Migrate into the bundled node
**Given** the appliance, **when** the user clicks **Use the bundled Couchbase** on the Target step, **then** the connection tests green, the `sandbox` bucket is browsable, and a migration from the sample PostgreSQL writes documents there. **Given** the regular image, **then** the button is not shown. **Given** a project with a bundled target, **when** exported, **then** the server answers 422 `BUNDLED_TARGET_NOT_EXPORTABLE`.

| Test | Location | Verifies |
|---|---|---|
| `TestTargetKinds_AC6_BundledOnlyWhenConfigured` | `internal/connections/handlers_test.go` | |
| `TestTargetConn_AC6_BundledPasswordFromFile` | `internal/connectors/target/couchbase/bundled_test.go` | Password never stored or returned |
| `TestExports_AC6_BundledTargetRefused` | `internal/export/export_test.go` | Once OC-050 exists |
| `TargetStep.test.tsx` › `AC6: bundled button` | `web/src/features/projects/steps/TargetStep.test.tsx` | |
| `appliance.spec.ts` › `AC6: migrate into bundled sandbox` | `web/tests/e2e/appliance.spec.ts` | Runs against the appliance image |

### AC7 — Works on Community Edition
**Given** `make test-integration-ce`, **then** the metadata-store, provisioning and pipeline integration suites pass against Couchbase Server CE, and any CE-specific fallback (e.g. durability) is covered by a test.

| Test | Location | Verifies |
|---|---|---|
| Integration suites with `OC_IT_CB_EDITION=community` | `backend/internal/...` (existing `_integration_test.go`) | CI job `integration-ce` |
| `TestWriter_AC7_DurabilityFallbackOnCE` | `internal/connectors/target/couchbase/writer_test.go` | Preflight warning, not failure |
