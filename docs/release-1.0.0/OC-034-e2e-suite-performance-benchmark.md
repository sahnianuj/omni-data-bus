# OC-034 — End-to-end test suite & performance benchmark (release gate)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E6 Operate & Harden | P0 | 8 | All P0 stories | Implemented |

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
- **Harness:** `go run ./cmd/odbbench` (in `bench/`) provisions the scenario, starts a job through the API, samples `/metrics` and job progress every second, and writes `bench/results/<date>-<sha>.json` plus a markdown summary.
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
| Manual release checklist item with `odbbench --dataset standard --gate release` exit code 0 | `bench/README.md`, `docs/release-1.0.0/INDEX.md` (release checklist) |

## Implementation notes
- **E2E (`web/tests/e2e`, 8 specs, 10 tests):** they run against the e2e compose stack (`deploy/compose/e2e.yml`: the built image as separate api and worker containers, the dev stack's Couchbase and PostgreSQL). Each spec sets up what it doesn't test through the API (`support/api.ts`) and drives the UI for what it does. All 10 pass locally in about 3 minutes. CI runs Chromium on every PR (`ci.yml` › `e2e`, reusing the image built there) and all three browsers nightly (`nightly.yml`).
  - AC1 skips "map remaining fields by drag": every `customer` column auto-maps. The spec asserts the score, previews and migrates.
  - AC2 uses a sample whose names differ and whose types conflict (`phoneNumber` as a number, `active` as a string). The conflicts score between 0.60 and 0.85, so they become suggestions. Edited mappings must be validated again before they're ready.
  - AC4 throttles the 1M-row job (`maxDocsPerSecond: 40000`) so it can be paused mid-run. The crash test uses `docker kill` and then `docker start` on the worker. The restarted worker's reaper requeues the job once its heartbeat is 30 s stale, and the job resumes from its checkpoint: every key is present once, and the job log says "Resumed from checkpoint".
  - AC5 creates its own dirty table, sets the field to `integer`, adds `parseInteger` with strict on, fixes the transform with `nullIf "n/a"`, and retries from the Errors tab.
  - AC6 uses a PostgreSQL role and a Couchbase user with random passwords. It checks that no response body the browser receives contains either password.
  - AC7: 1.0 has no invite flow, so the spec gives a second user a viewer membership in the owner's workspace directly in the e2e metadata scope (`support/db.ts` › `moveMembership`).
  - AC8 runs axe (WCAG 2.1 A/AA) on 13 screens plus the workbench's Table and Design views, in the light and dark themes.
- **Product fixes found by the suite:**
  - Viewers could click **Migrate** and **Run checks** on Review, and **Re-run auto-map** in the score header. `Can` wrapped a `<footer>` and a `<div>`, and disabling those has no effect; each action is now wrapped on its own. `Can` also turns a link styled as a button into a disabled button, because a disabled link still navigates.
  - The workbench nested a second `<main>`.
  - The canvas's state dots had `aria-label` without a role.
  - The canvas's scrollable panels weren't keyboard-focusable.
  - The document tree contained its "Add field" footer (a tree may only own treeitems).
  - Long repository values ran into the next column.
- **Benchmark (`bench/`, its own Go module with only the standard library):**
  - `internal/analyze` holds the gates, the window median, p95 and the rolling-median regression check (AC9).
  - `internal/dataset` generates deterministic `generate_series` SQL with `bigint` rows (`int4` overflows once `g * k` passes 2³¹).
  - `internal/api` is the REST client, plus an SSE reader that measures progress latency.
  - `internal/report` writes the results JSON and markdown and reads the history.
  - The worker serves `net/http/pprof` on `OC_PPROF_ADDR` (its own listener, off by default).
  - Make targets: `make bench` (release) and `make bench-smoke` (nightly: integrity gates plus a 20% regression check against the last 7 runs, kept in the Actions cache).
  - `bench/infra` is Terraform for the reference environment. It passes `terraform validate`, and the user-data templates render; it hasn't been applied.
- **Local indicative run** (laptop, Docker Desktop, `smoke` at 500k rows): 89,945 docs/s sustained, 224 MiB peak RSS, 965 ms p95 progress latency, 500,000 of 500,000 written, verification passed. The `embed` dataset was also checked at 20k customers: every document holds its 10 orders.
- **AC10 is a release-time step:** run `odbbench --dataset standard --gate release` in the AWS reference environment (`bench/README.md`), commit `bench/results/<run>.{json,md}`, and link them from the release notes. It needs AWS access and hasn't been run yet.
