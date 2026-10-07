# OC-140 — Computed fields everywhere: export, older workers, E2E and benchmark

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Computed Fields | P0 | 3 | OC-137, OC-138, OC-050, OC-051, OC-056, OC-057 | Not started |

## User story
As a **data engineer** who exports migrations and runs them elsewhere, I want computed fields to work the same in exported bundles, `migrate.sh` and the all-in-one image, and an older build to refuse a mapping it can't run, so that no document is ever written without its computed fields.

## Scope
**In:**
- **Bundles (OC-050):** `computed` is part of the bundle's mapping and of its integrity hash; `api/bundle.schema.json` documents it. Bundle format version is unchanged (an additive field), but see the next point.
- **Older workers and runners (OC-057 mechanism):** a mapping with a computed field requires the computed-fields capability level. A worker or `odb` binary built before this story refuses to claim the job or run the bundle, with `MAPPING_NEWER_VERSION` ("This mapping uses computed fields; upgrade odb to ≥ <version>"), instead of ignoring an unknown field. `mapping.RequiresComputed(spec)` sets `minWorkerLevel` on jobs and bundles.
- **`odb run` and `migrate.sh`:** same evaluation as the server; `migrate.sh preflight` compiles every computed expression and fails (exit 4) on an invalid one.
- **Validation gate:** export and job creation apply the MAPPING_INVALID gate to computed issues (cycle, unknown field, syntax), as they do to other errors (OC-049).
- **E2E:** one Playwright spec covering add field → JavaScript → live result → migrate → document browser shows the value, tagged `@smoke` for the happy path (CI runs smoke only).
- **Benchmark gate:** add **G8 — Computed fields throughput** to `bench/gates-2.0.0.json`: the `nested` scenario plus 2 template and 2 JavaScript fields (one at the root, one per item) must keep ≥ 85% of G2's throughput, and peak RSS stays within G4b.
- **Docs:** a user-guide chapter "Computed fields" (value sources, scope names, examples from OC-137 AC1, limits, determinism), linked from the Inspector's help icon (OC-057 `HelpLink`).

**Out:** nothing beyond the above.

## Acceptance criteria & unit tests

### AC1 — Bundle round trip and parity
**Given** a mapping with template and JavaScript fields, **when** it's exported and run with `odb run`, **then** the documents are byte-identical to the server pipeline's, for the Couchbase and directory targets.

| Test | Location | Verifies |
|---|---|---|
| `TestExports_AC1_ComputedInBundle` | `backend/internal/export/bundle_test.go` | Schema + integrity hash |
| `TestParity_AC3_GoldenMappings` (adds `computed-js.json`) | `backend/internal/headless/parity_integration_test.go` | |

### AC2 — Older builds refuse
**Given** a job whose mapping has a computed field and a worker below the required level, **then** the worker doesn't claim it. **Given** such a bundle and an `odb` binary below the level, **then** `odb run` exits 4 with `MAPPING_NEWER_VERSION`.

| Test | Location | Verifies |
|---|---|---|
| `TestRequiresComputed_AC2_MinWorkerLevel` | `backend/internal/mapping/release_compat_test.go` | |
| `TestClaim_AC2_SkipsComputedForOldWorker` | `backend/internal/jobs/claim_test.go` | |

### AC3 — Preflight and gates
**Given** a bundle edited by hand to contain a computed cycle, **then** `migrate.sh preflight` exits 4 naming the field. **Given** a mapping with a computed syntax error, **then** export and job creation return 422 MAPPING_INVALID.

| Test | Location | Verifies |
|---|---|---|
| `migrate_run.bats` › `OC-140 AC3: preflight rejects invalid computed field` | `backend/internal/export/testdata/bats/migrate_run.bats` | |
| `TestExports_AC3_ComputedGate` | `backend/internal/export/bundle_test.go` | |

### AC4 — End to end
**Given** the dev stack, **when** a user adds `fullName` as JavaScript on the orders mapping, sees the live result, migrates and opens a document, **then** `fullName` is there with the expected value.

| Test | Location | Verifies |
|---|---|---|
| `computed-fields.spec.ts` `@smoke` | `web/tests/e2e/computed-fields.spec.ts` | |

### AC5 — Benchmark gate
**Given** `make bench-gate`, **then** G8 is evaluated and recorded under `bench/results/2.0.0/`.

| Test | Location | Verifies |
|---|---|---|
| `odbbench gate` with G8 | `bench/gates-2.0.0.json` | |
