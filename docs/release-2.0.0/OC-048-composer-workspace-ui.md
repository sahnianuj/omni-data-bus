# OC-048 — Document composer workspace (UI)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P0 | 13 | OC-047, OC-042, OC-004 | Implemented |

## User story
As a **data engineer**, I want a workspace where I sketch my JSON document as a tree, drag source tables onto objects and arrays to define 1:1 and 1:M associations, and drag columns onto fields, while seeing a live sample document, so that I can build a custom document structure without writing code.

## Scope
**In:**
- A third design mode in the Design & Map step: **Composer**, next to Designer and Canvas. A mapping can switch between Canvas and Composer (same model, OC-047).
- Starting points: blank, paste a JSON sample (fields and types inferred, OC-015), from generated, or open an existing mapping.
- Three panes: **Sources** (relationship tree, OC-042), **Document** (editable tree), **Inspector** (binding or field settings), plus a collapsible **Sample** pane.
- Drag and drop plus a full keyboard alternative, meeting WCAG 2.1 AA.
- Autosave with optimistic concurrency, as in 1.0.

**Out:** multi-user live co-editing (last write wins on 412 with a merge prompt, as in 1.0); theming the document tree.

## Implementation details

### Layout (`src/features/composer/`)
```
┌ Sources ───────────────┬ Document ─────────────────────────┬ Inspector ─────────────┐
│ ● orders (driving)     │ { }  $                            │ items[]                 │
│   ↙ order_items  1.2M  │   #  orderId        ← order_id    │ Bound to: order_items   │
│      ↗ products        │   T  customerName   ← customers.… │ Kind: one-to-many       │
│   ↗ customers          │   [ ] items  ⛓ order_items        │ Join: order_id=order_id │
│   ↔ tags via order_tags│     { }                           │ Order by: line_no ↑     │
│ + Virtual relationship │       #  quantity   ← quantity    │ Limit: 1000  Filter: —  │
│                        │       { } product ⛓ products      │ Shape: objects          │
│                        │           T sku     ← sku         │ Overflow: truncate      │
└────────────────────────┴───────────────────────────────────┴─────────────────────────┘
 Sample ▾  order 1001 ◂ ▸   { "orderId": 1001, "items": [ { "quantity": 2, … } ] }
```

### Document tree editing
- Add object, array, or field nodes (context menu, toolbar, or `Enter` for a sibling and `Tab` for a child). Rename inline, choose a type, reorder by drag or `Alt+↑/↓`, delete with undo (`Ctrl+Z`, 50-step history in the Zustand store).
- Paste a JSON sample onto any node to create its sub-tree.
- Node badges: ⛓ bound relation, ← mapped source, ⚠ validation issue (from OC-049).

### Defining associations (the 1:M builder)
- **Drag a table from Sources onto an array node** → the Bind dialog opens, pre-filled from the graph path between the node's current scope and that table:
  - kind and join (auto from FK or virtual relationship; editable column pairs for manual joins),
  - for a junction path, a "many-to-many via order_items" option with payload columns,
  - order-by, limit, filter, shape, overflow (OC-045/046),
  - *Generate item fields* and *Auto-map* checkboxes (auto-map scoped to this node, OC-049).
- **Drag a table onto an object node** → many-to-one binding, or `first` adapter when the path is one-to-many (with an explanation).
- **Drag a table onto the root, or a field, with "values only"** → a lookup-only relation.
- If there is no path from the node's scope to the table, the dialog offers **Define relationship…**, which opens the virtual relationship editor (OC-042) with both tables pre-selected.
- The Sources tree highlights, while a node is selected, which tables are in scope (solid), reachable (outlined), or unreachable (dimmed).

### Mapping fields
- Drag a column onto a field, or select a field and pick from the inspector's *Source* combobox, which lists **in-scope columns only** (OC-047 scope function), grouped by relation.
- The inspector shows transforms (OC-020), null handling and the expression editor with in-scope autocompletion.

### Sample pane
- Calls the preview API (OC-049) debounced at 800 ms after edits. Navigate between sample root rows. Clicking a value in the sample selects its field in the tree.

### Accessibility & keyboard
- The document tree is an ARIA `tree`. Every drag action has a menu equivalent (*Bind to table…*, *Map from column…*).
- Below 1024 px, the three panes collapse into tabs.

## Acceptance criteria & unit tests

### AC1 — Build a document from blank
**Given** a blank composer, **when** the user adds `orderId`, an array `items` and an object `items[].product`, binds `items` to `order_items` by drag, binds `product` to `products`, and maps fields, **then** the saved spec equals `testdata/composer/order_from_blank.json`.

| Test | Location | Verifies |
|---|---|---|
| `Composer.test.tsx` › `AC1: builds order document from blank` | `src/features/composer/Composer.test.tsx` | |
| `composer-order.spec.ts` *(E2E)* | `web/tests/e2e/composer-order.spec.ts` | Real backend, sample shows the items |

### AC2 — Paste a JSON sample
**Given** the user pastes `{"orderId":1,"items":[{"sku":"A","qty":2}]}`, **then** the tree has `orderId: integer`, `items: array<object{sku: string, qty: integer}>`, all unbound and unmapped.

| Test | Location | Verifies |
|---|---|---|
| `Composer.test.tsx` › `AC2: infers tree from pasted JSON` | `src/features/composer/Composer.test.tsx` | |

### AC3 — Bind dialog pre-fill
**Given** `items` selected under `orders`, **when** `products` is dropped on it, **then** the dialog offers "many-to-many via order_items" with payload columns. **When** `customers` is dropped on an object, **then** many-to-one. **Given** a table with no path, **then** *Define relationship…* is offered.

| Test | Location | Verifies |
|---|---|---|
| `BindDialog.test.tsx` › `AC3: prefill by path kind` | `src/features/composer/BindDialog.test.tsx` | |

### AC4 — Scope-filtered column picker
**Given** `items[].sku` selected, **then** the Source combobox lists columns of `orders`, `order_items` and `products` (bound on the path) and not `tags` (bound on a sibling).

| Test | Location | Verifies |
|---|---|---|
| `Inspector.test.tsx` › `AC4: lists in-scope columns only` | `src/features/composer/Inspector.test.tsx` | |

### AC5 — Keyboard-only operation
**Given** keyboard only, **then** the user can add nodes, bind a table (menu), map a column and save; axe reports no violations.

| Test | Location | Verifies |
|---|---|---|
| `Composer.a11y.test.tsx` › `AC5: keyboard flow and axe` | `src/features/composer/Composer.a11y.test.tsx` | |

### AC6 — Canvas ↔ Composer round trip
**Given** a canvas mapping with nested relations, **when** opened in Composer, edited and reopened in Canvas, **then** no relation, mapping or transform is lost.

| Test | Location | Verifies |
|---|---|---|
| `modeSwitch.test.ts` › `AC6: round trip preserves spec` | `src/features/composer/modeSwitch.test.ts` | |

### AC7 — Autosave & undo
**Given** an edit, **then** it is saved after 800 ms with "Saved · just now". **When** the user presses `Ctrl+Z`, **then** the last edit is undone and saved. **Given** a 412, **then** the 1.0 conflict prompt is shown.

| Test | Location | Verifies |
|---|---|---|
| `composerStore.test.ts` › `AC7: undo history and autosave` | `src/features/composer/composerStore.test.ts` | |

## Implementation notes (2026-09-30)
- The composer is a fourth view of the mapping workbench (**Canvas · Composer · Table · Design**) on the same store, so undo (100 steps; ≥ 50 required), 800 ms autosave with If-Match and the 412 conflict banner are shared. Choosing Composer sets `spec.mode: composed` (and `root` to the mapping's own table); choosing Canvas sets it back unless the mapping is driven by another table, which is refused with a message. A composed mapping opens in the composer.
- Panes: Sources (the driving table's relationship tree from the OC-042 graph, junctions, then every other table; bold / outlined / muted by reach from the selected node), Document (ARIA tree), Inspector, a Problems panel under the tree (OC-049) and the Sample pane. Below 1024 px the panes are tabs.
- Keyboard: arrows, Home/End, Enter (sibling field), Tab (child field), Alt+↑/↓ (reorder), F2 (rename), Delete, Shift+F10 or the Menu key (node actions, arrow-navigable). Every drag has a menu equivalent: *Bind to table…* (table picker → Bind dialog), *Look up values from a table…* (lookup-only relation), *Map from column…* (focuses the inspector's Source picker), *Paste JSON sample…*.
- The Bind dialog pre-fills from the graph edges leaving the node's scope table; junction candidates apply when that table is the graph's root (the graph's junctions are root-relative). Join columns are editable; *Define relationship…* opens the virtual relationship editor with both tables selected. *Generate item fields* adds a field per column (a one-to-many's join columns left out); *Auto-map* runs the OC-049 scoped auto-map after saving.
- Starting points: Blank, JSON sample (types inferred client-side: integer vs number, nested objects and arrays), Generated from table (then a root auto-map), or the existing mapping.
- AC1's golden is `web/testdata/composer/order_from_blank.json` (ids normalized); the backend compiles the same file (`TestCompile_ComposerGolden`), so the UI and the server agree on the model. E2E: `web/tests/e2e/composer-order.spec.ts` (drag-and-drop bindings against the built image); the a11y E2E now also checks the Composer view in both themes.
- The workbench's save badge now reads "Saved · just now" (was "Saved"), matching the project wizard and AC7. Small grey (`text-subtle`) labels failed color contrast in a real browser, so the composer and the canvas's *Virtual relationship* button use `text-muted`.
