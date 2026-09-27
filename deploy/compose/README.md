# Running Omni-Data-Bus with Docker Compose

`prod.yml` runs Omni-Data-Bus on a single VM. Its own data (users, projects, encrypted
credentials, jobs) lives in a **Couchbase Capella** bucket, so the stack has no database
container. An optional Caddy reverse proxy adds HTTPS with automatic certificates.

| File | Purpose |
|---|---|
| `prod.yml` | The production stack: `omni-data-bus` (API + worker) and, with `--profile tls`, `caddy` |
| `prod.env.example` | Settings template; copy to `.env.prod` (git-ignored) |
| `Caddyfile` | HTTPS proxy config (SSE-safe: no buffering, 1 h read timeout) |
| `dev.yml` | Local development stack (Couchbase Server 8.0.3 + sample PostgreSQL); not for production |
| `ci.yml`, `ci.env`, `ci.app.env` | The CI smoke test: `prod.yml` against the dev stack's Couchbase |

## First run

1. **Prepare Capella.** Create the bucket named in `OC_META_BUCKET` (default `omniconnect`), a
   database credential with Read/Write on it, and allow this VM's public IP under
   *Settings → Networking → Allowed IP addresses*.
2. **Settings.** `cp prod.env.example .env.prod`, then set `ODB_IMAGE` (pin a version),
   `OC_PUBLIC_URL`, `ODB_DOMAIN` and the `OC_META_*` values.
3. **Secrets.** They are mounted as files and never appear in the environment:

   ```sh
   mkdir -p secrets
   docker run --rm "$ODB_IMAGE" admin gen-key > secrets/master_key
   printf '%s' '<capella database password>' > secrets/meta_password
   chmod 600 secrets/*
   ```

   **Back up `secrets/master_key` somewhere safe now.** It encrypts every stored database
   password; without it they can't be decrypted (users would have to enter them again).
4. **Start.**

   ```sh
   docker compose -f prod.yml --env-file .env.prod up -d --wait                 # app on 127.0.0.1:8080
   docker compose -f prod.yml --env-file .env.prod --profile tls up -d --wait   # + HTTPS on :443
   ```

   On first start the app creates scope `OC_META_SCOPE`, its collections and indexes. If the
   Capella credential isn't allowed to, list what to create by hand with
   `docker compose -f prod.yml --env-file .env.prod run --rm omni-data-bus admin provision --print`.
5. **Check.** `docker compose -f prod.yml ps` shows `healthy`; the container's healthcheck is
   `odb healthcheck`, which probes `/readyz` (metadata store reachable).

The app refuses to start without a master key or the metadata credentials, and names the
missing variable in its single startup log line.

## Operating

- **Logs:** `docker compose -f prod.yml logs -f omni-data-bus` (JSON, one line per request,
  with `request_id` and, when tracing is on, `trace_id`).
- **Metrics:** Prometheus text on `127.0.0.1:9090/metrics` (`ODB_METRICS_BIND`/`ODB_METRICS_PORT`
  to change). Import `deploy/observability/dashboard.json` into Grafana and load
  `deploy/observability/alerts.yml` into Prometheus.
- **Scaling:** one container runs the API and a worker (`OC_WORKER_MAX_JOBS` jobs at a time).
  For more workers, run a second VM with the same `.env.prod` and secrets; jobs are claimed
  through the shared metadata store, so they coordinate on their own.

## Upgrading

```sh
# 1. Pick the new version in .env.prod (ODB_IMAGE=...:1.1.0), then:
docker compose -f prod.yml --env-file .env.prod pull
docker compose -f prod.yml --env-file .env.prod up -d --wait
```

Running jobs drain for up to 30 s on shutdown (the container gets 45 s), checkpoint, and go
back to *queued*; the new container resumes them. Metadata schema changes are applied on
start (`OC_META_AUTO_PROVISION=true`); with provisioning turned off, run
`... run --rm omni-data-bus admin migrate` before `up`.

## Backup and restore

Everything durable is in the Capella bucket plus the master key:

- **Bucket:** schedule Capella backups for the metadata bucket (Capella → cluster → Backups).
  Restoring the bucket (or just its `OC_META_SCOPE` scope) restores users, projects,
  mappings, job history and encrypted credentials.
- **Master key:** keep `secrets/master_key` (and any older keys still in `OC_MASTER_KEYS`) in a
  password manager or secret store. A restored bucket is only useful with the key that
  encrypted it.
- Migrated data lives in your own target clusters and isn't part of this backup.

## Rotating the master key

1. Generate a new key: `docker run --rm "$ODB_IMAGE" admin gen-key`.
2. Replace the `master_key` secret with a key ring. Put `k2:<new>,k1:<old>` in
   `secrets/master_keys`, and in `prod.yml` set `OC_MASTER_KEYS_FILE: /run/secrets/master_keys`
   and `OC_MASTER_KEY_ACTIVE: k2` instead of `OC_MASTER_KEY_FILE` (add the `master_keys` secret
   next to `master_key`).
3. Restart (`up -d --wait`), then re-wrap every stored credential with the new key:
   `docker compose -f prod.yml --env-file .env.prod exec omni-data-bus odb admin rotate-secrets`.
4. When it reports no failures, you can drop `k1` from the key ring and restart again.

## Smoke test (CI)

```sh
make dev-stack
docker build -f deploy/docker/Dockerfile -t omni-data-bus:ci .
mkdir -p deploy/compose/secrets-ci
docker run --rm omni-data-bus:ci admin gen-key > deploy/compose/secrets-ci/master_key
printf password > deploy/compose/secrets-ci/meta_password
docker compose -f deploy/compose/prod.yml -f deploy/compose/ci.yml --env-file deploy/compose/ci.env up -d --wait
curl -fs localhost:18080/readyz
```
