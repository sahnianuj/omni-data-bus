# OC-162 — Point-of-sale fixture and a baseline score for design-first mapping

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P0 | 3 | OC-059, OC-060 | Implemented |

## User story
As an **engineer working on design-first mapping**, I want a realistic source database and customer design, with the mapping we expect from them, so that every 2.5.0 story is measured against the same case and we can see the proposal get better.

## Background (gap found 2026-10-17)
A customer sent two JSON Schemas: the target **cart** document, and one of its **payment** variants (a store-issued card). Their PostgreSQL order database has 30 tables. Importing the cart design with 2.0 (OC-059, then OC-060's *Suggest bindings*) gets the driving table right. Then it falls short:
- `items[].modifiers[]` sits two levels down (`cart_items → customizations`).
- `savedAt` / `orderCollectionType` come from a one-to-one table (`saved_orders`), not the driving table.
- Prices are `numeric(8,2)` dollars, but the design wants integer cents.
- `payments[]` is one array over five mutually exclusive payment tables.

We have no fixture that reproduces this, so there's no way to tell whether a change helps.

## Scope
**In:**
- **`pos` seed schema** in the dev PostgreSQL (`dev/seed/pos/*.sql`, loaded by `make dev-seed`). It has the same tables, keys, foreign keys, unique keys, CHECK constraints and enums as the customer's database, with generic names (`carts`, `cart_items`, `customizations`, `applied_rewards`, `customers`, `partners`, `saved_orders`, `associated_scans`, `collected_payments`, `sccs`, `svcs`, `credit_cards`, `cash_tenders`, `scan_and_pay_qrs`, `stores`, `encryption_keys`). It holds 200 synthetic carts.
  - Tests and the app reach it as a **connected source**, through a project source connection and discovery. No test reads the DDL directly.
- **Designs** under `testdata/design-first/pos/`:
  - `cart.schema.json`, generic and with synthetic examples;
  - `payments/scc.schema.json`, `svc.schema.json`, `credit_card.schema.json`, `cash.schema.json`, `scan_and_pay.schema.json`.
  - Descriptions are kept, because they carry hints (OC-166): "Not populated", "sourced from saved_orders.saved_at", "Document ID: cart::{uuid}".
- **`expected.json`**: the mapping a person would build by hand, as a list of field path → `{source | constant | computed | notPopulated, transform?}` plus the expected bindings.
- **`odb-bench propose-score`** (Go test helper and `make propose-score`). It runs the current proposal pipeline on the fixture against the seeded source and prints, per category, how many of the bindings and fields agree with `expected.json`.
- Record the **2.0 baseline** in this story.

**Out:** customer files in the repository (never committed); a second fixture (the `sales` seed stays the composer's general fixture).

## Implementation details
- The seed is idempotent, like the existing `sales` seed: `CREATE SCHEMA IF NOT EXISTS pos`, then a fixed random seed for the rows.
- Every payment row sets exactly one of the five payment foreign keys, enforced by the same `CHECK (num_nonnulls(...) = 1)` shape as the customer's database, so OC-171 has a real constraint to discover.
- `customers.cart_id` is nullable and not unique (as at the customer). `saved_orders` and `associated_scans` use `cart_id` as their primary key, so they're one-to-one with `carts`.
- Score categories: `driving`, `bindings`, `fields.direct`, `fields.converted`, `fields.computed`, `fields.constant`, `fields.notPopulated`, `unresolved` (fields the expected mapping also leaves without a source).

## Acceptance criteria & unit tests

### AC1 — The fixture is a connected source
**Given** `make dev-seed`, **when** a project's source connection points at the dev PostgreSQL, **then** discovery lists the 16 `pos` tables with their foreign keys, unique keys and enum values. The relationship graph from `pos.carts` has `cart_items`, `customers`, `saved_orders`, `associated_scans`, `collected_payments`, `applied_rewards` and `sccs` under Referenced by, and `partners` and `stores` under References.

| Test | Location | Verifies |
|---|---|---|
| `TestPOSFixture_AC1_Discovery` *(integration)* | `backend/internal/automap/propose_integration_test.go` | |

### AC2 — Baseline recorded
**When** `make propose-score` runs on 2.0 behaviour (import + `SuggestBindings` + `RunAll`), **then** it prints a per-category score. The numbers are copied into this story under *Baseline*.

| Test | Location | Verifies |
|---|---|---|
| `TestProposeScore_AC2_Report` *(integration)* | `backend/internal/automap/propose_integration_test.go` | Report format; not a gate |

## Baseline
2.0 behaviour on the `pos` fixture (`make propose-score` with `baseline20`: rank the driving table, accept the high binding suggestions, auto-map all), 2026-10-17:

| Category | Correct |
|---|---|
| driving | 1/1 (100 %) |
| bindings | 4/11 (36 %) |
| fields.direct | 48/65 (74 %) |
| fields.converted | 1/5 (20 %) |
| fields.computed | 0/5 (0 %) |
| fields.constant | 0/3 (0 %) |
| fields.notPopulated | 0/9 (0 %) |
| unresolved | 1/1 (100 %) |

What it misses: nothing two levels down (`items[].modifiers[]`), no wrapped N:1 array (`appliedRewards[]`), no one-to-one lookups (`savedAt`), no constants or not-populated fields, and no cents conversion. Prices get `parseInteger`, which **truncates dollars at high confidence** (5 wrong high-confidence fields, including `id` and `status`).

## Implementation notes (2026-10-17)
- The fixture is the Go package `backend/internal/fixtures/pos` (embedded files, so any test can use it), not `testdata/design-first/pos/`:
  - `schema.sql`: 16 tables, enums, foreign keys, one-to-one tables, and both exclusive-arc CHECK forms. 200 carts, 500 items, 500 modifiers, 142 customers (some carts have two), 20 saved orders and 157 payments, all deterministic (md5 UUIDs);
  - `designs/cart.schema.json` and `designs/payments/{scc,svc,credit_card,cash,scan_and_pay}.schema.json`;
  - `expected.json`: 11 containers and 88 fields.
- `pos.Apply(ctx, conn)` loads it once, under an advisory lock, unless `pos.carts` exists. The dev stack loads it on first start (`deploy/compose/seed/source/05-pos.sh` runs it from `/seed/pos`, the fixture folder `dev.yml` mounts), and `make dev-seed` reloads it. It also holds the `crm` and `support` variant fixtures of OC-171 and grants `omni_ro` read access.
- `pos.Score(spec, expected)` returns the per-category report, the misses and the wrong high-confidence fields. `make propose-score` (instead of an `odb-bench` subcommand) runs `TestProposeScore*` against the dev stack.

