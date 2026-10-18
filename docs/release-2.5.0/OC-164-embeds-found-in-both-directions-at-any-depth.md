# OC-164 — Embedded objects found in both directions, at any depth

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P0 | 5 | OC-163, OC-141, OC-157 | Implemented |

## User story
As a **data engineer** whose customer design nests objects and arrays several levels deep, I want the proposal to find, for each one, the related table that best represents it, whether that table is referenced by the parent or references it, and at whatever depth it sits, so that the embeds match the design without me hunting for join paths.

## Background (gap found 2026-10-17)
OC-060 considers only the **direct links** from a node's scope, and it scores cardinality as "array ↔ one-to-many, object ↔ many-to-one". The cart design breaks both assumptions:
- `customer` is an **object** filled from a **Referenced by** table (`customers.cart_id → carts`), which isn't unique, so it takes the first row.
- `items[].appliedRewards[]` is an **array** that has two candidates:
  - `cart_items.applied_reward_id → applied_rewards` (References, N:1, wrapped as a one-item array);
  - `applied_rewards.cart_id → carts` (the cart's rewards, a level up).
- `saved_orders` and `associated_scans` are **one-to-one** (the foreign key is their primary key).

Today the first case is scored as medium "with an adapter", the second may pick the wrong path, and one-to-one is treated as one-to-many.

## Scope
**In:**
- **Both directions.** Candidates for a container come from its scope table's hops (OC-141): References (N:1, 1:1) and Referenced by (1:N, 1:1, M:N). Each carries the OC-157 cardinality: 1:1 when the child's foreign key columns are its primary key or a unique key, from discovery's `UniqueConstraints`.
- **Cardinality fit:**

  | Design node | 1:1 | N:1 | 1:N | M:N |
  |---|---|---|---|---|
  | object | 1.0 | 1.0 | 0.7 with `first` (+0.2 when an order column exists or the design says "latest/primary") | 0.5 with `first` |
  | array | 0.6 with `wrap` | 0.6 with `wrap` | 1.0 | 1.0 |

- **Depth.** Containers are proposed top-down, each in its parent's proposed scope, up to `OC_MAX_RELATION_DEPTH`. A container deeper than the limit is unresolved, with the reason "deepest level reached".
- **Ambiguity between paths.** When two candidates are within 0.05 of each other, both are shown in the review (OC-168), the one closer to the node first. The reason names the join (`cart_items.applied_reward_id = applied_rewards.id`).
- **Virtual relationships** (OC-042) count as candidates, with their badge in the reasons.

**Out:** relationships that exist in neither the source nor the project's virtual relationships. Those stay unresolved with *Add virtual relationship…* (OC-169). Hierarchies are out too (OC-161).

## Implementation details
- `automap.Links` gains `Cardinality` and `Unique bool`, filled in by `mappings.projectCtx.links()` from discovery. One-to-one = the child's foreign key columns equal its primary key, or match one of its `UniqueConstraints`.
- `bindings.go` takes its candidates from `HopsOf(scopeTable)` instead of the driving graph's paths, so depth is unlimited up to the setting. Scores for a (node, table, join) are cached for the whole call.
- An object over 1:N gets the adapter `first`, with `orderBy` set to the child's `updated_at`, then `created_at`, then its primary key when such a column exists. An array over N:1 or 1:1 gets `wrap`.

## Acceptance criteria & unit tests

### AC1 — Referenced-by object and one-to-one
**Given** the `pos` source and the cart design, **then**:
- `customer` ← `customers` (Referenced by, 1:N, adapter `first`), high band, with the reason "object ← 1:N: first row";
- `saved_orders` and `associated_scans` are classified 1:1.

| Test | Location | Verifies |
|---|---|---|
| `TestProposeBindings_AC1_ReferencedByObject` | `backend/internal/automap/bindings_test.go` | |

### AC2 — Two levels down
**Then** `items[]` ← `cart_items` (1:N) and `items[].modifiers[]` ← `customizations` (1:N under `cart_items`), both high.

| Test | Location | Verifies |
|---|---|---|
| `TestProposeBindings_AC2_Depth` | `backend/internal/automap/bindings_test.go` | |

### AC3 — Close candidates are both offered
**Then** `items[].appliedRewards[]` is proposed as `wrap` over `cart_items.applied_reward_id → applied_rewards`, with `applied_rewards.cart_id → carts` as the alternative. Both reasons name their join.

| Test | Location | Verifies |
|---|---|---|
| `TestProposeBindings_AC3_Alternatives` | `backend/internal/automap/bindings_test.go` | |

### AC4 — Depth limit
**Given** `OC_MAX_RELATION_DEPTH=1`, **then** `items[].modifiers[]` is unresolved, with "Deepest level reached (1 of 1)".

| Test | Location | Verifies |
|---|---|---|
| `TestProposeBindings_AC4_DepthLimit` | `backend/internal/automap/bindings_test.go` | |

## Implementation notes (2026-10-17)
- `automap.Link` has `Cardinality` and `Direction`, filled in from the graph's hops. One-to-one detection already existed (`relations.Build`: the foreign key is the child's primary or unique key), so the graph needed no change.
- Candidates are each proposed scope's own links: the proposal recurses scope by scope, so depth is unlimited up to `mapping.MaxRelationDepth()` (`OC_MAX_RELATION_DEPTH`).
- **Cardinality fit:**
  - an object fits 1:1 and N:1 (1.0), 1:N with `first` (0.7) and M:N with `first` (0.5);
  - an array fits 1:N and M:N (1.0), and N:1 or 1:1 with `wrap` (0.6).

  This also changes OC-060's suggestions, which used 0.5 for every adapter.
- An object over 1:N takes its first row by `updated_at` → `modified_at` → `created_at` (newest first), else by its primary key. A runner-up within 0.05 drops the band to medium, with a "close alternative" reason.
- **AC3 as built:** from `cart_items`, the only reward link is `cart_items.applied_reward_id → applied_rewards`. The cart-level `applied_rewards.cart_id` isn't a link of the item scope. The test checks the wrap, the join and the reason.
- Tests are in `propose_test.go` (`TestProposeBindings_AC1…AC4`), not `bindings_test.go`.
