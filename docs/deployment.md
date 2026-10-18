# Deployment guide

How to build, ship and run Omni-Data-Bus: the binary, the container image, Docker Compose, Kubernetes, the all-in-one image, and the odb runner that runs exported migrations. For local development, see [development.md](development.md).

## Building and deploying

- **Single binary:** `make build` produces `backend/bin/odb` with the web app embedded. Run it with the production environment variables ([configuration reference](development.md#configuration-reference)) and it serves both UI and API on `:8080`.
- **Container image:** `make image` builds `deploy/docker/Dockerfile`: a distroless, non-root (uid 65532) image of about 38 MB holding `odb`, CA certificates, the release's odb checksums (OC-188) and the licence files (`/usr/share/doc/omni-data-bus/`, OC-189). `make image-test` checks it. Releases (`v*` tags) publish it multi-arch (amd64/arm64) to GHCR, signed with cosign, with an SPDX SBOM attached and a Trivy gate on HIGH/CRITICAL vulnerabilities.
- **Docker Compose (single VM):** `deploy/compose/prod.yml`, pointed at Capella, with an optional Caddy HTTPS proxy. See [deploy/compose/README.md](../deploy/compose/README.md) for the first run, upgrades, backup and key rotation.
- **Kubernetes:** the Helm chart in `deploy/helm/omni-data-bus` (separate api and worker Deployments, or one; migration hook; hardened pods; HPA/KEDA; NetworkPolicy; the odb cache and an optional odb dist volume for exports). See its [README](../deploy/helm/omni-data-bus/README.md); `make helm-test` lints and unit-tests it.
- **Secrets as files:** `OC_MASTER_KEY_FILE`, `OC_MASTER_KEYS_FILE`, `OC_META_PASSWORD_FILE`. `odb healthcheck` probes `/readyz` (for container healthchecks).
- **TLS:** put a TLS-terminating proxy in front and set `OC_PUBLIC_URL` to the public https origin.
- **Monitoring:** scrape `/metrics` (all `oc_*` metrics plus Go runtime and process metrics; `OC_METRICS_ADDR` keeps it off the public port). Import `deploy/observability/dashboard.json` into Grafana and load `deploy/observability/alerts.yml` into Prometheus. With tracing on, the request ID in an error (the problem `instance`) is the trace ID, and every log line carries `trace_id`.
- **Upgrading:** [1.0 → 2.0](upgrade/1.0-to-2.0.md), [2.0 → 2.5](upgrade/2.0-to-2.5.md) (exports and the odb runner).

## All-in-one image (app + Couchbase CE)

For handing the tool to a customer who has no Couchbase cluster: **one Docker image** holds Omni-Data-Bus and a single-node **Couchbase Server Community Edition** that stores the app's data and is offered as a migration target (*Use the bundled Couchbase* on the Target step). The customer loads one file and runs one command; everything lives in one Docker volume. Built from [`deploy/appliance/`](../deploy/appliance/).

| Use | When |
|---|---|
| All-in-one image | A customer (or a laptop) without Couchbase; evaluations; one machine, one team |
| Regular image + Capella / your cluster | Production with your own Couchbase; several API or worker nodes ([Docker Compose](../deploy/compose/README.md)) |
| Helm chart | Kubernetes ([chart README](../deploy/helm/omni-data-bus/README.md)) |

**1. Prerequisites.** Docker with buildx (Docker Desktop includes it). To build for the other CPU (an amd64 image on an Apple-silicon Mac, or the reverse), buildx needs QEMU: Docker Desktop has it; on Linux run `docker run --privileged --rm tonistiigi/binfmt --install all` once. The target machine needs 4 GB of memory for Docker.

**2. Build** for this machine, or for the customer's CPU (most servers are `amd64`):

<!-- docs-test -->
```bash
make appliance                 # this machine's CPU
make appliance ARCH=amd64      # Intel/AMD servers
```

It builds the regular image for that platform, then `omni-data-bus-appliance:<version>` on top of `couchbase/server:community-8.0.2` (pinned by digest) with the same `odb` binary.

**3. Try it:**

```bash
ODB_IMAGE=omni-data-bus-appliance:<version> deploy/appliance/run.sh start    # → http://localhost:8080
deploy/appliance/run.sh stop
```

**4. Package** it to share:

<!-- docs-test -->
```bash
make appliance-save ARCH=amd64    # or ARCH=arm64, or ARCH=all for both
```

`dist/appliance/` then holds everything the customer needs: `omni-data-bus-appliance-<version>-<arch>.tar.gz` (about 500 MB), its `.sha256`, `run.sh`, `compose.yml` and the customer guide `README.md`.

**5. Share** the `dist/appliance/` folder (a file share, a USB drive, or the assets of a GitHub release, which the release workflow attaches for every tag). For customers who can pull from a registry instead:

```bash
make appliance-push REGISTRY=registry.example.com/team    # both CPUs as one multi-arch image
```

**6. The customer** follows [`deploy/appliance/README.md`](../deploy/appliance/README.md): `./run.sh load`, `./run.sh start`, open http://localhost:8080. It also covers sign-in, stopping, upgrading, backups, troubleshooting and removal.

**7. Test** a build: `make appliance-test` (image structure, then a full start / sign-up / bundled target / stop / restart / crash / memory-floor smoke test, about 3 minutes); `make test-integration-ce` runs the Go integration suite against Couchbase CE.

**Licensing.** The image redistributes Couchbase Server Community Edition unmodified, under Couchbase's [Community Edition license](https://www.couchbase.com/community-license-agreement/) (included in the image at `/opt/couchbase/LICENSE.txt`), next to Omni-Data-Bus under Apache-2.0, so the image as a whole is labeled `Apache-2.0 AND LicenseRef-Couchbase-CE`. Before delivering it to a customer, confirm with Couchbase that this use is covered.

## The odb runner (exported migrations)

An export (OC-160, OC-182) is one folder with nothing OS-specific in it: `config.json` (**where**: the source and target connection details, passwords base64-encoded), `mappings.json` (**what**: the mappings, with a readable `outline` per mapping) and a `README.md` written for that export. The **`odb`** command runs it, the same pipeline as the server (documents are byte-identical), with no web app, metadata store or access to the tool, and the same commands on macOS, Windows and Linux:

```bash
vi config.json        # optional: another host, user or password (plain text is encoded on the next run)
odb preflight         # source, credentials, schema drift, target: writes nothing
odb run               # text progress on a terminal, JSON lines when piped; logs on stderr
odb resume | status | verify | inspect   # continue after Ctrl+C or a crash; summarize; count documents; what it migrates
```

**Without odb installed** (a machine without internet access), export with *Add odb for a machine without internet access*: the folder then holds `bin/odb` (`bin\odb.exe` on Windows) for the machine you chose, with its licences in `bin/LICENSES/`. Run that one instead:

```bash
bin/odb preflight          # macOS, Linux
bin/odb run
bin/odb status             # also resume | verify | inspect | help
```

```powershell
.\bin\odb.exe preflight    # Windows (PowerShell); in cmd: bin\odb.exe preflight
```

On macOS and Linux, if unzipping lost the executable bit, `chmod +x bin/odb` first. **On a Mac, an odb from a development build isn't signed**, and Gatekeeper refuses it ("Apple cannot check it for malware"). Clear the quarantine flag the browser download set, once:

```bash
xattr -d com.apple.quarantine bin/odb    # an installed odb: xattr -d com.apple.quarantine "$(command -v odb)"
```

A release build's macOS odb is signed and notarized and opens with no prompt (OC-185). On Windows, an unsigned development `odb.exe` may get a SmartScreen warning: choose *More info → Run anyway*.

odb finds `config.json` and `mappings.json` in the current directory, else beside odb (`--config`, `--mappings` or `ODB_CONFIG`, `ODB_MAPPINGS` name others). The state directory (`--state-dir` or `ODB_STATE_DIR`, default `odb-state` in the export folder) holds `checkpoint.json`, `errors.jsonl` and `report.json`. Exit codes: 0 completed, 2 completed with errors, 3 failed, 4 preflight or configuration error, 5 cancelled (`odb resume` continues). With Docker, the export README's `docker run … <image> preflight` runs the same commands (OC-183). 2.0 exports' `migrate.sh` still works. The full guide: [running migrations outside the tool](user-guide/running-outside-the-tool.md).

### Release binaries

`make dist` builds them into `dist/odb/`:
- `odb_<version>_<os>_<arch>.tar.gz` for linux and darwin, `.zip`s with `odb.exe` for windows, each with `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.md` beside the binary, and `SHA256SUMS`;
- with `lipo` on macOS, also one universal macOS binary (`darwin_universal`).

The release workflow signs and notarizes the macOS binary and Authenticode-signs `odb.exe` (OC-185, OC-186), writes `SIGNED`, signs `SHA256SUMS` with cosign, and attaches them all to the GitHub release. The server image carries that release's `SHA256SUMS` and `SIGNED`, so the server can add any machine's odb to an export: it downloads it from the release the first time, verifies it and caches it (OC-188). A server without internet access uses `OC_EXPORT_FETCH=false` and `OC_EXPORT_DIST_DIR` pointed at the release's odb files. `odb bundle validate | inspect | diff mappings.json` checks a mappings file. The other export settings are in `.env.example`.

## Security model

- **Credentials at rest:** envelope encryption: a random data key per secret (AES-256-GCM), wrapped by the master key; ciphertext is bound to its workspace/project/connection so it can't be copied between records. Supports key rotation.
- **Never echoed:** the API returns `passwordSet: true`, never the secret; logs redact passwords, tokens, keys and `user:password@` in URLs.
- **Sessions:** HttpOnly cookie, server-side session documents with expiry, CSRF token on every mutating request, login rate limiting, argon2id password hashing.
- **Tenant isolation:** every read checks the workspace; cross-workspace IDs return 404.
- **Least privilege:** use a read-only PostgreSQL role for sources; *Test connection* warns about superuser or write-capable roles.
- **Egress guard:** database hosts users type in are checked at dial time. Cloud metadata and link-local addresses are always refused, and private networks (loopback, RFC 1918, CGNAT, ULA) only when `OC_ALLOW_PRIVATE_NETWORKS=true` (the self-hosted default; set `false` for a shared service). PostgreSQL connections resolve once and dial only the checked IPs, which defeats DNS rebinding. The Couchbase SDK can't take a custom dialer, so its bootstrap hosts are checked before connecting and every node the cluster reports is checked afterwards. Pair it with a NetworkPolicy or egress firewall (the Helm chart has one).
- **Headers:** a strict Content-Security-Policy (`script-src 'self'`, no inline scripts), HSTS when `OC_PUBLIC_URL` is https, `nosniff`, `frame-ancestors 'none'`, `Permissions-Policy`, COOP.
- **Limits:** 1 MB request bodies (6 MB for JSON samples and connection settings); 20 requests/s per session (burst 40) and 5/s per IP when signed out; at most 2 discoveries and 4 previews at once per workspace, 3 connection tests per 10 s and 10 live streams per session.
- **Audit log:** sign-ins, projects, repositories, keyspaces, mappings (updates at most once per 10 minutes each), migrations, exports and report downloads, kept `OC_AUDIT_RETENTION_DAYS` (365). Owners see it in *Settings → Audit log*.
- **Scanning:** govulncheck, npm audit, gosec (golangci-lint), CodeQL, Trivy and a full-history gitleaks secret scan run in CI.
