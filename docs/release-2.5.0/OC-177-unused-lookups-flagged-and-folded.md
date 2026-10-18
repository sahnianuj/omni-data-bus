# OC-177 — Unused lookups: flagged, folded away, and not added twice

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 Mapping Canvas | P1 | 3 | OC-022, OC-047, OC-175 | Implemented |

## User story
As a **data engineer** tidying a mapping, I want a lookup ("values only" table) that no field reads to be pointed out, offered for removal and kept out of my way in the Canvas, and I want to be warned before I add one that repeats a table the object already reads, so that the source panel only shows what the document uses.

## Background (gap found 2026-10-17)
A **lookup** (a lookup-only relation, shown as *partners (values only)*) lets fields read single values from a many-to-one table without making that table an object. For example, a root `partnerNumber` could be filled from `partners.number`.

On the cart mapping, the source panel showed both *partners → partner (object)* and *partners (values only)*.
- `partner` was already bound to `partners`, so its fields read that table through their own relation.
- No field read the lookup.

The lookup changed nothing in the documents, but its twelve columns took a screen of the source panel, and nothing said it was unused.
- **Where it came from:** the Composer's bind dialog with *values only* (`addLookup`) adds the relation before any field uses it. Nothing warns when the node is already bound to the same table.
- **Nothing cleans it up later.** Taking back a proposed field (`removeItem`) drops the lookups it brought, but a lookup that was never used, or whose last field was unmapped by hand, stays.

## Scope
**In:**
- **An info issue: `LOOKUP_UNUSED`.** It reads *Lookup partners (values only) isn't read by any field. Remove it, or map a field from it.*, with a **Remove lookup** fix (`remove-relation`).
  - A lookup counts as used when:
    - a mapping's source reads it;
    - an expression names it (its id or table name followed by `.`);
    - a total reads it;
    - a relation hangs under it.
  - It's an info issue, not a warning: the mapping runs the same, apart from one wasted join per document.
- **The Canvas folds unused lookups.** The group shows only its header, labelled *partners (values only) · not used*, with a chevron to show the columns. A lookup becomes unfolded once a field reads it. In Document order (OC-175) it sits at the end of its scope already.
- **The bind dialog warns before a repeat.** It warns when *values only* would add a lookup of a table:
  - that the node is already bound to;
  - or that an existing lookup in the same scope reads with the same join.

  The warning: *partner already reads partners: its fields can use partners' columns without a lookup.* Adding stays possible.

**Out:** removing unused lookups automatically (one may have been added just before mapping a field from it); unused *bound* relations (they shape the document even with no field mapped).

## Implementation details
- **Backend:** `mapping/validate.go` › `unusedLookups(s)`. Issue `CodeLookupUnused` (info), with `Path` `relations.<id>` and `Fix{Kind: "remove-relation", RelationID}`. `remove-relation` is added to the OpenAPI `Fix.kind` enum.
- **Web:**
  - `fixes.ts` › `applyFix` `remove-relation` → `removeEmbed(spec, relationId)`.
  - `reads.ts` › `unusedLookups(spec, reads)`, the same rule on the client, for the Canvas fold.
  - `MappingCanvas` keeps the unfolded groups in local state.
  - `BindDialog` › `repeatsTable(spec, nodeId, table, join)`.

## Acceptance criteria & unit tests

### AC1 — Unused lookups are reported, with a fix
**Given** a lookup of `partners` that nothing reads, **then** validation reports `LOOKUP_UNUSED` (info) with a *Remove lookup* fix. **Given** a mapping or an expression reads it, a total sums it, or a relation hangs under it, **then** there's no issue. Applying the fix removes the relation.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_OC177_LookupUnused` | `backend/internal/mapping/validate_test.go` | Each kind of use; the fix |
| `fixes.test.ts` › `OC-177 AC1` | `web/src/features/mapping/fixes.test.ts` | `remove-relation` removes it |

### AC2 — The Canvas folds them
**Given** the same mapping, **then** the source panel shows *partners (values only) · not used* and none of its columns. Expanding shows them. Mapping a field from one of them unfolds the group for good.

| Test | Location | Verifies |
|---|---|---|
| `MappingCanvas.test.tsx` › `OC-177 AC2` | `web/src/features/mapping/MappingCanvas.test.tsx` | Header only; show and hide; read → ordinary group |

### AC3 — The bind dialog warns before a repeat
**Given** `partner` bound to `partners`, **when** the user adds a *values only* lookup of `partners` on it, **then** the warning shows and *Add lookup* stays enabled.

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `OC-177 AC3` | `web/src/features/composer/composerOps.test.ts` | `repeatsTable` |

## Implementation notes (2026-10-17)
- **Backend:** `validate.go` › `unusedLookups`. An expression counts as naming the lookup when its id or table name is followed by `.` and isn't part of a longer name (`mypartners.x` doesn't count). The issue sorts with the other info issues. `remove-relation` is in the OpenAPI `Fix.kind` enum, and the client types are regenerated.
- **Web:**
  - `reads.ts` › `unusedLookups` applies the same rule.
  - `fixes.ts` › `remove-relation` calls `removeEmbed`.
  - The Issues panel shows the fix's *Remove lookup* button like any other fix.
- **Canvas:**
  - The folded group's header shows *· not used* with a chevron (`aria-expanded`, *Show partners's columns*).
  - Opened groups are kept for the visit (local state).
  - The pane's column count leaves out the folded columns.
- **Bind dialog:** `composerOps.ts` › `repeatsTable` returns `bound` or `lookup`, and the dialog shows the warning above the footer (`role="alert"`). *Add lookup* stays enabled.
- **Found on the same mapping, fixed with it: columns read only by an expression looked unused.**
  - On `partner.name` = `concat(row.first_name, ' ', row.last_name)`, `first_name` and `last_name` had no green dot and no line, so the panel said they were never used.
  - `reads.ts` › `mappingReads` gives the columns a mapping reads: its sources, plus the columns its expressions name. It resolves names as the server's expression scope does: a bare column, `row.x`, `$root.x`, `$parent.x`, and a relation's id, table or field name. String literals are skipped, and only real columns are kept.
  - The Canvas dots (`sourceStates`), lines (`buildEdges`), filters and search (`selectVisible`), Document order (OC-175) and click-to-reveal (OC-174) all use it.
  - The mapping score (`computeStats`, kept identical to the server's) still counts sources only.
  - Tests: `reads.test.ts`.
