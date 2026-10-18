# 2.5.0 local runs

Indicative laptop runs, not judged by the release gates (they read only `--env reference=true` results).

| Run | What | Result |
|---|---|---|
| `2026-10-18T045158Z-dev-coldstart` | `odb preflight` in an export folder (OC-184), the golden orders fixture against the dev stack's PostgreSQL; Apple M1 Pro, odb darwin/amd64 under Rosetta | median 0.25 s over 5 runs (G5 target < 5 s) |

The 2.5 reference run of `coldstart` is pending. Once it's committed in `results/2.5.0/`, add G5 to [../../../gates-2.5.0.json](../../../gates-2.5.0.json).
