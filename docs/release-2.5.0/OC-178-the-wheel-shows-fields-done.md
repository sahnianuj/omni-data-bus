# OC-178 — The wheel shows how much of the document is done

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 Mapping Canvas | P1 | 3 | OC-017, OC-018, OC-166 | Implemented |

## User story
As a **data engineer** finishing a mapping, I want the score wheel to show how many of the target document's fields are done (filled, or deliberately left empty), the same number on the Mappings page and in the mapping, so that I know how much is left, and every field I map or mark *not populated* moves the wheel.

## Background (gap found 2026-10-17)
The wheel shows a different number on each page, and neither is about the document:
- **On the Mappings page**, the wheel is `coverageRatio`: the share of the driving table's *source columns* that are mapped. On the `sccs` card it read 83 %, next to *83 % auto-mapped*.
- **In the mapping**, the wheel is `automapStats.autoMapRatio`: the share of source columns the *last auto-map run* mapped by itself. It's frozen at that run, so on the same `sccs` mapping it read 100 %, next to *83 % coverage* and *1 required field unmapped*.

Users build the customer's document; whether the tool or they mapped a column, and whether every source column is used, isn't what they're after. The question is how many of the document's fields are still empty. The current target counts can't answer it either:
- `mappedTargetLeaves` / `totalTargetLeaves` leave out fields inside arrays of objects (`items[].price`) and variants.
- A field filled by a total (OC-134) or by a relation (a scalar array) doesn't count as mapped.

## Scope
**In:**
- **One measure: fields done.**
  - **What counts as a field:** every field of the design that holds a value: a scalar, an array of scalars, or an empty object or array. Fields inside arrays' items and inside variants count; objects and arrays of objects are containers, counted through their fields.
  - **A field is done when** it is:
    - filled by an active mapping (a column, a constant, an expression or a computed value);
    - filled by a total or a relation;
    - marked **not populated**.

    A field under a container that's marked not populated is done too.
  - **Not done:** pending suggestions and empty fields.
- **New stats:** `targetFields`, `targetFieldsDone` and `doneRatio`. The server and the client compute them identically (the shared `stats-cases.json`); the existing fields don't change.
- **The Mappings page card:** the wheel shows `doneRatio`, with the text *38 of 40 fields done* in place of *64 % auto-mapped*.
- **The mapping's header:**
  - the wheel shows the live `doneRatio`;
  - the headline reads *38 of 40 target fields done* (*All 40 target fields done* when finished);
  - while fields are left: *2 to go: map them, or mark them not populated*, with **Show them**, which applies the *Unmapped* filter.

  The source-column bar and its legend stay below as detail. The auto-map snapshot moves into it (*auto-mapped 7 of 7 columns*).
- **Mark not populated where you map.** The Canvas Inspector and the Composer Inspector offer **Mark not populated** on a field with no mapping (OC-166's `setNotPopulated`). Until now, only review mode offered it.

**Out:** changing *Ready / n to fix* (issues); weighting required fields; counting source columns into the wheel.

## Implementation details
- **Backend:** `mapping/stats.go` › `fieldsDone(s)`. It walks the design (`Walk`), skipping the items node and marking done any descendant of a not-populated container. `Stats.TargetFields`, `TargetFieldsDone` and `DoneRatio` are added to the OpenAPI `MappingStats` schema.
- **Web:**
  - `computeStats.ts` gains the same `fieldsDone`.
  - The ring's caller passes `doneRatio`: in `MappingsStep.tsx` and in `MappingScoreHeader.tsx`, which reads live stats instead of `automap`.
  - `Inspector.tsx` and `ComposerInspector.tsx` gain **Mark not populated**.
- The parity fixture gains a case with array items, a variant, a total, a scalar array filled by its relation, a pending suggestion and a not-populated object.

## Acceptance criteria & unit tests

### AC1 — Fields done, the same on server and client
**Given** the new parity case, **then** `targetFields`, `targetFieldsDone` and `doneRatio` are as expected on both. Array items, the variant's fields, the total and the relation-filled array count; the pending suggestion doesn't; the not-populated object's fields do.

| Test | Location | Verifies |
|---|---|---|
| `TestComputeStats_OC178_FieldsDone` | `backend/internal/mapping/stats_test.go` | 7 of 10; marking the rest not populated makes 100 % |
| `computeStats.test.ts` › `AC3: parity with server fixtures` | `web/src/features/mapping/computeStats.test.ts` | Same fixture |

### AC2 — Both wheels show it
**Given** a mapping with 38 of 40 fields done, **then** its card's wheel reads 95 % with *38 of 40 fields done*, and so does the mapping header's wheel, with *2 to go* and **Show them**. Mapping a field moves the header's wheel at once.

| Test | Location | Verifies |
|---|---|---|
| `MappingsStep.test.tsx` › `OC-178 AC2` | `web/src/features/mapping/MappingsStep.test.tsx` | Ring and *38 of 40 fields done*; no *auto-mapped* |
| `MappingScoreHeader.test.tsx` › `OC-178 AC2` | `web/src/features/mapping/MappingScoreHeader.test.tsx` | Live ratio; *Show them* sets the filter |

### AC3 — Mark not populated from the Canvas and the Composer
**Given** a field with no mapping selected in the Canvas or the Composer, **when** the user clicks **Mark not populated**, **then** the field counts as done and the wheel moves. **Undo** puts it back.

| Test | Location | Verifies |
|---|---|---|
| `Inspector.test.tsx` › `OC-178 AC3` | `web/src/features/mapping/Inspector.test.tsx` | Mark, *Map it instead*; not offered on a mapped field |
| `Inspector.test.tsx` › `OC-178 AC3` | `web/src/features/composer/Inspector.test.tsx` | Mark, undo; not offered on a mapped field |

## Implementation notes (2026-10-17)
- **Server and client:** `fieldsDone` in `stats.go` and `computeStats.ts`. The parity fixture's cases gained the three new values, plus the OC-178 case: 10 fields, 7 done. On that design, the old `mappedTargetLeaves` / `totalTargetLeaves` read 1 of 7.
- **`MappingStats`:** `targetFields`, `targetFieldsDone` and `doneRatio` are required in the OpenAPI schema (the server always sends them), and the client types are regenerated.
- **Header:**
  - The wheel and headline use the live stats. *Show them* applies the *Unmapped* filter, which also shows the unmapped source columns.
  - The auto-map snapshot is a caption over the source-column bar: *Source columns · auto-mapped 7 of 7 on the last run*.
  - *Required field unmapped*, the column coverage and the average confidence stay on the line under the bar.
- **Card:** *n of m fields done* replaces *n % auto-mapped*. *Ready / n to fix* is unchanged.
- **Mark not populated:**
  - **Canvas Inspector:** on a field with no mapping and no total. A field already marked shows *Not populated → field* with the OC-166 note and **Map it instead**.
  - **Composer Inspector:** on a field with no mapping, total or binding, for editors.
- **Restart the dev API to see it:** cards are built from the server's stats, so they show the new numbers only after a restart.
