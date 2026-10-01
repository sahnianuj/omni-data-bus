# OC-061 — Re-import a revised design, conformance check & mapping sheet

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 5 | OC-059, OC-049 | Implemented |

## User story
As a **data engineer** mapping into a customer-owned JSON design, I want to re-import the customer's revised design without losing my mappings, see where the generated documents don't match the design, and hand the customer a field-by-field mapping sheet, so that the two sides can agree the mapping before migrating.

## Background (gap found 2026-09-30)
Today, importing into the composer either replaces the design and clears every binding and mapping (`fromSample`), or skips fields whose names already exist, even if their type or position changed (`pasteSample`). Nothing checks the generated documents against the imported design, and there's no way to share the mapping outside the tool.

## Scope
**In:**
- **Re-import with reconcile**: a diff (added, removed, type changed, renamed, moved) that keeps the IDs, bindings and mappings of fields that still exist.
- **Conformance validation** against the imported design, with an opt-in **strict** mode that blocks migration and export.
- Per-document conformance in the preview.
- **Mapping sheet** export (CSV and Markdown).

**Out:**
- Two-way sync with a customer schema registry.
- Value-level comparison with the reference document (values differ by design).

## Implementation details

### Reconcile (`internal/docschema/reconcile.go`)
- `Reconcile(current, incoming Schema) (Schema, Diff)` matches fields by path.
  - Unchanged paths keep their field ID, so their bindings and mappings stay.
  - **Renames**: a removed and an added sibling of the same type, with name similarity ≥ 0.8 (OC-017 normalizer) or the same `example`, become a proposed rename that keeps the ID.
  - **Moves**: the same name and type under a different parent become a proposed move.
- The web `schemaDiff` shows the Go `Diff` (added green, removed red, changed amber, renamed and moved blue). Each proposal can be declined, which turns it into remove + add.
- Removed fields list the mappings and relations that go with them. The user confirms before applying.
- The Design tab's replace-or-merge (OC-015 AC7) uses the same `Reconcile`, so both views behave the same.

### Conformance validation (`internal/mapping`)
Active when `docSchema.imported` is set (OC-059). `spec.referenceStrict` (default false) raises the severities marked *strict*.

| Code | Severity | Meaning |
|---|---|---|
| `REFERENCE_FIELD_UNMAPPED` | info since OC-075, was warning (strict: error) | A field from the imported design has no mapping, constant or binding |
| `REFERENCE_TYPE_MISMATCH` | error | The mapping's output type can't produce the design's type (e.g. text into `integer` with no conversion) |
| `REFERENCE_EXTRA_FIELD` | info (strict: error); was warning until OC-070 | A field added in the tool that isn't in the imported design. Since OC-070 there's one issue per added branch, with *Add to design* and *Remove* |
| `REFERENCE_NULLABLE` | warning | The design field is required, but its source column is nullable with no `default` transform |

- Strict errors go through the OC-049 gate: `422 MAPPING_INVALID` on job start and bundle export.

### Preview conformance
- Each preview document (OC-049) gains `conformance {missing[], extra[], typeDiff[]}` by path, compared with the design's structure, not values. The Reference tab (OC-059) highlights those paths.

### Mapping sheet
- `GET /mappings/{id}/mapping-sheet?format=csv|md` returns one row per design field, in tree order, with these columns:
  - `path`, `type`, `required`, `example`;
  - `source` (`table.column`, or the relation path such as `items[] ← order_items.quantity`);
  - `transform` (a readable summary of the op chain), `constant`, `status` (mapped, constant, bound, unmapped), `issues`.
- The composer toolbar has *Export ▾ → Mapping sheet (CSV / Markdown)*. The file name is `<mapping>-mapping-sheet-<date>.csv`.

## Acceptance criteria & unit tests

### AC1 — Re-import keeps mappings
**Given** an imported and fully mapped order design, **when** a revision with one added field and one type change is imported, **then**:
- every other field keeps its ID and mapping;
- the added field is unmapped;
- the type change is flagged amber, and `REFERENCE_TYPE_MISMATCH` is re-evaluated.

| Test | Location | Verifies |
|---|---|---|
| `TestReconcile_AC1_KeepsIDs` | `internal/docschema/reconcile_test.go` | |

### AC2 — Renames and moves are proposed
**Given** `custName` renamed to `customerName`, and `zip` moved from the root into `shipping`, **then** both are proposed and keep their mappings if accepted. **When** a proposal is declined, **then** it becomes remove + add.

| Test | Location | Verifies |
|---|---|---|
| `TestReconcile_AC2_RenameMove` | `internal/docschema/reconcile_test.go` | |
| `ReimportDialog.test.tsx` › `AC2: decline rename` | `src/features/composer/ReimportDialog.test.tsx` | |

### AC3 — Removals are confirmed
**Given** a revision without `items[].discount` (mapped), **then** the dialog lists the field and its mapping. Nothing is removed until the user confirms.

| Test | Location | Verifies |
|---|---|---|
| `ReimportDialog.test.tsx` › `AC3: confirm removals` | `src/features/composer/ReimportDialog.test.tsx` | |

### AC4 — Strict conformance blocks migration
**Given** `referenceStrict = true` and one unmapped design field, **then** job create returns `422 MAPPING_INVALID` with `REFERENCE_FIELD_UNMAPPED`. **When** strict is off, **then** it's info (a warning before OC-075) and the job starts.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC4_ReferenceStrict` | `internal/mapping/validate_reference_test.go` | |

### AC5 — Preview conformance
**Given** a preview where `items[].sku` comes out as a number but the design says string, **then** `conformance.typeDiff` lists `items[].sku`, and the Reference tab highlights it.

| Test | Location | Verifies |
|---|---|---|
| `TestPreview_AC5_Conformance` | `internal/preview/preview_test.go` | |

### AC6 — Mapping sheet
**Given** the mapped order fixture, **then** the CSV matches the golden file row for row, and the Markdown renders the same rows as a table.

| Test | Location | Verifies |
|---|---|---|
| `TestMappingSheet_AC6_Golden` | `internal/mapping/sheet_test.go` (golden `testdata/golden/mapping_sheet_order.csv`) | |

### AC7 — One reconcile for both views
**Given** the same current and incoming designs, **then** the Design tab's merge and the composer's re-import produce the same result.

| Test | Location | Verifies |
|---|---|---|
| `DocDesigner.test.tsx` › `AC7: merge uses server reconcile` | `src/features/docschema/DocDesigner.test.tsx` | |

## Implementation notes (2026-09-30)
- `docschema.Reconcile(current, incoming, declined)` follows the revision (names, order, types, examples, reference, import record) and gives matched fields their current IDs and bindings. Matching runs level by level: by name under a matched parent, then **renames** (an unmatched sibling of the same type whose name similarity is ≥ 0.8, or whose example is identical). Similarity is the Dice coefficient of the names' words, where a word also matches a longer word it abbreviates (`cust` ~ `customer`). Then **moves** (a scalar with the same name and type under another parent). The Go normalizer isn't used, because `docschema` can't import `automap`.
- Two ways in, one algorithm. The composer calls the stateless `POST /doc-schemas/reconcile {current, incoming, declined}` for the diff, lets the user uncheck proposals (refetching with `declined`), and applies the result with `composerOps.adoptDesign`, an undoable edit that drops only what pointed at removed fields. The Design tab's new *Update, keep mappings* sends `strategy: "reconcile"` to `…/design`, which reconciles, `mapping.Prune`s and returns `diff`. Reconcile doesn't auto-map: added fields stay unmapped (AC1).
- The contract is `docSchema.imported.fields` (path, type, required), recorded at import (OC-059) and replaced by each re-import. Conformance compares against it, not against the current design, so renaming a field in the tool shows up as missing + extra.
- Validation (`mapping/reference.go`): `REFERENCE_TYPE_MISMATCH` compares the design's field type with the imported one; mapping-level type checks were already there. An unmapped field that is still `required` is left to `REQUIRED_FIELD_UNMAPPED` (already an error), so single-sample imports don't report it twice. `REFERENCE_FIELD_UNMAPPED` also covers a field of the imported design that no longer exists in the design. Strict errors reach the OC-049 gate through `mapping.Ready`.
- Preview items gain `conformance {missing, extra, typeDiff}` (`docschema.Conform`): shapes and JSON kinds, never values. A path is only "missing" when its parent object is present, so empty arrays say nothing about their items. The Sample pane shows it above the reference document.
- Mapping sheet: `GET …/mapping-sheet?format=csv|md` (`mapping.Sheet`), with statuses bound, grouping, mapped, constant, aggregate, suggested and unmapped. The composer toolbar has *Mapping sheet (CSV)* and *(Markdown)* buttons, not a menu, and saves pending edits before downloading. Golden file: `backend/internal/mapping/testdata/golden/mapping_sheet_order.csv` (`ODB_UPDATE_GOLDEN=1`).
- E2E: `web/tests/e2e/composer-import.spec.ts` also re-imports a revision (unitPrice → unit_price is kept as a rename, lineNo is removed after confirmation, currency is added) and checks the saved mapping sheet.
