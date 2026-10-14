# OC-049 — Composer preview, validation & scoped auto-map

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P0 | 5 | OC-047, OC-022, OC-017 | Implemented |

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
| `ARRAY_REPEATS_ROOT` | error / warning | A root array is bound back to the driving table on matching columns (`items ← Order Details`), so each document is still one row. It replaces `BINDING_KIND_MISMATCH` for that array (error when the binding can't work as is, else a warning) and offers **Combine rows by …** (OC-066), grouping by the primary key's leading columns and taking over that array |
| `REQUIRED_FIELD_UNMAPPED` | error | A `required` field has no mapping |
| `ARRAY_UNBOUND_EMPTY` | warning | A **required** array is unbound and has no element mappings. An optional array left unmapped raises nothing, like any unmapped field |
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

## Implementation notes (2026-09-30)
- `internal/preview`: relation reads are capped at 50 rows per parent (51 are read, so a cut shows), except relations whose aggregates or split pages need every row; per-node stats (`rows`, `total` for arrays directly under the root, `truncated`, `dangling`, `adapterDiscarded`) keyed by dotted path (`items`, `items.product`); `Trim`; the worst case's size estimate; and the reads (`Related`, `RowsByKey`, `WorstCaseRows`). `POST …/preview` takes `rootKey` (driving-table column → value) and `worstCase` (a relation id); items gain `stats` and `path` (directory targets), the result `worstCase {relationId, path, rows, estimatedBytes}`. The cap applies to canvas previews too.
- Sources gain `source.RelatedCounter` (PostgreSQL): `CountRelated` — one correlated `count(*)` per key, in key order — and `TopRelated` — `GROUP BY` the join key `ORDER BY count DESC LIMIT 1` under a 5 s context. The root row is then read by key (`ReadKeys`).
- Validation: issues carry `nodePath` and `relationId` (`mapping.Annotate`); new `ARRAY_UNBOUND_EMPTY` (warning) and `DOC_SIZE_RISK` (the composed name of `EMBED_DOC_SIZE`); `ADAPTER_DISCARDS_ROWS` is a row warning. The tree marks nodes ⛔/⚠ and the Problems panel selects the node on click.
- Scoped auto-map: `automap.RunScope` (candidates: the bound relation's columns and junction payload, or the root's; targets: the unmapped fields of that scope only) and `automap.RunAll` (the root, then each bound scope, parents first). `POST …/automap` takes `nodeId` or `allScopes`; stats gain `byNode` (Go `mapping.ByNode`, TS `byNode` in `computeStats`, parity through `stats-cases.json`).
- AC6: starting a job with validation errors is `422 MAPPING_INVALID` listing each error (`fieldErrors`: `schema.table: node` → `CODE: message`), with the pre-flight checklist still attached; it replaces `PREFLIGHT_FAILED` when validation errors are among the failures (a missing target location is left to the pre-flight). The test is in `internal/jobs/handlers_test.go`, where the jobs handlers are tested.
- **Open:** `TestExports_AC6_BlockedByComposerErrors` — the export endpoint arrives with OC-050/OC-055, which must apply the same gate.
- Integration tests (`internal/preview/composed_integration_test.go`, tag `integration`) run against the dev PostgreSQL: AC1 builds order 1001 in ~0.15 s; AC2 finds the customer with 1,240 orders, shows 50 of them and estimates the full document.
