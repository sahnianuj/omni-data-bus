# 2.0.0 — local (indicative) runs

These runs were made on 2026-10-02 on a 10-CPU laptop. The api and worker ran as containers of the 2.0 image in Docker Desktop (`deploy/compose/e2e.yml`), with the dev stack's PostgreSQL 16 and Couchbase 8.0.3. They aren't release evidence: `make bench-gate` ignores them, and judges only runs in the reference environment (`--env reference=true`) committed to `results/2.0.0/`.

The datasets were scaled down to fit the laptop's Docker disk: directory 500k, nested 200k, skewed 400k, fan-out 8k customers (4M orders). The commands are in [../../../README.md](../../../README.md#20-scenarios-oc-056).

What these runs say:

- **G1 directory throughput** reached 12,981 files/s, below 15,000. The gate is defined for local NVMe; this ran on Docker Desktop's virtual disk with `durability: file`, which makes one fsync per file.
- **G3 sorted-merge vs batched** came out at 1.00×. With 500 orders per customer, both strategies wrote about 1,500 docs/s, and sorted-merge didn't beat batched. On a local, idle PostgreSQL, batched lookups by an indexed key are cheap. Whether sorted-merge reaches 1.5× where the source is remote is the open question for the reference run. If it doesn't, the gate or the `auto` thresholds (OC-046) need revisiting before 2.0 ships.
- **Memory:** every scenario stayed under 768 MB after two fixes made during this work:
  - The directory target's manifest merge, integrity check, count and Output page are now bounded. Before, 1M files took the worker to 989 MiB.
  - The process now sets a Go soft memory limit, `OC_MEMORY_LIMIT_MB`, default 640. Before, fan-out documents of 75 KB took the worker to 1.1 GB.
- **G6 1.0 regression** is pending. There's no committed 1.0 reference result yet (OC-034 AC10), so `results/1.0.0/standard.json` doesn't exist.

# Release gates 2.0.0 — ❌ FAIL

| Gate | Scenario | Value | Target | Result | Run |
|---|---|---|---|---|---|
| G1 Directory throughput | directory | 12981 docs/s | ≥ 15000 docs/s | ❌ fail | 2026-10-03T063037Z-2006732-directory |
| G2 Nested relations throughput | nested | 54493 docs/s | ≥ 8000 docs/s | ✅ pass | 2026-10-03T063209Z-2006732-nested |
| G3 Sorted-merge vs batched | fanout-sorted-merge | 1.00× (1499 vs 1501 docs/s) | ≥ 1.50× | ❌ fail | 2026-10-03T062840Z-2006732-fanout-sorted-merge |
| G4a Memory (directory) | directory | 257 MiB | < 768 MiB | ✅ pass | 2026-10-03T063037Z-2006732-directory |
| G4b Memory (nested) | nested | 320 MiB | < 768 MiB | ✅ pass | 2026-10-03T063209Z-2006732-nested |
| G4c Memory (skewed, sorted-merge) | skewed-sorted-merge | 403 MiB | < 768 MiB | ✅ pass | 2026-10-03T063251Z-2006732-skewed-sorted-merge |
| G4d Memory (fan-out, sorted-merge) | fanout-sorted-merge | 630 MiB | < 768 MiB | ✅ pass | 2026-10-03T062840Z-2006732-fanout-sorted-merge |
| G5 Runner cold start | coldstart | 0.45 s | < 5.00 s | ✅ pass | 2026-10-03T063015Z-dev-coldstart |
| G6 1.0 regression (standard) | standard | — (no result for standard) | ≥ 95% of baseline | ⏳ pending | — |
| G7a No loss (directory) | directory | yes (500000 / 500000 written, verification passed) | yes | ✅ pass | 2026-10-03T063037Z-2006732-directory |
| G7b No loss (nested) | nested | yes (200000 / 200000 written, verification passed) | yes | ✅ pass | 2026-10-03T063209Z-2006732-nested |
| G7c No loss (skewed, sorted-merge) | skewed-sorted-merge | yes (400000 / 400000 written, verification passed) | yes | ✅ pass | 2026-10-03T063251Z-2006732-skewed-sorted-merge |
| G7d No loss (fan-out, sorted-merge) | fanout-sorted-merge | yes (8000 / 8000 written, verification passed) | yes | ✅ pass | 2026-10-03T062840Z-2006732-fanout-sorted-merge |
