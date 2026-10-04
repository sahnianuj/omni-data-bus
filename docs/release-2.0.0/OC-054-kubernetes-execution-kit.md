# OC-054 — Kubernetes execution kit

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 5 | OC-053 | Descoped |

> **Descoped (2026-10-01):** product decision, this kit won't be built for 2.0. The exported `migrate.sh` (OC-053) and the all-in-one image (OC-072 – OC-074) cover running migrations outside the tool. Kept for reference; dependent stories no longer rely on it.

## User story
As a **platform engineer**, I want the exported migration to run as a Kubernetes Job that writes JSON documents to a persistent volume (or loads a database), restarts safely from its checkpoint, and reports progress as JSON in its logs, so that we can run production migrations inside our cluster, close to the data.

## Scope
**In:**
- `migrate.sh k8s …` commands, plus plain manifests in the export zip (`k8s/`) for GitOps.
- Manifests: ConfigMap (bundle), Secret (passphrase or credentials, OC-052), PVC (state and, for directory targets, output), Job, optional NetworkPolicy.
- Restarts resume from the checkpoint on the PVC. Progress is available through `kubectl logs`.
- Fetching the output from the PVC to a local machine.

**Out:** a Helm chart or operator for runs (these plain manifests are enough and simpler to review), and CronJob scheduling (documented as a pattern only).

## Implementation details

### Generated manifests (`internal/export/templates/k8s/*.yaml.tmpl`)
| Object | Notes |
|---|---|
| `ConfigMap odb-<run>-bundle` | `bundle.odb.json`. Bundles > 900 KiB (ConfigMap limit 1 MiB) go into a Secret instead, and the script says so |
| `Secret odb-<run>-credentials` | `ODB_BUNDLE_PASSPHRASE` (sealed), or credential values or files (plaintext or external). Generated as a **separate file**, `k8s/secret.yaml`, so it can be left out of git |
| `PersistentVolumeClaim odb-<run>-data` | `storageClassName` and size from flags. Default size = the preflight output estimate × 1.3 + 1 GiB. Mounted at `/state` and `/out` (sub-paths) |
| `Job odb-<run>` | Image pinned by digest. `args: [run, --bundle, /bundle/bundle.odb.json, --state-dir, /state, --target-path, /out]`. `backoffLimit: 6`, `restartPolicy: OnFailure`, `activeDeadlineSeconds` optional, resources (defaults 2 CPU / 1 GiB request, 4 CPU / 2 GiB limit), `terminationGracePeriodSeconds: 45`, hardened securityContext (non-root 65532, read-only root FS, drop ALL, seccomp RuntimeDefault), `ttlSecondsAfterFinished: 604800` |
| `NetworkPolicy` (optional) | Egress only to the source (and target DB) CIDRs and ports given by flags, plus DNS |

- Labels: `app.kubernetes.io/name=odb-run`, `odb.io/run=<run>`, `odb.io/bundle-sha=<first 12>`.
- Exit codes map to Job behavior: 0 and 2 complete; 3 and 4 are made non-retriable through `podFailurePolicy` (`action: FailJob` on exit codes 3 and 4), so a config error doesn't loop; other failures are retried and resume.

### `migrate.sh k8s` commands
| Command | Does |
|---|---|
| `k8s render [--namespace ns] [--storage-class sc] [--size 50Gi] [--run name]` | Writes the manifests to `./k8s/` (no cluster access needed) |
| `k8s apply …` | Render, then `kubectl apply` (Secret first), then print how to follow the run |
| `k8s logs [-f]` | `kubectl logs job/odb-<run>`, piped through a small `jq`-free formatter unless `--json` |
| `k8s status` | Job and pod state plus the last progress line |
| `k8s fetch <local-dir>` | Starts a short-lived `busybox` pod mounting the PVC read-only, then `kubectl cp` of `/out` (the distroless image has no `tar`), then deletes the helper pod |
| `k8s delete [--keep-data]` | Deletes the Job, ConfigMap and Secret, and the PVC unless `--keep-data` |

- Requires `kubectl` ≥ 1.27 (for `podFailurePolicy`). The current context is printed and confirmed before `apply` and `delete` unless `--yes`.

## Acceptance criteria & unit tests

### AC1 — Manifests are valid and hardened
**Given** every render variant, **then** `kubeconform -strict` passes against Kubernetes 1.27–1.31, and the Job has the hardened securityContext and a digest-pinned image.

| Test | Location | Verifies |
|---|---|---|
| `TestK8sTemplates_AC1_Kubeconform` | `internal/export/k8s_test.go` | |
| `TestK8sTemplates_AC1_SecurityContext` | same | |

### AC2 — Run in a cluster
**Given** a kind cluster with Postgres, **when** `./migrate.sh k8s apply` runs, **then** the Job completes, `k8s logs` shows JSON progress, and `k8s fetch` copies one JSON file per order locally.

| Test | Location | Verifies |
|---|---|---|
| `k8s_run.bats` › `AC2: apply, logs, fetch` *(kind)* | `backend/internal/export/testdata/bats/k8s_run.bats` | CI job `e2e-kind` |

### AC3 — Pod kill resumes
**Given** the pod is deleted at ~50%, **then** the Job starts a new pod that resumes from `/state/checkpoint.json`, and the output is complete with no duplicates.

| Test | Location | Verifies |
|---|---|---|
| `k8s_run.bats` › `AC3: pod kill resumes from PVC` *(kind)* | `backend/internal/export/testdata/bats/k8s_run.bats` | |

### AC4 — Non-retriable failures stop
**Given** a wrong passphrase (exit 4), **then** the Job fails after one pod, with no retries.

| Test | Location | Verifies |
|---|---|---|
| `k8s_run.bats` › `AC4: exit 4 fails job immediately` *(kind)* | `backend/internal/export/testdata/bats/k8s_run.bats` | |

### AC5 — Large bundles & separate secret
**Given** a 1.2 MiB bundle, **then** it is rendered into a Secret and mounted the same way. **Given** any render, **then** credentials appear only in `k8s/secret.yaml`.

| Test | Location | Verifies |
|---|---|---|
| `TestK8sTemplates_AC5_LargeBundleAsSecret` | `internal/export/k8s_test.go` | |
| `TestK8sTemplates_AC5_SecretsIsolated` | same | Secret scanner over other files |
