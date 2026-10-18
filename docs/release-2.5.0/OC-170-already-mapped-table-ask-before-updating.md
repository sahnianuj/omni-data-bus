# OC-170 — The driving table is already mapped: ask before updating that mapping

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P0 | 5 | OC-163, OC-061, OC-153 | Implemented |

## User story
As a **data engineer**, when the design I import is proposed to come from a table that already has a mapping in the project, I want to be asked whether to update that mapping, with what would change, or to create a separate one, so that I never overwrite someone's mapping by accident and never end up with two mappings writing the same documents without knowing.

## Background (gap found 2026-10-17)
*New mapping from a design* (OC-163) picks the driving table itself. In the customer project, `carts` may already have a mapping, for example one started on the Canvas from the table. Creating a second one silently means:
- two jobs write `cart::{id}` keys into the same collection, and the last one wins;
- the user's earlier work isn't reused.

Updating the existing one silently would replace its design. Today nothing checks for this.

## Scope
**In:**
- **Detection.** After the proposal picks its driving table, or when the user changes it, the client lists the project's mappings driven by that table: the same `schema.table` as `spec.root.table`, or as `sourceTable` for table mappings.
- **The modal** "*pos.carts* is already mapped", for each matching mapping:
  - its name, mode (table or composed), target collection, key template, status (draft or ready), fields mapped, last edited by and when, and the last job;
  - **Update this mapping** (default when there's exactly one): opens review mode (OC-168) **on that mapping**. The proposal is computed with the existing spec as the starting point:
    - fields already mapped keep their mappings unless the user accepts a change;
    - the design is reconciled with OC-061's `Reconcile`, keeping IDs and showing a diff (added, removed, type changed, renamed, moved);
    - nothing is saved until **Apply**, which saves one new version using `If-Match` (OC-153), and conflicts show the usual conflict banner;
  - **Create a new mapping**: continues as OC-163. If the new mapping would write to the same collection with an overlapping key template, the modal warns ("Both mappings write `cart::{id}` keys into `orders.carts` — the last job to run wins"). The user can change the collection or key before creating;
  - **Cancel**: nothing is created or changed.
- **Composer import on an existing mapping.** *Import JSON design…* on a mapping that already has mappings or bindings asks the same question in the same modal: *Update this mapping* (reconcile and review) or *Replace the design* (today's confirm-and-clear, OC-059).
- **Permissions.** Only editors see *Update this mapping*. Viewers see the existing mapping's details and *Create a new mapping* is disabled, as everywhere else (`Can role="editor"`).

**Out:** merging two existing mappings; locking a mapping while someone reviews it (OC-153's version check covers concurrent edits).

## Implementation details
- `GET /projects/{pid}/mappings?drivingTable=schema.table` adds a filter to the existing list. It returns the summary fields the modal shows, including `lastJob`.
- `propose {base: "current"}`: `automap.Propose` takes the existing spec. Existing field mappings get the score 1.0 with the reason "already mapped". Proposals that differ appear as *changes* in review mode, never applied by default.
- Overlap check: `mapping.KeyOverlap(a, b) bool` holds when the collections are the same and the templates have the same literal prefix and the same placeholder columns. It's also used by the job start check, as a warning.

## Acceptance criteria & unit tests

### AC1 — Asked, with the existing mapping's details
**Given** a project with a ready mapping "Carts" driven by `pos.carts`, **when** the user creates a mapping from `cart.schema.json`, **then** the modal names "Carts", its collection, key, 34 mapped fields, its last editor and its last job. *Update this mapping* is the default.

| Test | Location | Verifies |
|---|---|---|
| `NewFromDesign.test.tsx` › `OC-170 AC1` | `web/src/features/mapping/NewFromDesign.test.tsx` | |

### AC2 — Update keeps what's mapped and shows the changes
**When** they choose *Update this mapping*, **then** review mode opens on "Carts":
- its 34 existing mappings show "already mapped";
- the design diff lists the added fields;
- after **Apply**, one new version is saved and the existing mappings are unchanged.

A concurrent edit makes Apply show the conflict banner and saves nothing.

| Test | Location | Verifies |
|---|---|---|
| `TestPropose_OC170_AC2_BaseCurrent` | `backend/internal/automap/propose_test.go` | |
| `ReviewMode.test.tsx` › `OC-170 AC2` | `web/src/features/composer/ReviewMode.test.tsx` | |

### AC3 — New mapping warns about overlapping keys
**When** they choose *Create a new mapping* with the same collection and `cart::{id}`, **then** the overlap warning shows. Changing the collection clears it.

| Test | Location | Verifies |
|---|---|---|
| `TestKeyOverlap_OC170_AC3` | `backend/internal/mapping/key_test.go` | |
| `NewFromDesign.test.tsx` › `OC-170 AC3` | `web/src/features/mapping/NewFromDesign.test.tsx` | |

### AC4 — Cancel changes nothing; viewers can't update
**When** they choose *Cancel*, **then** no mapping is created and no PUT is sent. **Given** a viewer, **then** *Update this mapping* isn't shown.

| Test | Location | Verifies |
|---|---|---|
| `NewFromDesign.test.tsx` › `OC-170 AC4` | `web/src/features/mapping/NewFromDesign.test.tsx` | |

## Implementation notes (2026-10-17)
- **Mapping summaries** have `keyTemplate` and `createdBy`. The dialog finds the mappings driven by the chosen table, or on it, from the project's mapping list, so no new query parameter was needed.
- **The store allows one mapping per source table per project** (since 1.0). So when the table has its own mapping, *Create a new mapping* is disabled, and the dialog explains why. When the table only drives another composed mapping, creating is allowed, with the overlap warning.
- **Update this mapping** stashes the design (`sessionStorage`) and opens that mapping with `?import=pending`. The import opens with the design and *Propose* on, and its **Update** is the existing reconcile step (OC-061): it shows the design diff and keeps IDs and mappings. Then the proposal runs with `base`, which keeps every existing binding and mapping ("already mapped") and proposes only what's missing.
- **Saving** goes through the usual If-Match autosave (OC-153), so a concurrent edit shows the conflict banner.
- **Key overlap:**
  - `mapping.KeyOverlap(a, b)`: the same literal prefix and the same columns;
  - the job pre-flight warns `KEY_OVERLAP` for any two mappings of a run with the same location and overlapping keys;
  - the dialog uses the same rule on the proposal's key.
- **Not shown:** the existing mapping's last job (not in the list summary).
- **Tests:** `NewFromDesign.test.tsx` (AC1, AC3, AC4), `TestPropose_OC170_AC2_BaseCurrent`, `TestKeyOverlap_OC170_AC3`, `TestPreflight_OC170_KeyOverlap`.
