# OC-032 — Packaging & deployment

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E6 Operate & Harden | P0 | 5 | OC-001, OC-002 | Implemented |

## User story
As an **operator**, I want Omni-Data-Bus shipped as a small, secure container image with a production docker-compose file and a Helm chart, so that I can deploy it on a single VM or scale it on Kubernetes.

## Implementation details

### Container image (`deploy/docker/Dockerfile`)
- **Multi-stage build:**
  1. `node:<lts>-alpine` runs `pnpm install --frozen-lockfile && pnpm build`, producing `web/dist`.
  2. `golang:<ver>` builds with `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION -X main.commit=$SHA"`, with the SPA embedded via `embed.FS`.
  3. `gcr.io/distroless/static-debian12:nonroot`, containing only the binary and CA certs, running as uid 65532.
- `HEALTHCHECK` isn't used with distroless; probes go in compose and k8s. The image exposes 8080 (app) and 9090 (metrics).
- Size target: < 40 MB.
- Multi-arch (`linux/amd64`, `linux/arm64`) via `docker buildx`.
- Signed with cosign, with an SBOM (syft) attached, and a Trivy scan in CI that fails on HIGH or CRITICAL CVEs.
- Version info: `odb --version` and `GET /api/v1/version`.

### docker-compose (production) `deploy/compose/prod.yml`
- Services: `omni-data-bus` (role all, 2 replicas possible), pointed at **Couchbase Capella** for its metadata (`OC_META_*`). There's no database container in production. There is an optional `caddy` reverse proxy with automatic TLS.
- Secrets via env file or `secrets:` (`OC_MASTER_KEY`, `OC_META_PASSWORD`).
- `deploy/compose/README.md` covers first run, key generation (`odb admin gen-key`), upgrade, backup and restore, and key rotation (OC-006).

### Helm chart `deploy/helm/omni-data-bus`
- Separate Deployments for `api` (HPA on CPU/RPS) and `worker` (HPA on the custom metric `oc_worker_active_jobs / oc_worker_capacity`, or KEDA optional). Or a single `all` deployment for small installs (`values.mode: split | all`).
- A `Service` and an `Ingress` with SSE-friendly annotations (e.g., `nginx.ingress.kubernetes.io/proxy-buffering: "off"`, and a read timeout of 3600 s).
- Secret references for the master key and DB URL (existingSecret support).
- Hardened `securityContext` (runAsNonRoot, readOnlyRootFilesystem, drop ALL capabilities, seccomp RuntimeDefault).
- `PodDisruptionBudget`, liveness `/healthz`, readiness `/readyz`, and worker `terminationGracePeriodSeconds: 45` (> the 30 s drain).
- Migrations run as a `Job` pre-upgrade hook (`odb admin migrate`) with `OC_DB_AUTO_MIGRATE=false` for the pods.
- A `NetworkPolicy` template (egress to the Capella metadata cluster plus configurable CIDRs for source and target databases).

### Release pipeline (`.github/workflows/release.yml`)
On a tag `v*`: build, test, image build and push (GHCR), sign, SBOM, Helm package and push (OCI), and a GitHub release with a changelog from Conventional Commits.

## Acceptance criteria & unit tests

### AC1 — Image builds small, non-root, and embeds the SPA
**Given** `docker build`, **then** the image is < 40 MB, runs as a non-root user, `odb --version` prints the version and commit, and `GET /` serves the SPA.

| Test | Location | Verifies |
|---|---|---|
| `TestVersion_AC1_LdflagsInjected` | `cmd/odb/main_test.go` | Default `dev` when unset; `--version` output format |
| `TestEmbeddedSPA_AC1_IndexPresent` | `internal/httpapi/spa_embed_test.go` | Build tag `embedspa`: the embedded FS contains `index.html` |
| CI `image-check` job: `container-structure-test` with `deploy/docker/structure-test.yaml` | `.github/workflows/ci.yml` | User, entrypoint, and size assertions |

### AC2 — Admin CLI
**Given** `odb admin gen-key`, **then** it prints a base64 32-byte key. **Given** `admin migrate`, **then** migrations run and exit 0. **Given** `admin rotate-secrets`, **then** OC-006 rotation runs.

| Test | Location | Verifies |
|---|---|---|
| `TestAdminGenKey_AC2_Length` | `cmd/odb/admin_test.go` | |
| `TestAdminCommands_AC2_Dispatch` | same | Subcommand routing, help text |

### AC3 — Compose production stack
**Given** `prod.yml` with the env file, **then** the stack starts healthy, and the app refuses to start without `OC_MASTER_KEY`.

| Test | Location | Verifies |
|---|---|---|
| `compose.test.ts` › `AC3: prod services have healthchecks, restart policy, no default secrets` | `web/scripts/compose.test.ts` | Parses the YAML |
| CI `compose-smoke` job | `.github/workflows/ci.yml` | `docker compose up --wait` and `curl /readyz` |

### AC4 — Helm chart correctness
**Given** default values and `mode=split`, **then** `helm template` renders api and worker Deployments with the hardened securityContext, probes, PDB, SSE ingress annotations, a migration hook Job, and a grace period ≥ 45 s. `helm lint` passes.

| Test | Location | Verifies |
|---|---|---|
| `helm unittest` suite › `AC4: split mode renders two deployments` | `deploy/helm/omni-data-bus/tests/deployment_test.yaml` | helm-unittest plugin |
| `helm unittest` › `AC4: securityContext hardened` | same | |
| `helm unittest` › `AC4: ingress SSE annotations` | `tests/ingress_test.yaml` | |
| `helm unittest` › `AC4: migration hook job` | `tests/hooks_test.yaml` | |

### AC5 — Supply-chain gates
**Given** the release pipeline, **then** the image is signed, an SBOM is attached, and a HIGH or CRITICAL vulnerability fails the pipeline.

| Test | Location | Verifies |
|---|---|---|
| Release workflow steps `cosign sign`, `syft`, `trivy --exit-code 1 --severity HIGH,CRITICAL` | `.github/workflows/release.yml` | Verified by `actionlint` in CI plus a dry run on PRs labeled `release-check` |

## Implementation notes
- The web stage uses `npm ci` (the repo uses npm, not pnpm). Build stages run on `$BUILDPLATFORM` and cross-compile, so multi-arch builds don't emulate the Go and Node builds. Image: 38.3 MB, uid 65532, `ENTRYPOINT odb`, `OC_HTTP_ADDR=:8080`, `OC_METRICS_ADDR=:9090`.
- Metadata "migrations" are Couchbase provisioning: `odb admin migrate` is an alias of `admin provision`, and the pods run with `OC_META_AUTO_PROVISION=false` when the hook is on (`migrations.enabled`). There is no `OC_DB_AUTO_MIGRATE`.
- Secrets are mounted as files: new `OC_MASTER_KEY_FILE`, `OC_MASTER_KEYS_FILE`, `OC_META_PASSWORD_FILE` (setting both a variable and its `_FILE` is an error). `existingSecret` keys: `master-key`, `meta-password`.
- Distroless has no shell, so the compose healthcheck is a new `odb healthcheck` subcommand (probes `/readyz` on `OC_HTTP_ADDR`). Unknown top-level commands now exit 2 instead of starting the server.
- Helm pre-install hooks run before release resources exist, so a chart-managed secret gets a hook-only copy (weight −10, deleted on success), and the migrate Job uses the namespace's default service account (with no token).
- Worker autoscaling: HPA on the per-pod metric `oc_worker_active_jobs` (via prometheus-adapter; target `maxJobs × 0.8`), or KEDA on `(active + queued) / capacity`. Off by default.
- Verified beyond the tests: the image smoke-run against Couchbase (SPA, `/readyz`, `/metrics` on :9090 only); `prod.yml` healthy with a read-only root filesystem and dropped capabilities; and the chart installed and upgraded on a kind cluster (Kubernetes 1.33). There, the migrate hook ran, api and worker became Ready, and `/readyz` answered through the Service. kubeconform validates the rendered manifests for Kubernetes 1.30.
- The release workflow publishes to `ghcr.io/<owner>/omni-data-bus` and the chart to `oci://ghcr.io/<owner>/charts`; the changelog comes from git-cliff's Conventional Commits defaults. It can't run until the repository has commits and a remote build.
