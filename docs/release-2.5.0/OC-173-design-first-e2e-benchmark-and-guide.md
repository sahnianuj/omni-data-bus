# OC-173 — Design-first mapping: end-to-end tests, a quality gate, a benchmark and the guide

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E12 Harden & Ship 2.5 | P0 | 3 | OC-162, OC-163, OC-164, OC-165, OC-166, OC-167, OC-168, OC-169, OC-170, OC-171, OC-172 | Implemented |

## User story
As the **release owner**, I want design-first mapping proven end to end against a real source, with a quality gate on how much of the customer's design it gets right and a guide for users, so that 2.5.0 ships knowing the proposal is good and stays good.

## Scope
**In:**
- **E2E** (`web/tests/e2e/design-first.spec.ts`, against the seeded `pos` source):
  1. *New mapping from a design* with the cart and payment designs;
  2. the proposal picks `pos.carts`;
  3. the user reviews and changes one binding and one field;
  4. **Apply**;
  5. preview, run a job, and verify;
  6. export, then `odb run` gives the same documents.

  A second run imports the design onto the now-existing mapping and goes through the OC-170 modal, with *Update*.
- **Quality gate G10** in `bench/gates-2.5.0.json`, from `make propose-score` (OC-162). On the `pos` fixture against `expected.json`:
  - the driving table is correct;
  - bindings ≥ 95 %;
  - fields (direct + converted + constant + not populated) ≥ 90 %;
  - computed and aggregate fields ≥ 70 %;
  - zero *wrong* high-confidence fields: a high-band proposal that disagrees with `expected.json` fails the gate even when the totals pass.
- **Benchmark:** `Propose` on 300 tables and a 200-container design takes < 2 s (OC-163 AC4), and variant arrays (all three source patterns) run within 10 % of the throughput of a plain 1:N array of the same rows (OC-171).
- **User guide:** a new page, *Start from the customer's design*:
  - importing several files;
  - what the proposal decides and the reasons behind it;
  - review mode and its filters;
  - what "not populated" means;
  - the already-mapped modal;
  - variant shapes (`oneOf`, several files; exclusive arc, type column, union of tables).

  Screenshots come from the `pos` fixture.
- **Release notes** for 2.5.0.

**Out:** gates on customer data (customer files are never in CI).

## Acceptance criteria & unit tests

### AC1 — End to end
**Then** the E2E passes in CI, and the documents from the job and from `odb run` are byte-identical.

| Test | Location | Verifies |
|---|---|---|
| `design-first.spec.ts` | `web/tests/e2e/design-first.spec.ts` | |

### AC2 — Quality gate
**Then** `make propose-score` passes G10, and a deliberately broken scorer (a test-only flag) fails it on the "wrong high-confidence" rule.

| Test | Location | Verifies |
|---|---|---|
| `TestProposeGate_AC2` *(integration)* | `backend/internal/automap/propose_integration_test.go` | |

### AC3 — Guide published
**Then** `docs/user-guide/design-first.md` is linked from the user-guide README and from the Import dialog's help link (`helpLinks.ts`).

| Test | Location | Verifies |
|---|---|---|
| `helpLinks.test.ts` › `OC-173 AC3` | `web/src/lib/helpLinks.test.ts` | |

## Implementation notes (2026-10-17)
- **E2E** (`web/tests/e2e/design-first.spec.ts`), passing against the built image and the dev stack's pos fixture:
  1. the cart and its five payment designs are imported as one, with *Propose* on;
  2. review: carts, customer 1:N first, payments with their variants, prices × 100;
  3. fine-tune: one row taken back, one marked not populated, both shown *edited*;
  4. **Apply**;
  5. the sample shows `paymentType`;
  6. the migration completes with all 200 carts written;
  7. *New mapping from a design* on carts asks first, and *Update this mapping* opens the import.

  The E2E found two bugs, both fixed: variant rows read *edited* when untouched, and *Take back* on one variant unbound the whole array.

  **The runner comparison is the parity test, not the E2E:** `TestParity_AC3_GoldenMappings` now includes the proposed `pos.carts` mapping (payment variants included), and the server job and `odb run` give byte-identical documents.
- **Gate G10** (`bench/gates-2.5.0.json`):
  - `bench`'s gate tool has a `quality` metric: a result's named score, with an inclusive max;
  - `make propose-score` runs `TestProposeGate_AC2` and writes `bench/results/2.5.0/propose-quality.json`, marked a reference run (a correctness score is the same on any machine);
  - `make bench-gate RELEASE=2.5.0` passes: 100 % on every category, 0 wrong high-confidence proposals.

  The test also checks that a proposal with one wrong high-confidence field fails even while its totals pass.
- **Benchmarks:**
  - `TestPropose_AC4_Budget`: 300 tables × 200 containers in 0.67 s, with candidates cached per container and table;
  - `TestVariants_OC173_ThroughputBudget`: a variant array costs no more than a plain one over the same rows (5.5 µs vs 7.2 µs per document, since each row's variant is picked once);
  - both skip under `-race` or coverage.
- **Guide:** `docs/user-guide/design-first.md`, linked from the user-guide README and the composer chapter, and from the help icon of *Import JSON design…* and *New mapping from a design…* (`helpLinks.ts`: `designFirst`, `variants`).
- **Release notes:** `docs/release-2.5.0/RELEASE-NOTES.md`.
- **Dev stack:** the fixture is loaded on a fresh volume by `deploy/compose/seed/source/05-pos.sh`, from the fixture folder mounted at `/seed/pos`. Mounting the SQL file inside the read-only seed folder failed when the container was recreated, which the first E2E run found.

