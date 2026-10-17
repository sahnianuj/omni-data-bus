# OC-140 — Computed fields everywhere: export, older workers, E2E and benchmark

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Computed Fields | P0 | 3 | OC-136, OC-138, OC-050, OC-051, OC-057 | Implemented |

> **2.0 scope (2026-10-16):** template computed fields. The Playwright spec and the G8 throughput gate move to OC-137 (release 3.0), which adds JavaScript and is where the throughput question is: templates are native Go and are covered by OC-136's benchmark.

## User story
As a **data engineer** who exports migrations and runs them elsewhere, I want computed fields to work the same in exported bundles, `migrate.sh` and the all-in-one image, and an older build to refuse a mapping it can't run, so that no document is ever written without its computed fields.

## Scope
**In:**
- **Bundles (OC-050):** `computed` is part of the bundle's mapping and of its integrity hash; `api/bundle.schema.json` documents it. Bundle format version is unchanged (an additive field), but see the next point.
- **Older workers and runners (OC-057 mechanism):** computed fields ship with 2.0, so `mapping.RequiresV2` lists "computed fields" and a job with them needs a 2.0 worker: a 1.0 worker never claims it during an upgrade. Bundles already carry `odbVersion: >=2.0.0 <3.0.0`, which no 1.0 build runs.
- **`odb run` and `migrate.sh`:** same evaluation as the server; `migrate.sh preflight` compiles every computed expression and fails (exit 4) on an invalid one.
- **Validation gate:** export and job creation apply the MAPPING_INVALID gate to computed issues (cycle, unknown field, syntax), as they do to other errors (OC-049).
- **Docs:** a user-guide chapter "Computed fields" (value sources, scope names, examples from OC-137 AC1, limits, determinism), linked from the Inspector's help icon (OC-057 `HelpLink`).

**Out:** nothing beyond the above.

## Acceptance criteria & unit tests

### AC1 — Bundle round trip and parity
**Given** a mapping with template fields, **when** it's exported and run with `odb run`, **then** the documents are byte-identical to the server pipeline's.

| Test | Location | Verifies |
|---|---|---|
| `TestRunComputed_OC140_BundleAndPreflight` | `backend/internal/headless/runconfig_test.go` | A bundle with a template field: `odb run` writes it |
| `TestParity_AC3_GoldenMappings` | `backend/internal/headless/parity_integration_test.go` | Server vs `odb run`, byte-identical |

### AC2 — Older builds refuse
**Given** a job whose mapping has a computed field and a worker below the required level, **then** the worker doesn't claim it. **Given** such a bundle and an `odb` binary below the level, **then** `odb run` exits 4 with `MAPPING_NEWER_VERSION`.

| Test | Location | Verifies |
|---|---|---|
| `TestRequiresComputed_AC2_MinWorkerLevel` | `backend/internal/mapping/release_compat_test.go` | Jobs with computed fields need a 2.0 worker (`RequiresV2`) |

### AC3 — Preflight and gates
**Given** a bundle edited by hand to contain a computed cycle, **then** `migrate.sh preflight` exits 4 naming the field. **Given** a mapping with a computed syntax error, **then** export and job creation return 422 MAPPING_INVALID.

| Test | Location | Verifies |
|---|---|---|
| `TestRunComputed_OC140_BundleAndPreflight` | `backend/internal/headless/runconfig_test.go` | Preflight exit 4 on a cycle; the same validation gates export and job creation |

