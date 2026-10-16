# OC-152 — An embedded table opens to show what it references

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E9 Relationship Mapping | P2 | 1 | OC-042, OC-043 | Implemented |

## User story
As a **data engineer** who just embedded a child table, I want the tables it references (its product, its order) shown right under it in Related tables, so that I can embed them next without hunting for a small chevron.

## Background (2026-10-15)
Related tables already held the next hops (order_items ↗ products), but every row started collapsed, including the one just embedded. Users read it as "the embedded object's references aren't offered". OC-142 adds these relationships on the composer's source side; this story is the Canvas's Related tables.

## Scope
**In:**
- A row whose path is embedded starts open, showing the hops after it; other rows start closed. Either can be toggled, and the choice is kept while the panel is open.

**Out:** the composer source side (OC-142).

## Acceptance criteria & unit tests

### AC1 — Open once embedded
**Given** order_items is embedded, **then** "↗ products via product_id" shows under it without clicking; **when** I click "Hide tables related to order_items", **then** it's hidden.

| Test | Location | Verifies |
|---|---|---|
| `RelatedTree.test.tsx` › `an embedded table opens to show what it references…` | `web/src/features/mapping/RelatedTree.test.tsx` | AC1 |
