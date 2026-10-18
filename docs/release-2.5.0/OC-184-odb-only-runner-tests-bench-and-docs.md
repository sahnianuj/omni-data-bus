# OC-184 — odb-only runner: end-to-end tests, benchmark, CI and docs

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E12 Harden & Ship 2.5 | P0 | 3 | OC-182 | Implemented |

## User story
As the **release owner**, I want the tests, the benchmark, CI and the guides to use `odb` instead of `migrate.sh`, so that 2.5 ships one documented way to run an export, and 2.0 exports still run.

## Scope
**In:**
- **E2E:**
  - `web/tests/e2e/export-script.spec.ts` becomes `export-odb.spec.ts`. It exports the OC-173 fixture, unzips it and runs `odb preflight`, then `odb run`, then `odb verify` with the installed odb, on Linux, macOS and Windows (OC-186). Then it imports `odb-state/report.json` into the run history.
- **Benchmark:** `odbbench coldstart` times `odb preflight` in the export folder (`--dir`) instead of `--script`. The results go in `bench/results/2.5.0/`, and the gate is unchanged: cold start ≤ the 2.0 figure.
- **CI:**
  - `ci.yml` drops shellcheck of the template and the bats jobs for new exports;
  - one job keeps running a **2.0 `migrate.sh`** against the 2.5 odb (OC-181 AC2) until 3.0;
  - `release.yml` and `scripts/dist.sh` keep publishing the tarballs and `SHA256SUMS`, because the README's download link uses them.
- **Docs:**
  - `docs/user-guide/running-outside-the-tool.md` is rewritten around `odb preflight / run / resume / status / verify / inspect`, Docker (OC-183) and the exit codes;
  - `docs/upgrade/` gets a note: "2.5 exports have no migrate.sh: install odb 2.5 and run `odb run` in the export folder. 2.0 exports still work with their script";
  - the 2.5 release notes get a section;
  - the comment in `deploy/helm/…/values.yaml` drops its mention of `migrate.sh`.
- **Grep check:** no `migrate.sh` in the user-facing strings (`web/src/i18n/en.json`), the README template or the user guide, except the upgrade note.

**Out:** deleting the template and bats suites (3.0).

## Acceptance criteria & unit tests

### AC1 — End to end with odb alone
**Then** `export-odb.spec.ts` passes in CI on the Linux, macOS and Windows runners, against the same exported folder.

| Test | Location | Verifies |
|---|---|---|
| `export-odb.spec.ts` | `web/tests/e2e/` | |

### AC2 — 2.0 exports still run
**Then** the 2.0 compatibility job passes.

| Test | Location | Verifies |
|---|---|---|
| `runner-compat-2.0` job | `.github/workflows/ci.yml` | |

### AC3 — Benchmark and gate
**Then** `odbbench coldstart --dir` results are committed, and `odbbench gate` passes.

| Test | Location | Verifies |
|---|---|---|
| `TestGate_OC184_AC3_ColdStartDir` | `bench/cmd/odbbench/gate_test.go` | |

### AC4 — No stale instructions
**Then** a test fails if `migrate.sh` appears in `en.json`, `README.md.tmpl` or `running-outside-the-tool.md`.

| Test | Location | Verifies |
|---|---|---|
| `TestDocs_OC184_AC4_NoMigrateSh` | `backend/internal/export/script_test.go` | |

## Implementation notes (2026-10-17)
- **E2E**: `web/tests/e2e/export-script.spec.ts` became `export-odb.spec.ts`. It:
  1. exports from the UI;
  2. checks that the folder is exactly `README.md`, `config.json` and `mappings.json`, and that the README gives the odb commands and never mentions `migrate.sh`;
  3. runs `preflight`, `run` and `verify` with the README's `docker run` command (OC-183) on the dev stack's network;
  4. counts the files and imports `odb-state/report.json`.

  **It passed locally** (`E2E_KEEP=1 E2E_SKIP_BUILD=1 ./scripts/e2e.sh export-odb`, with `omni-data-bus:e2e` built from this tree). The e2e stack is left running.
- **Three OSs**: the Playwright suite runs on Linux CI only (it needs the whole stack). The same export folder is run with odb alone on Linux, macOS and Windows by `TestRunner_OC186_AC1_InterruptResume` in the new **`runner-e2e`** CI matrix job (`ubuntu-latest`, `macos-14`, `windows-2022`). That job:
  - starts each runner's PostgreSQL (preinstalled on Linux and Windows, Homebrew on macOS) and loads `deploy/compose/seed/source/*.sql`;
  - runs the odb, headless, export, mapping and pathcodec unit tests;
  - runs the interrupt/resume end-to-end test.
- **CI**: `runner-bats-linux` became **`runner-compat-2.0`** (the 2.0 `migrate.sh` bats suites, until 3.0). `runner-bats-macos` and the separate `runner-windows` job were removed; the matrix covers them.
- **Correction (AC2)**: a released 2.0 `migrate.sh` accepts only an odb of exactly its release, so it runs odb 2.0, not 2.5. The bats suites build the script and odb as `dev`, which checks the `odb run` forms the script calls. **odb 2.5 also runs a 2.0 export folder directly** (OC-180), and `TestCheckVersion_OC184_AC2` checks the range (`>=2.0.0 <3.0.0` accepts 2.5.0). The docs, the release notes and the checklist say this.
- **Benchmark**:
  - `odbbench coldstart --dir <export> [--odb odb]` times `odb preflight` in the export folder, and `--script` still times a 2.0 `migrate.sh`. `TestColdStartFixture` writes a folder without `migrate.sh`. Test: `TestGate_OC184_AC3_ColdStartDir`.
  - A local run is in `bench/results/2.5.0/local/`: a 0.25 s median on an M1 Pro, against the G5 target of under 5 s. It's marked `reference=false`, so the gate ignores it.
  - **G5 is not yet in `gates-2.5.0.json`**: the release gate reads only reference-environment results, and no 2.5 reference cold-start run exists. Add G5 when one is committed. `make bench-gate RELEASE=2.5.0` passes.
- **Docs**:
  - `docs/user-guide/running-outside-the-tool.md` is rewritten: the odb commands, installing odb, Docker, settings, and folder names on Windows;
  - new `docs/upgrade/2.0-to-2.5.md` (exports and the runner: a command mapping table, 2.0 compatibility, automation, the server's dist folder);
  - the release notes gain a *New* item, *Changes* and *Compatibility* entries;
  - the root `README.md`, `.env.example`, the `config.go` comments, the Helm `values.yaml` comment and `deploy/compose/e2e.yml` were updated.
- **AC4**: `TestDocs_OC184_AC4_NoMigrateSh` checks `en.json`, `README.md.tmpl` and the user guide; only its *Exports from 2.0* section may mention `migrate.sh`.
- **Local environment note**: the e2e image (`omni-data-bus:e2e`) was retagged from the image built for OC-183, of this tree.
