# Performance benchmark (OC-034)

`odbbench` measures Omni-Data-Bus against the release gates in
[architecture §9](../docs/release-1.0.0/00-architecture.md#9-non-functional-requirements-release-gate).
It is a small Go module with no dependencies outside the standard library.

A run does the following:

1. It generates a dataset in the source PostgreSQL with `generate_series`. Every value is derived from the row number, so each run of a dataset reads identical rows. An existing table with the right row count is reused.
2. It signs in (or signs up) through the API, creates a project, saves and tests both repositories, and discovers the schema.
3. It maps the table into a new `bench_<time>` scope and starts a migration job.
4. Every second while the job runs, it samples the job's progress and the worker's `process_resident_memory_bytes`.
5. It follows the job's SSE stream to measure progress latency.
6. When `--pprof` is set, it captures a CPU profile and a heap profile at 50% progress.
7. It reads the job report and verification, evaluates the gates, and writes `results/<time>-<commit>-<dataset>.json` and `.md`.

The exit code is 0 when everything passed, 1 when a gate or the regression check failed, and 2 when the harness itself failed.

## Gates

| Gate | Threshold | How it's measured |
|---|---|---|
| Sustained throughput (`standard` dataset) | ≥ 20,000 docs/s | Median of the 1 s intervals whose both ends lie between 10% and 90% progress, computed from the written counts |
| Worker memory | RSS < 512 MB | Peak `process_resident_memory_bytes` from the worker's `/metrics`. This metric is Linux-only, so run the worker in a container or on Linux. |
| Progress latency | p95 ≤ 2 s | Time from a progress event's timestamp (set by the worker) to its arrival over SSE (what the UI sees). Keep the clocks in sync with NTP. |
| Zero data loss | counts equal, verification passes | Documents written = source rows, and the job's verification (OC-030) is `passed` or `passed_with_warnings` |

`--gate release` checks all four gates. `--gate integrity` checks only the data-loss and verification gates, which is what the nightly smoke run uses. `--gate none` just measures. You can change individual thresholds with `--min-docs-per-sec`, `--max-rss-mb` and `--max-latency`.

## Datasets

| Dataset | Rows | Shape | Use |
|---|---|---|---|
| `narrow` | 10M | 8 columns, ~200 B docs | Per-document overhead |
| `standard` | 5M | 20 columns, ~1 KB docs | **The gating dataset** |
| `wide` | 1M | 120 columns, ~6 KB docs | Large documents |
| `embed` | 1M customers + 10M orders | Orders embedded as an array (10 per customer) | Embed lookups (OC-021) |
| `smoke` | 500k | Same shape as `standard` | Nightly CI regression run |

`--rows N` scales any dataset for a quick local try. For `embed`, it scales the number of customers and keeps 10 orders each.

## Running it

### Locally, against the e2e stack

```bash
make dev-stack                                  # Couchbase + sample PostgreSQL
make image IMAGE=omni-data-bus:e2e
docker compose -f deploy/compose/e2e.yml up -d --wait   # api :18081, worker metrics :19092, pprof :16060
make bench DATASET=smoke GATE=release BENCH_ARGS="--pprof http://localhost:16060"
```

The defaults point at this stack. The app reaches the source as `source-pg` and the target as `couchbase://couchbase`, while the harness runs `psql` through `docker exec`. Results go to `bench/results/`.

A local run is indicative only. On a 10-CPU laptop with Docker Desktop, `smoke` measured 89,945 docs/s sustained, 224 MiB peak RSS and 965 ms p95 latency, with verification passing. Only the reference environment counts for the release.

### Nightly (CI)

`.github/workflows/nightly.yml` runs `make bench-smoke`: 500k rows with the integrity gates and relative regression detection. The run fails when sustained throughput is more than 20% below the median of the last 7 nightly runs. Those earlier runs are kept in the Actions cache (`bench/history/`), and each run's summary is added to the job summary.

### Release (AC10): the reference environment

| Role | Size | Terraform (`infra/`) |
|---|---|---|
| Worker (api + worker, one process) | 8 vCPU / 16 GB | `c6i.2xlarge` |
| Source | PostgreSQL 16, 8 vCPU | `m6i.2xlarge`, gp3 6000 IOPS |
| Couchbase | 3 × 8 vCPU / 32 GB, 8.0.3 (or 7.6) | `m6i.2xlarge` × 3 |
| Harness | Runs odbbench off the worker | `c6i.large` |

All nodes run in one AZ, in a cluster placement group. The target bucket has enough RAM to hold the dataset (`sandbox_ram_mb`).

```bash
cd bench/infra
terraform init
terraform apply -var key_name=<your-key> -var allowed_cidr=<your-ip>/32 -var image=ghcr.io/sahnianuj/omni-data-bus:<rc>
terraform output -raw odbbench_command        # the exact command, with the generated passwords
scp -r ../../bench ubuntu@<harness>:           # then on the harness host:
cd bench && <odbbench_command>                 # exit code 0 = every gate passed
scp -r ubuntu@<harness>:bench/results/* ../results/   # commit results/<run>.{json,md}, then link them in the release notes
terraform destroy
```

The node bootstrap scripts are in `infra/scripts/`. Wait for `http://<worker>:8080/readyz` before you start; the Couchbase cluster and its buckets take a few minutes to come up.

### Release checklist item

- [ ] `odbbench --dataset standard --gate release` exits 0 in the reference environment.
- [ ] `results/<run>.json` and `.md` are committed and linked from the release notes.

## 2.0 scenarios (OC-056)

The 2.0 gates ([architecture §7](../docs/release-2.0.0/00-architecture.md#7-additional-non-functional-requirements-release-gate-validated-by-oc-056)) are in [gates-2.0.0.json](gates-2.0.0.json). Each 2.0 scenario runs with `--gate integrity`, which checks for no loss and a passing verification. `odbbench gate` then judges the results against the file.

| Scenario | Dataset | Shape | Gates |
|---|---|---|---|
| `directory` | `--dataset directory --directory /data/odb-out` | 2M flat rows, about 1 KB documents, written to a directory target with `durability: file` | G1 ≥ 15,000 files/s, G4a, G7a |
| `nested` | `--dataset nested` | 500k orders, each embedding 10 items, each embedding its product (3 tables, 2 levels), written to Couchbase | G2 ≥ 8,000 docs/s, G4b, G7b |
| `skewed-<strategy>` | `--dataset skewed --strategy batched\|sorted-merge` | 500k customers with all their orders: 5 each, or 500 for 1% of them | G4c, G7c |
| `fanout-<strategy>` | `--dataset fanout --strategy batched\|sorted-merge` | 100k customers × 500 orders, 50M rows (OC-046 AC5) | G3 sorted-merge ≥ 1.5× batched, G4d, G7d |
| `coldstart` | `odbbench coldstart --dir <export> --odb <odb>` | Median of 5 `odb preflight` runs in the export folder, after one untimed run (2.0: `--script <export>/migrate.sh`) | G5 < 5 s |
| `standard` | `--dataset standard` (1.0) | OC-034's gating dataset | G6 within 5% of `results/1.0.0/standard.json` |

The memory gates (G4) need the worker's RSS, so the worker must run on Linux. `--directory` is the path as the worker sees it; each run writes to a new `bench_<time>` folder under it.

```bash
cd bench
A="--gate integrity --out results/2.0.0 --env reference=true"
go run ./cmd/odbbench --dataset directory --directory /data/odb-out $A
go run ./cmd/odbbench --dataset nested $A
for s in batched sorted-merge; do go run ./cmd/odbbench --dataset skewed --strategy $s $A; done
for s in batched sorted-merge; do go run ./cmd/odbbench --dataset fanout --strategy $s $A; done
go run ./cmd/odbbench --dataset standard --gate release --out results/2.0.0 --env reference=true
# Cold start: export any project, unzip it, then time odb preflight in its folder:
ODB_BUNDLE_PASSPHRASE=... go run ./cmd/odbbench coldstart --dir <export> --odb <path to odb> --out results/2.5.0 --env reference=true
make -C .. bench-gate                          # exit 0 = every gate passed
```

To get a cold-start fixture against the dev stack without the UI, run `cd backend && ODB_COLDSTART_OUT=<dir> OC_IT_PG_HOST=localhost OC_IT_PG_PORT=5434 go test -tags=integration -run TestColdStartFixture ./internal/headless/`. It writes `mappings.json` and an empty `config.json` (the sealed bundle keeps its connections) for the golden orders project; build the matching `odb` with `go build -o <dir>/bin/odb ./cmd/odb` and pass `--dir <dir> --odb <dir>/bin/odb`.

`make bench-gate` judges only runs marked `--env reference=true` (`--reference-only`).
- CI's `bench-gate` job reports gates that have no reference result yet as pending.
- The release workflow fails on them: a release that has a `bench/gates-<version>.json` ships only when every gate has passed.
- Local runs are indicative. Keep them under `results/2.0.0/local/` ([what they showed](results/2.0.0/local/README.md)).

## Profiles

When `--pprof` points at the worker's `OC_PPROF_ADDR`, the harness saves `<run>-cpu.pprof` (30 s by default; see `--profile-seconds`) and `<run>-heap.pprof` next to the results. The profiles aren't committed. Open them with `go tool pprof -http=: <file>`. Never publish `OC_PPROF_ADDR` beyond the benchmark network.
