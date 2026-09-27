# omni-data-bus Helm chart

Deploys Omni-Data-Bus on Kubernetes (≥ 1.25). Its metadata lives in **Couchbase Capella**
(`metadata.*`), so the chart has no database.

```sh
kubectl create secret generic odb-secrets \
  --from-literal=master-key="$(docker run --rm ghcr.io/sahnianuj/omni-data-bus:1.0.0 admin gen-key)" \
  --from-literal=meta-password='<capella database password>'

helm install odb oci://ghcr.io/sahnianuj/charts/omni-data-bus --version 1.0.0 \
  --set publicUrl=https://odb.example.com \
  --set metadata.connStr=couchbases://cb.xxxx.cloud.couchbase.com \
  --set metadata.username=omnidb \
  --set secrets.existingSecret=odb-secrets \
  --set ingress.enabled=true --set ingress.host=odb.example.com --set ingress.tlsSecretName=odb-tls
```

Back up the master key: without it, stored database passwords can't be decrypted.

## What it deploys

| Resource | Notes |
|---|---|
| Deployments `…-api`, `…-worker` (`mode: split`, default) or `…-all` (`mode: all`) | Non-root (uid 65532), read-only root filesystem, all capabilities dropped, seccomp `RuntimeDefault`, no service account token. Probes: liveness `/healthz`, readiness `/readyz`. Workers get ≥ 45 s to drain running jobs (they drain for 30 s, then checkpoint and requeue) |
| Job `…-migrate` (pre-install / pre-upgrade hook) | `odb admin migrate` creates or upgrades the metadata collections and indexes; pods then run with `OC_META_AUTO_PROVISION=false`. Turn off with `migrations.enabled=false` |
| Service `…` (http) and headless `…-metrics` | Every pod serves Prometheus metrics on `:9090` (also annotated for `prometheus.io/scrape`) |
| Ingress (optional) | SSE-friendly nginx annotations: no proxy buffering, 3600 s read/send timeouts |
| HPA `…-api` | CPU based |
| HPA or KEDA `ScaledObject` `…-worker` (optional) | See below |
| PodDisruptionBudgets | api `minAvailable: 1`; worker/all `maxUnavailable: 1` |
| NetworkPolicy (optional) | Egress only to DNS, the metadata cluster, and the source/target CIDRs you list |
| Secret (unless `secrets.existingSecret`) | Keys `master-key`, `meta-password`, mounted as files (`OC_*_FILE`), never as env values |

## Scaling workers

Each worker pod runs up to `worker.maxJobs` jobs. Utilization is
`oc_worker_active_jobs / oc_worker_capacity`.

- **`worker.autoscaling.type: keda`** (simplest): a KEDA `ScaledObject` with a Prometheus trigger on
  `(active jobs + queued jobs) / capacity`, targeting `targetUtilization` (0.8).
- **`worker.autoscaling.type: hpa`**: an HPA on the per-pod metric `oc_worker_active_jobs`, which
  needs [prometheus-adapter](https://github.com/kubernetes-sigs/prometheus-adapter) with a rule such as:

  ```yaml
  rules:
    - seriesQuery: 'oc_worker_active_jobs{namespace!="",pod!=""}'
      resources: { overrides: { namespace: { resource: namespace }, pod: { resource: pod } } }
      metricsQuery: 'max by (<<.GroupBy>>) (<<.Series>>{<<.LabelMatchers>>})'
  ```

  Set `targetAverageActiveJobs` to `maxJobs × 0.8` (default `1600m` for `maxJobs: 2`).

Scale-down is slow on purpose (one pod per 5 minutes, 10-minute window): a removed worker
drains, checkpoints, and requeues its jobs for another worker.

## Useful values

| Value | Default | |
|---|---|---|
| `mode` | `split` | `split` or `all` |
| `publicUrl` | — | Public https origin (CSRF check); `OC_SESSION_COOKIE_SECURE` follows its scheme |
| `metadata.connStr`, `metadata.username` | — | Required |
| `secrets.existingSecret` | — | Recommended; otherwise set `secrets.masterKey` and `secrets.metaPassword` |
| `config.otel.endpoint` | — | OTLP/HTTP collector for traces |
| `config.extraEnv` | `{}` | Any other `OC_*` setting |
| `config.egress.allowPrivateNetworks` | `true` | The server's own egress guard (`OC_ALLOW_PRIVATE_NETWORKS`); plus `allowCidrs`, `denyCidrs` |
| `config.auditRetentionDays` | `365` | How long audit events are kept |
| `networkPolicy.enabled` | `false` | Plus `sourceCidrs`, `targetCidrs`, `metadataCidrs` |

## Testing the chart

```sh
make helm-test     # helm lint --strict + helm-unittest suites in tests/ (via Docker)
```
