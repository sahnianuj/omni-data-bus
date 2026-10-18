# OC-163 — Import a design and get the whole mapping proposed

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P0 | 8 | OC-162, OC-059, OC-060, OC-049 | Implemented |

## User story
As a **data engineer** who has the customer's target JSON design, I want to import it and have the tool decide, against the connected source, which table the documents come from, which related tables fill each embedded object and array, and which column fills each field, so that I start from a complete proposed mapping instead of building it step by step.

## Background (gap found 2026-10-17)
In 2.0 a design import leaves everything unbound. The user then has to:
1. click *Suggest bindings* (OC-060);
2. accept the driving table;
3. accept the bindings, which refetches suggestions after each accept because children's scopes change;
4. run *Auto-map all* (OC-049).

The driving table is picked from the root's scalar fields alone, and auto-map only runs once everything is bound. None of this is wrong, but it isn't what the user asked for: "here is the document I want; work out where it comes from".

## Scope
**In:**
- **One server-side pipeline**, `automap.Propose(design, source, graph)`. It returns a `Proposal` with:
  - the driving table and up to 2 alternatives;
  - a binding for every object and array (OC-164; variants from OC-171);
  - a source for every scalar field: a column, a conversion (OC-167), a lookup from a one-to-one table (OC-165), a constant or a not-populated hint (OC-166);
  - the unresolved fields with their reasons (OC-169);
  - coverage counts.
- **Better driving-table choice.** Each candidate table is scored on its fit with the whole design, not just the root's fields:
  - `0.40·rootFit`: as in OC-060.
  - `0.35·reach`: the share of the design's containers that a binding from this table could fill, with a score of at least 0.5.
  - `0.15·key`: the design's key template (OC-166, e.g. `cart::{uuid}`) or an `id` field whose examples match the table's primary-key type.
  - `0.10·name`: the design's `title` (`CartDocument`), the mapping name and the target collection against the table name (`carts`).
- **Entry points.** These all run the proposal; the user reviews it in OC-168:
  - the composer's *Import JSON design…* has a *Propose the mapping from the source* option, on by default when the mapping is empty;
  - the mappings list has a new *New mapping from a design…* action: choose the design, and the tool picks the driving table;
  - the composer toolbar has *Propose again*.
- **API.** `POST /projects/{pid}/mappings/{id}/propose {design?, specOverride?, drivingTable?}` returns a `Proposal`. It's read-only (viewer role), like `suggest-bindings`. `POST /projects/{pid}/mappings/propose {design}` creates nothing; *New mapping from a design* uses it to choose the table before the mapping exists.
- **Budget.** Under 2 s for a 300-table source and a 200-container design, measured server-side and excluding discovery (which is cached, OC-005).

**Out:** applying the proposal, which is the review step (OC-168); ML or calls to any external service; reading the source from anything other than the project's source connection.

## Implementation details
- `internal/automap/propose.go`: `Propose(in ProposeInput) Proposal`. It reuses:
  - `SuggestBindings`' scorer and links, deepened by OC-164;
  - the OC-017 field scorer (Hungarian per scope).
- **Order:** score the driving tables, then pick the best. Bind the containers top-down, each in the scope its parent proposes. Map each scope's fields in one assignment over the columns in scope (the table's own columns plus the one-to-one lookups from OC-165).
  - When the best driving table changes the result for less than 5 % of fields compared with the runner-up, both are kept, and the review shows the choice (OC-168).
- `ProposeInput.Source` is the cached discovery snapshot of the project's source connection. `ProposeInput.Graph` comes from `relations.Build` + `Junctions` + per-table hops (OC-141). Nothing is read from uploaded files.
- `Proposal` is a value, not a spec. `proposal.Apply(spec) (spec, ApplyReport)` turns it into spec operations, and the web client calls the same operations (`composerOps.applyProposal`) so the result can be undone.

## Acceptance criteria & unit tests

### AC1 — The cart design picks `pos.carts`
**Given** the `pos` fixture as the connected source and `cart.schema.json`, **then** the proposal drives from `pos.carts` with a high score, ahead of `pos.saved_orders` and `pos.cart_items`. The reasons include "key: cart::{uuid} ↔ carts.id uuid" and "reach 7/9 containers".

| Test | Location | Verifies |
|---|---|---|
| `TestPropose_AC1_DrivingTable` | `backend/internal/automap/propose_test.go` | |

### AC2 — Whole proposal in one call
**Given** the same input, **then** one `propose` call returns a binding for `customer`, `partner`, `items[]`, `items[].modifiers[]`, `items[].appliedRewards[]` and `payments[]`, plus *map from parent* for `pricing`, and a source or hint for every scalar field. `make propose-score` beats the OC-162 baseline in every category.

| Test | Location | Verifies |
|---|---|---|
| `TestPropose_AC2_EndToEnd` *(integration)* | `backend/internal/automap/propose_integration_test.go` | Against the seeded source and `expected.json` |

### AC3 — New mapping from a design
**When** the user picks *New mapping from a design…*, chooses `cart.schema.json` and confirms the proposed table, **then** a composed mapping of `pos.carts` is created with the design imported, and the review (OC-168) opens on the proposal.

| Test | Location | Verifies |
|---|---|---|
| `NewFromDesign.test.tsx` › `OC-163 AC3` | `web/src/features/mapping/NewFromDesign.test.tsx` | |

### AC4 — Read-only and within budget
**Given** a viewer, **then** `propose` succeeds and the spec is unchanged. **Given** 300 tables and 200 containers, **then** `Propose` completes in < 2 s.

| Test | Location | Verifies |
|---|---|---|
| `TestProposeHandler_AC4_ReadOnly` | `backend/internal/mappings/propose_test.go` | |
| `BenchmarkPropose_AC4` | `backend/internal/automap/propose_bench_test.go` | |

## Implementation notes (2026-10-17)
- **Engine:** `automap.Propose` (`backend/internal/automap/propose.go`).
  - **Driving-table ranking:** `0.40·rootFit + 0.35·reach + 0.15·key + 0.10·name`, where the name is the imported file's name.
    - In `reach`, a container that only groups the table's own columns counts half.
    - Containers the design marks "Not populated" don't count.
    - Sources over 20 tables are ranked in full only for a shortlist of 20, picked by a cheap pre-score (root-field fit, name, whether the table has links).
  - **Scope walk:** fields first, then containers, each in its proposed parent scope.
  - **Output:** `Proposal{driving, spec, items, coverage}`. `spec` is the design with everything applied, and each item records the relations, mappings and aggregates it added.
- **Endpoints:**
  - `POST …/mappings/{mid}/propose {specOverride, drivingTable, base, nodeId}`;
  - `POST …/mappings/propose {docSchema, drivingTable}`.

  Both are read-only and use the project's discovery snapshot and relationship graph.
- **Entry points:**
  - *Import JSON design…* has *Propose the mapping from the source*, on by default for an empty mapping;
  - the toolbar's *Propose mapping*;
  - the mappings list's *New mapping from a design…* (`NewFromDesign.tsx`). It creates the mapping, saves the design, and opens the composer with `?propose=1`.
- **Score on the pos fixture:** driving 1/1. Bindings 10/11: `payments[]` is OC-171's. Direct, converted, computed, constant and not-populated fields are all at 100 %, and there are 0 wrong high-confidence fields (2.0 had 5).
- **Budget:** 300 tables × 200 containers in 1.26 s (`TestPropose_AC4_Budget`). It skips under `-race` or coverage, like the other budgets.
- **Tests:**
  - `TestPropose_AC1_DrivingTable` (`propose_test.go`);
  - `TestProposeScore_OC163_AC2_EndToEnd` and `TestPropose_OC163_Documents` (integration, real documents through the plan and preview reads);
  - `NewFromDesign.test.tsx` › `OC-163 AC3`;
  - `TestProposeHandler_AC4_ReadOnly`;
  - `TestPropose_AC4_Budget` and `BenchmarkPropose_AC4` (`propose_bench_test.go`).
