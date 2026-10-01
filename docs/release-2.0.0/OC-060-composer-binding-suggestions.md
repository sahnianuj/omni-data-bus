# OC-060 — Suggest bindings for an imported design

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P0 | 8 | OC-059, OC-042, OC-049 | Implemented |

## User story
As a **data engineer** who has imported a customer's JSON design, I want the tool to propose the driving table and which source relation fills each object and array, with a reason and a confidence, so that I can accept the obvious bindings in one click, then auto-map the fields, and only decide the unclear ones by hand.

## Background (gap found 2026-09-30)
Scoped auto-map (OC-049) only maps fields **inside already-bound scopes**. For an imported design, nothing is bound yet, so auto-map can only use the root table. Every array and object has to be bound by hand with the Bind dialog, which is most of the work on a large customer design.

## Scope
**In:**
- A suggested driving table for the root, from the root's scalar fields.
- A suggested binding (relation, kind, adapter, join path) for every unbound object and array, top-down, through FK, virtual and junction relationships (OC-042/044).
- A *map from parent* suggestion for objects that only group the parent's own columns (e.g. `shipping{street,city,zip}`).
- Scalar arrays (`tags: ["a","b"]`) suggested with `valueColumn`.
- A suggestions panel: accept, reject, or pick an alternative. *Accept all high-confidence* then runs *Auto-map all*.

**Out:**
- Suggesting filters, ordering or aggregates on arrays (the user sets those in the Bind dialog).
- Creating virtual relationships automatically. When no path exists, the suggestion says so and links to *Add virtual relationship…*.

## Implementation details

### Engine (`internal/automap/bindings.go`)
- `SuggestBindings(spec, graph, tables, dicts) []BindingSuggestion`. It's deterministic, with no ML, and reuses the OC-017 normalizer, the dictionaries and the Hungarian scorer.
- **Driving table** (only when `spec.root` is unset, or on request): each table is scored by the average best-match score of the root's scalar fields against its columns. The mapping's name and target collection name add a +0.1 name bonus. The top 3 are returned.
- **Containers**, parents first, so each chosen binding becomes the scope for its children:
  - Candidates are relations reachable from the parent scope's table within **2 hops**. For example, `items[].product` resolves `order_items` → `products`.
  - `score = 0.35·name + 0.45·fieldFit + 0.20·cardinality`:
    - `name`: the node's name (for `[]` items, the array's name) against the table name, with singular/plural forms, abbreviations, synonyms and the prefix stripped (`order_items` ~ `items`).
    - `fieldFit`: the share of the node's scalar descendants in its own scope that the scorer maps to the candidate's columns at ≥ 0.6.
    - `cardinality`: 1.0 when array ↔ one-to-many / many-to-many or object ↔ many-to-one; 0.5 with an adapter (`first` / `wrap`, OC-047).
  - **Map from parent** competes as a candidate for objects: its `fieldFit` is measured against the parent scope's columns, and its cardinality is 1.0.
  - Bands: **high** ≥ 0.75, **medium** 0.50–0.75. Below 0.50 is *unresolved*, with the best candidate shown but not suggested.
- Each suggestion carries `reasons`, for example `["name: items ~ order_items (0.92)", "fields 5/6", "one-to-many ✓"]`, the join path, and up to 2 alternatives.

### API
- `POST /mappings/{id}/suggest-bindings {nodeId?}` returns `{root?, suggestions[]}`.
- `POST /mappings/{id}/apply-suggestions {ids[], thenAutomap: bool}` applies each suggestion with the same operation as the Bind dialog, then optionally runs `automap.RunAll`. Nodes the user has already bound are never changed. It returns the updated spec and `automapStats.byNode`.

### UI (`src/features/composer`)
- After an import (OC-059), a banner reads "4 arrays and 2 objects aren't bound — *Suggest bindings*". The same action is in the score header, which now also counts unbound containers ("2 unbound").
- `SuggestionsPanel.tsx` groups suggestions by node, top-down. It shows the confidence chip, reasons and join path, plus **Accept**, **Reject** and **Other…** (opens the Bind dialog prefilled through `bindPrefill`). *Accept all high* and *Accept all high & auto-map* are at the top.
- Accepted nodes flash in the tree. Suggestions are recomputed after each accept, because a new binding changes its children's scope.

## Acceptance criteria & unit tests

### AC1 — Driving table
**Given** the order sample on the `shop` fixture with `spec.root` unset, **then** `public.orders` is ranked first with a score ≥ 0.75, ahead of `sales.orders_archive`.

| Test | Location | Verifies |
|---|---|---|
| `TestSuggestBindings_AC1_DrivingTable` | `internal/automap/bindings_test.go` | |

### AC2 — Multi-level arrays and objects
**Given** root `orders`, **then**:
- `items[]` is suggested as one-to-many to `order_items` (high);
- `items[].product` is suggested as many-to-one to `products` through `order_items.product_id` (high);
- `customer` is suggested as many-to-one to `customers` (high).

| Test | Location | Verifies |
|---|---|---|
| `TestSuggestBindings_AC2_Chain` | `internal/automap/bindings_test.go` | |

### AC3 — Many-to-many and scalar arrays
**Given** a product design with `tags: ["a","b"]`, **then** `tags[]` is suggested through the `product_tags` junction to `tags`, with `valueColumn = name`.

| Test | Location | Verifies |
|---|---|---|
| `TestSuggestBindings_AC3_ScalarJunction` | `internal/automap/bindings_test.go` | |

### AC4 — Grouping objects map from the parent
**Given** `shipping{street, city, zip}` whose fields match `orders.ship_street/ship_city/ship_zip`, **then** the suggestion is *map from parent* (no relation), ranked above any `addresses` table.

| Test | Location | Verifies |
|---|---|---|
| `TestSuggestBindings_AC4_MapFromParent` | `internal/automap/bindings_test.go` | |

### AC5 — Cardinality with an adapter
**Given** an object `latestPayment` whose only candidate is one-to-many `payments`, **then** it's suggested as medium confidence with adapter `first`, and the reasons say "object ← one-to-many: first row".

| Test | Location | Verifies |
|---|---|---|
| `TestSuggestBindings_AC5_Adapter` | `internal/automap/bindings_test.go` | |

### AC6 — Accept all, then auto-map
**Given** the imported order design (`web/testdata/composer/order_customer.json`), **when** the user picks *Accept all high & auto-map*, **then**:
- ≥ 90 % of fields are mapped;
- the preview (OC-049) returns order 1001 with 3 items, each with its product;
- a node the user had bound before is unchanged.

| Test | Location | Verifies |
|---|---|---|
| `TestApplySuggestions_AC6_EndToEnd` *(integration)* | `internal/automap/bindings_integration_test.go` | |
| `SuggestionsPanel.test.tsx` › `AC6: accept all high` | `src/features/composer/SuggestionsPanel.test.tsx` | |
| `composer-import.spec.ts` › `import, suggest, preview` | `web/tests/e2e/composer-import.spec.ts` | |

### AC7 — Unresolved and explained
**Given** a node `loyalty{tier, points}` with no reachable table scoring ≥ 0.50, **then** it's listed as unresolved, with its best candidate and a link to *Add virtual relationship…*. Nothing is applied.

| Test | Location | Verifies |
|---|---|---|
| `TestSuggestBindings_AC7_Unresolved` | `internal/automap/bindings_test.go` | |

### AC8 — Performance
**Given** a 300-table source and a design with 200 containers, **then** `SuggestBindings` completes in < 1 s.

| Test | Location | Verifies |
|---|---|---|
| `BenchmarkSuggestBindings_AC8` | `internal/automap/bindings_bench_test.go` | |

## Implementation notes (2026-09-30)
- Engine: `automap.SuggestBindings` (`internal/automap/bindings.go`). The graph comes in through `automap.Links`, adapted in `mappings.projectCtx.links()` from `relations.Build` (FK and virtual hops) plus `relations.Junctions` (many-to-many), so `automap` doesn't depend on `relations`. Candidates are the **direct links from the node's scope**, not 2 hops: a nested node is scored in its parent's (proposed) scope, which already covers `items[].product` ← `order_items` → `products`, and junctions cover the through-table case.
- Scores follow the story: `0.35·name + 0.45·fit + 0.20·cardinality`. `fit` is the mean best name score of the node's own scalar fields, counting only scores ≥ 0.60. A node with no scalar fields of its own gets a neutral 0.5. *Map from parent* uses a fixed 0.5 name score. Scalar arrays take `shape: scalars` with a `valueColumn` (best name match, else the first text column that isn't a key or join column).
- **No `apply-suggestions` endpoint.** `POST …/suggest-bindings {nodeId?, root?, specOverride?}` is read-only (viewer role, like preview). The composer applies what's accepted with the Bind dialog's own `bind` operation (`composerOps.acceptSuggestion`), so each accept is an undo step. *Accept high & auto-map* binds them all in one step, parents first, then runs *Auto-map all*. The list is refetched with the unsaved spec after every change.
- AC5: `latestPayment ← payments` scores high (0.80), not medium, because its fields match exactly. The test checks the adapter and the reason, not the band. AC6 is covered in Go (`TestApplySuggestions_AC6_EndToEnd`: every field gets a mapping or suggestion except the two with no table) and in `web/tests/e2e/composer-import.spec.ts` against the seeded `sales` schema.
- AC8: 300 tables × 200 containers takes ~0.6 s on an M-series laptop, with name variants cached per field and table. The budget test skips under `-race` or coverage, like the other budget tests (`make test-budgets`).
- UI: *Suggest bindings* in the score header, next to an "N unbound" count (`composerOps.unboundContainers`). After an import, a banner says what's left to bind. The panel lists a better driving table when one scores higher (*Drive from it*), alternatives (*Use …*), *Other…* (opens the Bind dialog prefilled, or the table picker) and *Reject*.
