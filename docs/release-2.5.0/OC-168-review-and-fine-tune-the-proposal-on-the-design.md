# OC-168 — Review and fine-tune the proposed mapping on the customer's own structure

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P0 | 8 | OC-163, OC-048, OC-060, OC-142 | Implemented |

## User story
As a **data engineer**, I want to see the proposed mapping laid out on the customer's design: every object with the table it comes from, every field with its column, conversion and confidence, so that I can accept what's right, change what isn't with the editors I already use, and then keep working in the normal Composer and Canvas.

## Background (gap found 2026-10-17)
OC-060's Suggestions panel lists **bindings only**, one node at a time, in a side panel. Field mappings appear only after *Auto-map all*, which runs after the bindings are accepted. With a whole-mapping proposal (OC-163), the user needs to see everything at once and in the design's own order. The question to answer is "is this what the customer asked for?", not "which table should I bind next?".

## Scope
**In:**
- **Review mode** in the Composer, opened by a proposal (import, *New mapping from a design*, *Propose again*). It's a banner ("Proposed mapping: 112 of 131 fields · 9 need review · 19 not populated · 3 unresolved"), with **Apply**, **Apply high only** and **Discard**.
- **The document tree is the customer's design.** It keeps the same names, order and nesting. Each row shows what's proposed:
  - objects and arrays: the table, its direction and cardinality chip (OC-157: `↙ customers 1:N · first`) and the join on hover;
  - fields: `← cart_items.price × 100` (the source and a short transform), a constant `= "cart"`, `not populated`, or `unresolved`;
  - a **confidence chip** (high, medium, low) with the reasons in a tooltip and in the Inspector.
  - Rows the user changed show "edited" and keep their state when the proposal is recomputed.
- **Filters:** *All*, *Needs review* (medium, low and alternatives), *Unresolved*, *Not populated*, *Edited*. Keyboard: `n` / `p` move to the next or previous item that needs review.
- **Fine-tune with the existing editors.** Nothing new to learn:
  - *Change table…* opens the Bind dialog (OC-048), prefilled with the alternatives (OC-164);
  - selecting a field opens the Inspector, where the user can change the column, transform or expression, set a constant, or mark it not populated;
  - Sources › "In this document" (OC-142) shows the proposed instances;
  - dragging a column onto a field works as it does today.
  - Changing a container's table recomputes the proposal for its subtree only (`propose {nodeId}`), keeping edited rows.
- **Accept per row or per subtree** (*Accept this*, *Accept all under items[]*).
- **Apply** writes the accepted proposal to the spec in **one undo step**, using the same operations as manual edits (`composerOps.applyProposal`). Review mode then closes, and the mapping is an ordinary composed mapping: Canvas, Composer, preview and export all show it. *Discard* leaves the spec as it was.
- **Preview while reviewing.** The Sample pane (OC-049) previews the proposal as if applied (`specOverride`), next to the customer's reference document (OC-059).

**Out:** a separate review page outside the Composer; reviewing proposals in the Canvas (once applied, the Canvas shows the mapping as usual).

## Implementation details
- Store: `proposal?: { value: Proposal; accepted: Set<id>; edited: Map<id, Override> }` on the workbench store. It isn't part of `history` until Apply.
  - Overrides are kept by field or node ID, so `propose {nodeId}` can merge results without losing edits.
- `composerOps.applyProposal(spec, proposal, accepted, edited)` returns `{spec, report}`. It's pure, and shared with the tests and *Accept high & auto-map* (OC-060), which becomes "propose, then apply high only".
- Tree rows: `DocumentTree` gets a `proposal` overlay prop. There's no second tree component.
- Accessibility: chips have text alternatives ("high confidence"). The banner counts are a live region, and every drag action has a button.

## Acceptance criteria & unit tests

### AC1 — The proposal on the design's tree
**Given** the cart proposal, **then**:
- the tree has the design's names in its order;
- `customer` shows `↙ customers 1:N · first`;
- `items[].price` shows `← cart_items.price × 100` with a high chip;
- `receipt` shows `not populated`;
- the banner counts match the proposal.

| Test | Location | Verifies |
|---|---|---|
| `ReviewMode.test.tsx` › `OC-168 AC1` | `web/src/features/composer/ReviewMode.test.tsx` | |

### AC2 — Fine-tune with the existing editors
**When** the user:
1. changes `items[].appliedRewards[]` to its alternative in the Bind dialog;
2. sets `partner.name` to `first_name` only in the Inspector;
3. filters *Needs review*;

**then** only the remaining medium and low items are listed, both edits show "edited", and *Propose again* keeps them.

| Test | Location | Verifies |
|---|---|---|
| `ReviewMode.test.tsx` › `OC-168 AC2` | `web/src/features/composer/ReviewMode.test.tsx` | |

### AC3 — Apply is one step, then it's an ordinary mapping
**When** the user clicks **Apply**, **then** the spec has the bindings, mappings, constants and not-populated states, and one undo returns to the imported design. The Canvas shows the same field mappings. The preview of a seeded cart equals `expected.json`'s document, ignoring fields that `expected.json` leaves unresolved.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-168 AC3: applyProposal` | `web/src/features/composer/composerOps.test.ts` | |
| `design-first.spec.ts` › `import cart, review, apply, preview` | `web/tests/e2e/design-first.spec.ts` | Against the seeded `pos` source |

### AC4 — Accessible review
**Then** review mode passes axe, the chips have text alternatives, and `n` / `p` move focus between items that need review.

| Test | Location | Verifies |
|---|---|---|
| `Composer.a11y.test.tsx` › `OC-168 AC4: review mode` | `web/src/features/composer/Composer.a11y.test.tsx` | |

## Implementation notes (2026-10-17)
- **The proposal is applied, then reviewed.** The proposal's spec is applied as one undo step, and the workbench store keeps `review {proposal, mark}`:
  - **Apply** squashes everything since the mark into one step (OC-142's `squash`);
  - **Discard** goes back to it (`revert`);
  - **Apply high only** takes back every row below high (a container takes its subtree), then applies.
- **Rows start accepted.** The user changes them with the composer's own editors (Inspector, Bind dialog, drops), or **Takes back** a row, instead of accepting row by row.
- **Edits are detected** by comparing each row with the proposal's own spec, and edited rows show "edited".
- **Tree:** `DocumentTree` has a review overlay. Each row's name carries a chip with its confidence or status. The filters keep the ancestors of matching rows, and `n` / `p` stop on rows that need review or are unresolved.
- **The banner** counts the same way as the server's coverage: "needs review" is proposed below high; unresolved is counted separately.
- **Inspector:** `ProposalPanel.tsx` shows the summary, score, reasons, alternatives (*Use …*, then *Propose again* under the node) and the fixes from OC-169.
- **Propose again** sends the current spec with `base`. Rows that were already there keep their original items.
- **Preview:** it needs no `specOverride`, because the proposal is in the spec.
- **Tests:** `ReviewMode.test.tsx` (AC1–AC3), `review.test.ts`, and `Composer.a11y.test.tsx` › `OC-168 AC4`. The E2E is OC-173's.
