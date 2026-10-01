# OC-076 — Reference box lists differences only when the mapping is strict

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 1 | OC-061, OC-071, OC-075 | Implemented |

## User story
As a **data engineer** mapping into a customer's own JSON design, I want the sample's Reference box to show the customer's document without a list of warnings about how my sample differs from it, unless I asked for the mapping to match the design exactly, so that fields I chose not to fill (or values the tool encodes differently on purpose) don't nag me.

## Background (2026-10-01)
After OC-075 the Problems panel was quiet, but the Reference box still said, in warning colors: *"This sample differs from the customer's document: products[].productId is in the customer's document but not generated. [Add productId back (from product_id)] · products[].product.preciseCost has another type than in the customer's document."* `preciseCost` comes from a wide `numeric` column, which the tool writes as a string so no digits are lost (`encode.NumericMode` auto), while the customer's sample has a number.

## Scope
**In:**
- The Reference box shows the difference list (missing, extra, type differences, with their fix buttons) only when `referenceStrict` is on. Otherwise it shows the customer's document only.
- The preview still returns `conformance` per item; the Canvas validation panel still lists the info issues with their fixes (OC-070, OC-075).

**Out:** making the numeric encoding (`numericMode`) choosable in the UI, so a wide numeric can be written as a JSON number on purpose (possible future story).

## Acceptance criteria & unit tests

### AC1 — Quiet unless strict
**Given** a sample with differences from the imported design, **when** the mapping isn't strict, **then** the Reference box shows the customer's document and no difference list. **When** it is strict, **then** the list and its fixes are shown (OC-061 AC5, OC-071 AC3).

| Test | Location | Verifies |
|---|---|---|
| `SamplePane.test.tsx` › `OC-076 AC1: lists no differences unless strict` | `web/src/features/composer/SamplePane.test.tsx` | |
| `SamplePane.test.tsx` › `OC-061 AC5: shows the document's conformance beside the reference` (now strict) | same | |
