# OC-019 — Visual mapping workbench (drag-and-drop canvas & table view)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E4 Design & Map | P0 | 13 | OC-015, OC-017, OC-018 | Implemented |

## User story
As a **data engineer**, I want to see source columns and my target document side by side and drag lines between them to map whatever the auto-mapper didn't, so that finishing the mapping is quick, visual, and obvious.
As a **keyboard or screen-reader user** (or someone mapping hundreds of fields), I want an equivalent table view, so that I can do the same thing efficiently without a pointer.

## Implementation details

### Workbench layout (`/projects/{p}/mappings/{mid}`, Workbench pattern)
```
┌ Project › Mappings › public.customer → shop.sales.customer        Saved · 2s ago   [Preview ▸] [Validate] ┐
├ Score header (OC-018) ────────────────────────────────────────────────────────────────────────────────────┤
│ [Canvas | Table]   Filter: [All ▾]  🔍 find field…     Legend: ● auto ◐ suggested ● manual ○ unmapped     │
├───────────── Source: customer (23) ──────┬─────────── edges ───────────┬──── Target document ────────────┤
│ 🔑 customer_id      int4           ●─────┼──────────────────────────────┼──●  customerId      integer  *  │
│    first_name       text           ●─────┼──────────────────────────────┼──●  ▾ name                       │
│    last_name        text           ●─────┼───────────╮      ⓕ trim      │  ●     first       string       │
│    email_address    citext         ◐┄┄┄┄┄┼┄┄┄┄┄┄┄┄┄┄┄┄┼┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┼┄┄◐     last        string       │
│    loyalty_tier     text           ○     │           ╰──────────────────┼──●  email           string       │
│    created_on       timestamptz    ●─────┼─────────── ⓕ toEpochMs ──────┼──●  createdAt       integer      │
│ ⊘  legacy_flag      bool   (ignored)     │                              │  ○  tags            array<string>│
├──────────────────────────────────────────┴──────────────────────────────┴─────────────────────────────────┤
│ Inspector (bottom sheet when an edge/field is selected): sources, transform pipeline (OC-020), explain    │
└───────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```
- Built on **React Flow (`@xyflow/react`)** with two custom node types:
  - `SourceTableNode`: a list of column rows, each with a right-side handle.
  - `TargetDocNode`: a tree of field rows (the designer tree from OC-015 in compact mode), each leaf with a left-side handle. Objects can collapse; the edges of collapsed children aggregate onto the parent row as a bundle ("3 mappings").
- Both nodes are **virtualized internally**. React Flow nodes are large panels rather than one node per field, which keeps the graph to 2 nodes plus N edges. Handle positions are computed from row offsets, and edges re-route on scroll and collapse.
- **Edges:** a custom `MappingEdge` bezier.
  - Styling by state: solid emerald (auto), dashed amber (suggested/pending), solid indigo (manual), red (error from validation, OC-022).
  - A small ⓕ badge at the midpoint shows the transform count and first op.
  - Multi-source mappings (e.g., `concat(first_name, last_name)`) render as several edges converging into a small junction dot.
- **Interactions:**
  - Drag from a source handle to a target handle to create a manual mapping. An invalid drop target (incompatible type) shows a red ring and a tooltip ("text → object is not supported; map a JSON column or add fields"). A lossy drop is allowed with an amber toast suggesting a conversion.
  - Drop onto an already-mapped target: a popover asks *Replace* or *Add as additional source (concat)*.
  - Drag a source column onto the **empty area of the target panel**, or onto an object row, to create a new field with that name and map it ("create & map").
  - Clicking an edge selects it and opens the Inspector. Delete/Backspace removes it; for auto or suggested edges this becomes `rejected`, so auto-map doesn't bring it back.
  - Hovering a row highlights its edges and the counterpart rows, and dims the others.
  - The right-click context menu on a source row offers *Map to…* (combobox), *Ignore column*, and *Create target field*. On a target row: *Unmap*, *Set constant…*, *Set default…*.
  - Pan and zoom are disabled horizontally (the layout is fixed); only vertical scrolling happens within the panels. A minimap is not needed.
- **Filter & search:** the filter dropdown offers All / Unmapped / Suggested / Auto / Manual / Errors / Required. Search filters rows in both panels and keeps the edges of visible rows.
- **Table view** (toggle, the same store): a two-column editable grid with one row per **target leaf**: Target path | Type | Source (combobox with type badges and search) | Transform summary (click to edit) | State | Actions. A second tab lists source columns with their mapped targets. It is fully keyboard operable: Enter edits, Esc cancels, and ⌘↑/⌘↓ move between rows.
- **Inspector:**
  - Source(s) list with reordering (concat order).
  - Transform pipeline editor (OC-020).
  - Explain box (OC-018).
  - A live mini-preview: the value for the first 3 sample rows, before and after the transform, fetched through `POST …/preview` with `fieldMappingId` (OC-022).

### Store (`src/features/mapping/store.ts`)
- The Zustand store holds `spec` (docSchema, fieldMappings, key, …), the selection, the filter, and a UI-only `layout` cache.
- All mutations are pure reducers in `mappingOps.ts`: `connect`, `replaceSource`, `addSource`, `disconnect`, `acceptSuggestion`, `rejectSuggestion`, `ignoreColumn`, `createFieldAndMap`, `setTransforms`, and `setConstant`. Each returns a new spec and a list of `sideEffects` (e.g., `statsChanged`).
- The autosave hook from OC-007 persists `spec` with `If-Match`. Undo/redo is shared with the designer (one history stack for the workbench).
- `canConnect(sourceCol, targetField)` returns `{allowed, class: exact|lossless|lossy|incompatible, suggestedTransform?}` using the **same compatibility matrix as the server**, from a shared JSON fixture `testdata/type-compat.json`.

### Server-side
There are no new endpoints: the workbench saves via `PUT /projects/{p}/mappings/{mid}`. Validation runs on save (OC-022) and returns `{spec, stats, issues[]}`. The API rejects invalid references (unknown column or field ID) with 422.

## Acceptance criteria & unit tests

### AC1 — Render mappings as edges
**Given** a spec with 10 auto, 4 suggested, and 3 manual mappings, **then** 17 edges render with the correct style per state, and the ⓕ badge shows on edges with transforms. **Given** a collapsed object, **then** its children's edges bundle onto the parent row with a count.

| Test | Location | Verifies |
|---|---|---|
| `buildEdges.test.ts` › `AC1: one edge per source ref with state style` | `src/features/mapping/buildEdges.test.ts` | Pure function |
| `buildEdges.test.ts` › `AC1: bundles edges for collapsed parents` | `src/features/mapping/buildEdges.test.ts` | |
| `MappingCanvas.test.tsx` › `AC1: renders edges and badges` | `src/features/mapping/MappingCanvas.test.tsx` | React Flow in jsdom with a ResizeObserver mock |

### AC2 — Drag to connect
**Given** the user drags from `loyalty_tier` to `loyaltyTier`, **then** a manual mapping is created, the edge appears solid indigo, the coverage increases, and autosave fires. **Given** an incompatible target, **then** the connection is refused with a tooltip. **Given** a lossy pair (`text → integer`), **then** it connects, attaches `parseInteger`, and shows the warning toast.

| Test | Location | Verifies |
|---|---|---|
| `mappingOps.test.ts` › `AC2: connect creates manual mapping` | `src/features/mapping/mappingOps.test.ts` | |
| `canConnect.test.ts` › `AC2: matrix parity with server fixture` | `src/features/mapping/canConnect.test.ts` | |
| `MappingCanvas.test.tsx` › `AC2: onConnect wires store and toasts lossy` | `src/features/mapping/MappingCanvas.test.tsx` | Invoke the React Flow `onConnect` handler directly |

### AC3 — Replace vs add-source on mapped target
**Given** a target already mapped from `first_name`, **when** `last_name` is dropped on it, **then** a popover offers Replace or Add. Add creates a two-source mapping with a default `concat` (separator " "); Replace swaps the source.

| Test | Location | Verifies |
|---|---|---|
| `mappingOps.test.ts` › `AC3: addSource appends and sets concat` | `src/features/mapping/mappingOps.test.ts` | |
| `mappingOps.test.ts` › `AC3: replaceSource keeps transforms that remain valid` | `src/features/mapping/mappingOps.test.ts` | |

### AC4 — Create field by dropping on target panel
**Given** `loyalty_tier` is dropped on the empty area of the target panel (or on the `profile` object row), **then** a field `loyaltyTier: string` is created at the root (or under `profile`) and mapped.

| Test | Location | Verifies |
|---|---|---|
| `mappingOps.test.ts` › `AC4: createFieldAndMap at root and nested` | `src/features/mapping/mappingOps.test.ts` | Naming style is respected |

### AC5 — Delete edge semantics
**Given** a manual edge is deleted, **then** the mapping is removed. **Given** an auto or suggested edge is deleted, **then** its state becomes `rejected` (it stays hidden and blocklisted).

| Test | Location | Verifies |
|---|---|---|
| `mappingOps.test.ts` › `AC5: disconnect manual removes, auto rejects` | `src/features/mapping/mappingOps.test.ts` | |

### AC6 — Filtering, search & highlight
**Given** filter *Unmapped*, **then** only unmapped source and target rows are shown. **Given** search `addr`, **then** matching rows in both panels are shown with their edges. Hovering a row highlights its edges and counterparts.

| Test | Location | Verifies |
|---|---|---|
| `selectVisible.test.ts` › `AC6: filter + search combine` | `src/features/mapping/selectVisible.test.ts` | |
| `MappingCanvas.test.tsx` › `AC6: hover adds highlight class to related edges` | `src/features/mapping/MappingCanvas.test.tsx` | |

### AC7 — Table view parity & keyboard operation
**Given** the table view, the user can map, unmap, change the source, and open the transform editor for any target field using only the keyboard, and the resulting spec equals the one produced by the equivalent canvas actions. There are no axe violations.

| Test | Location | Verifies |
|---|---|---|
| `MappingTable.test.tsx` › `AC7: keyboard map via combobox` | `src/features/mapping/MappingTable.test.tsx` | user-event keyboard only |
| `MappingTable.test.tsx` › `AC7: same spec as canvas actions` | `src/features/mapping/MappingTable.test.tsx` | Compare the reducer outputs |
| `MappingTable.test.tsx` › `AC7: axe clean` | `src/features/mapping/MappingTable.test.tsx` | |

### AC8 — Inspector with live mini-preview
**Given** an edge is selected, **then** the Inspector shows its sources, transforms, explanation, and a before/after preview for 3 sample rows. Changing a transform refreshes the preview (debounced 300 ms).

| Test | Location | Verifies |
|---|---|---|
| `Inspector.test.tsx` › `AC8: shows sources, explain, preview` | `src/features/mapping/Inspector.test.tsx` | MSW preview |
| `Inspector.test.tsx` › `AC8: debounced preview refresh on transform change` | `src/features/mapping/Inspector.test.tsx` | Fake timers |

### AC9 — Server rejects invalid references
**Given** a PUT with a mapping that references an unknown column or field ID, **then** a 422 with `issues[].path` pointing at the offending mapping.

| Test | Location | Verifies |
|---|---|---|
| `TestSpecValidate_AC9_UnknownReferences` | `internal/mapping/validate_test.go` | |

### AC10 — Performance at scale
**Given** 300 source columns, 500 target leaves, and 300 edges, **then** initial render is < 500 ms in the test harness, and dragging a connection keeps frames under 16 ms: edge recomputation happens only for affected rows, via memoized selectors.

| Test | Location | Verifies |
|---|---|---|
| `buildEdges.bench.ts` › `AC10: 300 edges < 5ms` | `src/features/mapping/buildEdges.bench.ts` | `vitest bench` |
| `MappingCanvas.test.tsx` › `AC10: row virtualization caps DOM rows` | `src/features/mapping/MappingCanvas.test.tsx` | |
