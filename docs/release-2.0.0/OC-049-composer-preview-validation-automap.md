# OC-049 — Composer preview, validation & scoped auto-map

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P0 | 5 | OC-047, OC-022, OC-017 | Not started |

## User story
As a **data engineer** composing a custom document, I want to see real sample documents with their nested arrays filled, get clear errors and warnings on the exact node that has a problem, and have the tool auto-map fields within each bound object or array, so that I can finish a complex mapping quickly and trust the result.

## Scope
**In:**
- Preview (OC-022 dry run) for nested and composed mappings: real documents from sample root rows, with per-node statistics.
- Validation surfaced per node: errors block migration and export; warnings don't.
- Auto-map (OC-017) run **per bound scope**, with the score broken down per node.

**Out:** preview of more than 20 root rows at once (use a migration to a scratch folder).

## Implementation details

### Preview (`internal/preview`)
- `POST /mappings/{id}/preview {rows: 5, rootKey?: …}` compiles the plan and reads 5 root rows (or a specific one by key). Relations are resolved with the real resolver in a read-only snapshot, and each document is returned with:
  - `document`, `key`, `path` (directory targets), `bytes`,
  - `stats` per relation node: `{rows, truncated, dangling, adapterDiscarded}`,
  - row warnings and errors (the same codes as a real run).
- Relation lookups in preview are capped (`limit` min 50) to keep it under 2 s. The UI shows "showing 50 of 1,240 items".
- A **worst case** button previews the root row with the most children for the selected array (`ORDER BY count DESC LIMIT 1` on the child, under `statement_timeout = 5s`), to check size early.

### Validation surfacing
- `POST /mappings/{id}/validate` returns issues with `fieldId` or `relationId`, a code, a severity and a message (the 1.0 format plus a `nodePath` such as `items[].product`).
- Composer-specific checks, on top of OC-037/043/044/045/047:

| Code | Severity | Meaning |
|---|---|---|
| `SOURCE_OUT_OF_SCOPE` | error | Mapping reads a column not in the field's scope |
| `BINDING_KIND_MISMATCH` | error | For example, an object bound to one-to-many without the `first` adapter |
| `BINDING_BROKEN` | error | The relation's join no longer resolves (driving table or schema changed) |
| `REQUIRED_FIELD_UNMAPPED` | error | A `required` field has no mapping |
| `ARRAY_UNBOUND_EMPTY` | warning | An unbound array has no element mappings |
| `ADAPTER_DISCARDS_ROWS` | warning | `first` adapter in preview dropped rows |
| `DOC_SIZE_RISK` | warning | Estimated size > 10 MB (multiplied across levels) |

- The tree shows ⚠/⛔ on nodes, and a problems panel lists all issues. Clicking one selects its node.

### Scoped auto-map (`internal/automap`)
- `AutoMapScope(spec, nodeId)` runs the 1.0 engine with candidates = columns of the relation bound at `nodeId` (plus root columns for root-level fields), targets = the node's unmapped descendant fields that aren't inside a deeper bound node.
- The **Auto-map all** action runs it for every scope from the root down. Already-mapped fields are never changed.
- The score header shows the total and a breakdown: "root 8/10 · items 5/6 · items.product 3/3".

## Acceptance criteria & unit tests

### AC1 — Nested preview with stats
**Given** order 1001 with 3 items, **then** the preview returns the full document with `stats.items.rows = 3` and `stats["items.product"].rows = 3`, in < 2 s against the fixture DB.

| Test | Location | Verifies |
|---|---|---|
| `TestPreview_AC1_NestedStats` *(integration)* | `internal/preview/composed_integration_test.go` | |

### AC2 — Worst-case preview
**Given** a customer with 1,240 orders, **when** worst case is requested for `orders[]`, **then** that customer is previewed, with "showing 50 of 1,240" and a size estimate for the full document.

| Test | Location | Verifies |
|---|---|---|
| `TestPreview_AC2_WorstCase` *(integration)* | `internal/preview/composed_integration_test.go` | |

### AC3 — Per-node validation
**Given** a composed spec with one out-of-scope mapping and one object bound to one-to-many without an adapter, **then** validate returns both errors with `nodePath`s, and the tree shows ⛔ on both nodes.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC3_ComposedIssues` | `internal/mapping/validate_composed_test.go` | |
| `ProblemsPanel.test.tsx` › `AC3: click selects node` | `src/features/composer/ProblemsPanel.test.tsx` | |

### AC4 — Scoped auto-map
**Given** `items[]` bound to `order_items` with fields `quantity`, `unitPrice`, `lineNo`, **then** auto-map for `items` maps all three from `order_items` columns and doesn't touch `items[].product` fields. **Given** a field already mapped manually, **then** it is unchanged.

| Test | Location | Verifies |
|---|---|---|
| `TestAutoMapScope_AC4_ScopedCandidates` | `internal/automap/scope_test.go` | |
| `TestAutoMapScope_AC4_KeepsManual` | same | |

### AC5 — Score breakdown
**Given** the scoped results above, **then** the header shows the total and per-node breakdown, matching the server's `automapStats.byNode`.

| Test | Location | Verifies |
|---|---|---|
| `ScoreHeader.test.tsx` › `AC5: per-node breakdown` | `src/features/composer/ScoreHeader.test.tsx` | |

### AC6 — Errors block migration & export
**Given** a mapping with a validation error, **then** starting a job and exporting a bundle both return `422 MAPPING_INVALID` listing the issues.

| Test | Location | Verifies |
|---|---|---|
| `TestJobsCreate_AC6_BlockedByComposerErrors` | `internal/httpapi/jobs_test.go` | |
| `TestExports_AC6_BlockedByComposerErrors` | `internal/httpapi/exports_test.go` | |
