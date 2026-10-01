# OC-070 — Resolve fields that aren't in the imported design

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P1 | 3 | OC-059, OC-061 | Implemented |

## User story
As a **data engineer** who built a mapping from the customer's JSON design, I want fields I added on purpose (such as an embedded `product`) to stop showing up as problems. When the tool does flag them, I want to know what that means and to resolve it in one click: keep the fields in the design, or remove them.

## Background (2026-10-01)
After the user embedded `product` into each order line, the panels showed about 13 warnings like `products[].product.categoryId isn't in the imported design. REFERENCE_EXTRA_FIELD`, one per field. The preview showed correct documents. The warnings didn't say what the imported design is, why it matters or how to resolve them, and there was no fix. A field added on purpose isn't a problem unless the mapping must match the design exactly (`referenceStrict`).

## Scope
**In:**
- `REFERENCE_EXTRA_FIELD` is reported once per added branch: the topmost field that isn't in the imported design. Its message names the design file and how many fields the branch holds, e.g. *"products[].product (13 fields) isn't in the design you imported from orders.json. Add it to the design to keep it, or remove it."*
- Severity: `info` normally, an error when the mapping is `referenceStrict`. Neither blocks a migration unless strict.
- Fix `add-to-design` (*Add to design*): the branch's fields join `docSchema.imported.fields` with their types, so conformance (OC-061) checks them from now on.
- *Remove* on the issue deletes the branch. A relation's field removes the relation, its mappings and the fields only it fed (OC-069).
- *Add all to design* when more than one branch is flagged. It's in the Canvas validation panel, and in the Composer's problems panel when strict.

**Out:** changing a field's imported type (REFERENCE_TYPE_MISMATCH).

## Acceptance criteria & unit tests

### AC1 — One plain issue per added branch
**Given** an imported design without `products[].product`, **when** `product` is embedded with 13 fields, **then** one `info` issue on `products[].product` says it has 13 fields and isn't in the design from its file, with fix `add-to-design`. A single added leaf is one issue on that leaf. **Given** `referenceStrict`, **then** it's an error.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_OC070_AC1_ExtraBranch` | `backend/internal/mapping/validate_reference_test.go` | |

### AC2 — Add to design
**When** *Add to design* is applied, **then** the branch's paths and types are in `imported.fields`, and the issue is gone on the next validation.

| Test | Location | Verifies |
|---|---|---|
| `fixes.test.ts` › `OC-070 AC2: add to design` | `web/src/features/mapping/fixes.test.ts` | |

### AC3 — Remove, and add all
**When** *Remove* is clicked on a relation's branch, **then** the relation and its fields are gone. **When** *Add all to design* is clicked, **then** every flagged branch is added.

| Test | Location | Verifies |
|---|---|---|
| `ValidationPanel.test.tsx` › `OC-070 AC3: remove and add all` | `web/src/features/mapping/ValidationPanel.test.tsx` | |

## Implementation notes (2026-10-01)
- Server, `mapping/reference.go`: `extraBranches` maps each extra leaf to its shortest path prefix that's in the design but not in the contract, then reports one issue per branch, sorted by path. A branch that is a single leaf gives the plain *"p isn't in …"* message. Otherwise the message counts the leaves. Containers don't count, and the type field is skipped as before.
- The OC-061 test `TestValidate_AC4_ReferenceStrict` now expects `info` for the non-strict case. The mapping-sheet golden file is unchanged.
- Web, `fixes.ts`: `addToDesign` appends the branch (field, `x[]` items, descendants) to `imported.fields` in the server's path format, skipping paths already there. `removeBranch` removes the relations whose field is in the branch (`removeEmbed`), then the field, its mappings and its aggregates. The Canvas `ValidationPanel` shows *Remove* beside the fix, and *Add all N to design* in the severity header when there's more than one. The Composer's `ProblemsPanel` (it lists errors and warnings, so strict mappings) has the same buttons.
- API: fix kind `add-to-design` added to the `Issue.fix.kind` enum.
