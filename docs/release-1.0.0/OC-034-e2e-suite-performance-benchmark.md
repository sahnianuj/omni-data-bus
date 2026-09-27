# OC-034 — End-to-end test suite & performance benchmark (release gate)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E6 Operate & Harden | P0 | 8 | All P0 stories | In progress |

## User story
As the **release owner**, I want automated end-to-end tests of the real user journeys and a repeatable performance benchmark with pass/fail gates, so that we only ship 1.0.0 when the product works end-to-end and meets its performance targets.

## Implementation details

### E2E (Playwright, `web/tests/e2e`)
- Runs against the docker-compose stack (OC-001 dev compose, built image). Global setup: seed the source PG, reset the metadata bucket, init Couchbase, and create a test user via the API.
- **Page objects** for SignIn, Projects, ProjectWizard (SourceStep, TargetStep, TablesStep), MappingWorkbench, DocDesigner, JobMonitor.
- `data-testid` is used only where roles and labels are insufficient. Queries prefer `getByRole`.
- Traces, videos, and screenshots are kept on failure. Browsers: Chromium (required) and Firefox/WebKit (nightly).
- Tests run serially per worker (shared Couchbase), with keyspaces named per test (`e2e_<random>`) for isolation.

### Scenarios (each one is an AC below)
1. Golden path: create project → source (test, save) → target (test, save) → select `customer` → auto-map score visible → map remaining fields by drag → preview → migrate → progress to 100% → verification passed.
2. Design import: drop a JSON sample → score reflects partial match → accept suggestions → migrate.
3. Embeds: embed `orders` into `customer` → preview shows the array → migrate → spot-check a document.
4. Pause and resume a 1M-row job; kill the worker container mid-run → the job resumes → final count matches.
5. Error handling: a strict parse on dirty data → completed_with_errors → errors tab → fix the transform → retry failed rows → completed.
6. Credentials stored once: reload and sign in again → open the project → browse tables and start a job without re-entering passwords; secrets never appear in network responses.
7. Access control: a viewer sees but cannot edit or migrate; a cross-workspace URL gives 404.
8. Accessibility sweep: axe on every main screen in light and dark themes.

### Performance benchmark (`bench/`)
- **Harness:** `go run ./bench/cmd/ocbench` provisions the scenario, starts a job through the API, samples `/metrics` and job progress every second, and writes `bench/results/<date>-<sha>.json` plus a markdown summary.
- **Datasets** (generated in the source PG with `generate_series`, deterministic seeds):
  - `narrow`: 10M rows, 8 columns (~200 B docs).
  - `standard`: 5M rows, 20 columns (~1 KB docs) — the **gating dataset** for the NFR.
  - `wide`: 1M rows, 120 columns (~6 KB docs).
  - `embed`: 1M customers + 10M orders (avg 10 per customer) as an array embed.
- **Reference environment:**
  - Worker: 8 vCPU / 16 GB.
  - Source: PG16, 8 vCPU.
  - Couchbase: 3-node 8.x or 7.6 cluster, 8 vCPU / 32 GB each, bucket RAM sufficient, same region/AZ.
  - Documented in `bench/README.md` with Terraform for AWS (`bench/infra/`).
- **Gates** (from [architecture §9](00-architecture.md#9-non-functional-requirements-release-gate)):
  - `standard` ≥ 20,000 docs/s sustained (median of the 1 s samples between 10% and 90% progress);
  - worker RSS < 512 MB;
  - progress latency ≤ 2 s (event timestamp vs UI receive, measured by a headless client);
  - zero data loss (count and sample verify pass).
- **CI:** a smaller `smoke-bench` (500k rows, compose, GitHub runner) runs nightly with **relative** regression detection: it fails if throughput is 20% below the rolling median of the last 7 runs. The full benchmark runs manually before release, and its results are attached to the release notes.
- **Profiling:** `OC_PPROF_ADDR` enables `net/http/pprof` on the worker for benchmark runs, and the harness captures a 30 s CPU and heap profile at 50% progress.

## Acceptance criteria & tests
(For this story, the "unit" tests are the E2E specs and the harness's own unit tests.)

### AC1 — Golden path E2E
**Given** a fresh stack, **when** the golden-path spec runs, **then** it passes end-to-end: the score header shows "N of M fields automatically mapped", the progress bar reaches 100%, verification shows Passed, and the Couchbase doc count equals the source row count.

| Test | Location |
|---|---|
| `golden-path.spec.ts` › `AC1: project → repos → map → migrate → verify` | `web/tests/e2e/golden-path.spec.ts` |

### AC2 — Design import & suggestions E2E
| Test | Location |
|---|---|
| `design-import.spec.ts` › `AC2: drop JSON sample, review suggestions, migrate` | `web/tests/e2e/design-import.spec.ts` |

### AC3 — Embeds E2E
| Test | Location |
|---|---|
| `embeds.spec.ts` › `AC3: orders embedded as array` | `web/tests/e2e/embeds.spec.ts` |

### AC4 — Resilience E2E (pause, resume, worker kill)
| Test | Location |
|---|---|
| `resilience.spec.ts` › `AC4: pause/resume 1M rows` | `web/tests/e2e/resilience.spec.ts` |
| `resilience.spec.ts` › `AC4: worker killed mid-run resumes with no loss` | same (uses `docker kill` via a test fixture) |

### AC5 — Error flow E2E
| Test | Location |
|---|---|
| `errors.spec.ts` › `AC5: completed_with_errors → fix → retry failed rows` | `web/tests/e2e/errors.spec.ts` |

### AC6 — Credentials stored once per project E2E
**Given** a project with saved repositories, **when** the user signs out and back in and runs a job, **then** there is no password prompt, and no network response body contains the source or Couchbase password (Playwright response interception).

| Test | Location |
|---|---|
| `credentials.spec.ts` › `AC6: reuse stored project credentials; secrets never returned` | `web/tests/e2e/credentials.spec.ts` |

### AC7 — Access control E2E
| Test | Location |
|---|---|
| `access.spec.ts` › `AC7: viewer read-only; cross-workspace 404` | `web/tests/e2e/access.spec.ts` |

### AC8 — Accessibility sweep
| Test | Location |
|---|---|
| `a11y.spec.ts` › `AC8: axe on all main screens (light/dark)` | `web/tests/e2e/a11y.spec.ts` (`@axe-core/playwright`) |

### AC9 — Benchmark harness correctness
**Given** synthetic progress samples, **then** the harness computes the sustained throughput (the 10–90% window median), p95 progress latency, and peak RSS correctly, and gate evaluation returns pass or fail with reasons.

| Test | Location |
|---|---|
| `TestSustainedThroughput_AC9_WindowMedian` | `bench/internal/analyze/analyze_test.go` |
| `TestGates_AC9_PassFail` | same |
| `TestRegression_AC9_RelativeToRollingMedian` | same |

### AC10 — Performance gates met
**Given** the reference environment and the `standard` dataset, **then** the full benchmark meets every gate. The results JSON and summary are committed under `bench/results/` and linked from the release notes.

| Test | Location |
|---|---|
| Manual release checklist item with `ocbench --dataset standard --gate` exit code 0 | `bench/README.md`, `docs/release-1.0.0/INDEX.md` (release checklist) |
