# OC-011 — Schema explorer UI & data preview

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E2 Projects & Source | P0 | 8 | OC-004, OC-010 | Implemented |

## User story
As a **data engineer**, I want to browse my source schema visually, search it, inspect table structure and relationships, and preview sample rows, so that I can pick the right tables and understand the data before mapping.

## Implementation details

### Preview API
`GET /api/v1/connections/{id}/tables/{schema}/{table}/preview?limit=50`
- It runs `SELECT <quoted cols> FROM <pgx.Identifier{schema,table}.Sanitize()> LIMIT $1` with `statement_timeout=5s` inside a read-only transaction. `limit` is capped at 200.
- Values are encoded with the same encoders as the pipeline (OC-020), so the preview matches what migration will produce. Each cell carries `{v, t}`, where `t` is the jsonType, plus a `truncated:true` flag for strings over 1 KB and bytea over 256 bytes.
- An optional `orderBy` must be a known column name (validated against the snapshot). The default is the primary key when present.
- The result is not cached.

### Explorer layout (`/projects/{projectId}/source/schema`, Workbench pattern, inside the project)
```
┌──────────────── Schema tree (resizable 280px) ─┬─────────── Table detail ───────────────────────┐
│ 🔍 Search tables & columns…    [⟳ Refresh]      │ shop.customer   TABLE  ~120,345 rows · 29.5 MB │
│ ▾ shop (6)                                      │ [Columns] [Relationships] [Indexes] [Preview]  │
│   ▸ ▦ address          ~40k                     │ ┌──────┬──────────────┬───────────┬────┬─────┐ │
│   ▾ ▦ customer         ~120k   ← selected       │ │ key  │ name         │ type      │null│ def │ │
│      🔑 customer_id  int4                        │ │ 🔑   │ customer_id  │ integer   │ no │ seq │ │
│      ◇ email        citext                      │ │ ◇U   │ email        │ citext    │ no │     │ │
│   ▸ ▦ orders           ~1.2M                    │ │ ↗FK  │ address_id   │ integer   │yes │     │ │
│   ▸ 👁 v_active_customers                       │ └──────┴──────────────┴───────────┴────┴─────┘ │
│ ▸ billing (12)                                  │                                               │
│ Last discovered 3 min ago · 18 tables           │             [ + Add to project mappings ]     │
└─────────────────────────────────────────────────┴───────────────────────────────────────────────┘
```
- **Tree:** virtualized (it must handle 20k nodes smoothly). Icons by kind (table, view, materialized view, partitioned). Row estimate badges are humanized (`1.2M`). PK and FK glyphs on column nodes. Keyboard support: ↑/↓ move, →/← expand or collapse, Enter selects, and type-ahead.
- **Search:** client-side fuzzy match (`fuse.js`, threshold 0.3) over `schema.table` and `schema.table.column`. Matched substrings are highlighted and ancestors are auto-expanded. Filter chips: *Tables*, *Views*, *Has PK*, *No PK*.
- **Columns tab:** a table with Key glyph, Name, Type (TypeBadge using the monospace display type plus jsonType), Nullable, Default, and Comment. The warning icon for unsupported types has a tooltip.
- **Relationships tab:** a mini ER diagram (React Flow, read-only, dagre auto-layout) showing the selected table with its FK parents and children. Clicking a neighbor navigates to it. Below it is a textual list of FKs.
- **Indexes tab:** a list of indexes.
- **Preview tab:** a grid of the first 50 rows. `null` renders as a muted italic *NULL*, JSON cells show a collapsible viewer, and truncated values have an ellipsis and an *Expand* affordance (a popover with the full value up to 64 KB, loaded from the returned value). There is a *Rows: 50 ▾* selector (10/50/200).
- **Header actions:** *Refresh* re-runs discovery; when the diff is non-empty it shows a toast "3 tables changed" with a *View changes* sheet listing the diff. *+ Add to project mappings* creates a mapping for the selected table in the current project (same as selecting it in the Tables step, OC-007) and navigates to the Tables step with it checked. When a mapping already exists for the table, the button reads *Open mapping →*.
- **Stale stats:** the estimate shows as "—" with the tooltip "Estimate unavailable — run ANALYZE".
- **Empty/error states:** "No tables found in included schemas" with a *Change filters* CTA, and discovery errors are shown with the problem hint.

## Acceptance criteria & unit tests

### AC1 — Tree renders the snapshot
**Given** a snapshot summary, **then** schemas render with table counts, tables are sorted by name, row estimates are humanized, kinds have distinct icons, and the footer shows the last-discovered relative time.

| Test | Location | Verifies |
|---|---|---|
| `SchemaTree.test.tsx` › `AC1: renders schemas, counts, sorted tables` | `src/features/discovery/SchemaTree.test.tsx` | |
| `format.test.ts` › `AC1: humanizeCount 999, 1.2k, 1.2M, 3.4B` | `src/lib/format/format.test.ts` | |
| `SchemaTree.test.tsx` › `AC1: virtualizes 20k nodes (renders < 100 DOM rows)` | same | |

### AC2 — Search & filters
**Given** the query `cust email`, **then** `shop.customer.email` matches, `shop` and `customer` are auto-expanded, and the match is highlighted. **Given** the *No PK* chip, **then** only tables without a primary key are shown.

| Test | Location | Verifies |
|---|---|---|
| `useSchemaSearch.test.ts` › `AC2: matches column path and returns expand set` | `src/features/discovery/useSchemaSearch.test.ts` | |
| `useSchemaSearch.test.ts` › `AC2: filter chips combine with AND` | same | |
| `SchemaTree.test.tsx` › `AC2: highlights matched substring` | `SchemaTree.test.tsx` | |

### AC3 — Keyboard navigation
**Given** focus in the tree, ↓ moves to the next visible node, → expands (or moves to the first child if already expanded), ← collapses (or moves to the parent), and Enter selects and loads the detail. It follows the ARIA `tree` pattern (`role="tree"`, `aria-expanded`, `aria-level`).

| Test | Location | Verifies |
|---|---|---|
| `SchemaTree.test.tsx` › `AC3: arrow key navigation` | `SchemaTree.test.tsx` | |
| `SchemaTree.test.tsx` › `AC3: ARIA attributes` | same | |

### AC4 — Table detail tabs
**Given** a selected table, **then** the Columns tab lists all columns with key glyphs (PK, unique, FK) and type badges; the Relationships tab shows parents and children; and the Indexes tab lists indexes.

| Test | Location | Verifies |
|---|---|---|
| `TableDetail.test.tsx` › `AC4: columns with key glyphs` | `src/features/discovery/TableDetail.test.tsx` | |
| `relationshipGraph.test.ts` › `AC4: builds nodes/edges for parents and children` | `src/features/discovery/relationshipGraph.test.ts` | Pure function |
| `TableDetail.test.tsx` › `AC4: clicking neighbor navigates` | `TableDetail.test.tsx` | |

### AC5 — Safe, faithful preview
**Given** the table `"Order Details"`, **then** the preview SQL quotes identifiers correctly (`"shop"."Order Details"`). **Given** `limit=1000`, it is capped at 200. **Given** an unknown `orderBy`, a 422 is returned. Cell values use the pipeline encoders (e.g., timestamptz → ISO `Z`, bytea → base64 truncated).

| Test | Location | Verifies |
|---|---|---|
| `TestPreviewSQL_AC5_QuotesIdentifiers` | `internal/connectors/source/postgres/preview_test.go` | Including a name with an embedded `"` |
| `TestPreview_AC5_LimitCapped` | same | |
| `TestPreviewHandler_AC5_UnknownOrderBy422` | `internal/httpapi/preview_test.go` | |
| `TestPreview_AC5_UsesPipelineEncoders` | `…/preview_test.go` | Fake rows → encoded cells |
| `PreviewGrid.test.tsx` › `AC5: renders NULL, JSON viewer, truncated expand` | `src/features/discovery/PreviewGrid.test.tsx` | |

### AC6 — Refresh with change summary
**Given** *Refresh* is clicked and the diff is non-empty, **then** a toast "N tables changed" appears, and *View changes* opens a sheet listing added, removed, and changed tables and columns. The tree updates without losing the current selection if the table still exists.

| Test | Location | Verifies |
|---|---|---|
| `SchemaExplorerPage.test.tsx` › `AC6: refresh shows diff toast and sheet` | `src/features/discovery/SchemaExplorerPage.test.tsx` | |
| `SchemaExplorerPage.test.tsx` › `AC6: preserves selection after refresh` | same | |

### AC7 — Add table to project mappings
**Given** a selected table without a mapping, **when** the user clicks *+ Add to project mappings*, **then** `POST /projects/{p}/mappings` is called for that table and the Tables step opens with it checked. **Given** a mapping already exists for the table, **then** the button reads *Open mapping →* and navigates to its workbench.

| Test | Location | Verifies |
|---|---|---|
| `SchemaExplorerPage.test.tsx` › `AC7: adds mapping and navigates to tables step` | `SchemaExplorerPage.test.tsx` | |
| `SchemaExplorerPage.test.tsx` › `AC7: existing mapping shows Open mapping` | same | |

## Implementation notes (as built)
- **Route:** `/projects/:projectId/source/schema`, opened from *Explore schema →* on the saved source card. The selected table lives in the URL (`?schema=&table=`), so it survives refreshes and back/forward, and a refresh keeps it while the table still exists. For editors, the first visit runs discovery automatically; viewers see an explanation instead.
- **Tree:** virtualized with `@tanstack/react-virtual` and keyboard-driven via `aria-activedescendant` (focus stays on the tree). Search uses `fuse.js` extended search (space-separated terms must all match, threshold 0.3). A column counts as a match only when the query hits the column's own name, so "orders" doesn't expand every `orders.*` column. Nodes you collapse during a search stay collapsed until the query changes. Unreadable tables (e.g. `billing` for `omni_ro`) show a lock.
- **Relationships:** a lightweight three-column diagram (references → table → referenced by) built by the pure `relationshipGraph()`, instead of React Flow + dagre. A one-hop neighbourhood never needs a general graph layout, and this keeps ~200 KB out of the bundle.
- **Preview:** `internal/encode` holds the §6.4 value encoders that OC-020's pipeline will reuse. Types pgx can't decode natively are read as text (enums, citext, timetz, xml, tsvector, …) and money as numeric. A numeric column encodes as a number when its declared precision is ≤ 15, otherwise as an exact string. Cells over 1 KB (text) or 256 B (binary) are flagged `truncated`, and the full value (up to 64 KB) is sent for the *Expand* popover. Unreadable tables are refused before querying (`SOURCE_PERMISSION_DENIED`).
- **AC7 (Add to project mappings) moves to OC-035:** it needs the mappings API, which OC-035 delivers together with the Tables step.

