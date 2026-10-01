# OC-059 — Import a target JSON design into the composer

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P0 | 5 | OC-048, OC-015 | Implemented |

## User story
As a **data engineer** whose customer has already designed the target JSON documents, I want to import those documents (or their JSON Schema) straight into the composer, with their exact field names, nesting, order, types and example values, so that I map the source tables into the customer's design instead of rebuilding it by hand.

## Background (gap found 2026-09-30)
The composer's *Paste JSON sample…* (OC-048 AC2) takes pasted text only, and it infers types in the browser (`composerOps.inferField`). That inference is weaker than the server's `docschema.InferFromSamples` used by the Design tab (OC-015): it doesn't detect datetime formats, merge several samples, or infer `required`. It also throws the sample's values away. File upload, drag-and-drop and JSON Schema import exist only on the Design tab.

## Scope
**In:**
- Composer entry points:
  - the empty-state start *Import JSON design…*;
  - the toolbar *Import ▾* (replace the design);
  - the node menu *Import JSON here…* (add to an object, or set an array's items).
  - Each accepts paste, a file picker, or a `.json` / `.ndjson` file dropped on the tree.
- Accepted formats (sniffed with `sniffJson`): one sample object, an array of samples, NDJSON, or a JSON Schema (draft 2020-12 or the draft-07 subset from OC-015).
- One inference engine: the composer calls `POST /doc-schemas/infer`. `composerOps.inferField` is deleted, so the composer and the Design tab always produce the same design.
- The customer's sample becomes the mapping's **reference document**, and each field keeps an **example value**.
- **Constant suggestions** for values that look fixed (discriminators, versions).
- An **import report**: field count, inferred types and formats, optional fields, and warnings.

**Out:**
- YAML, XML and Avro inputs.
- Sampling an existing Couchbase collection. The Design tab already does this (OC-015 `collection-sample`), and the composer shares the same store.
- Suggesting the table bindings (OC-060).
- Re-importing a revised design while keeping its mappings (OC-061).

## Implementation details

### Spec additions (additive to spec v2; no version bump)
- `docSchema.reference`: the first imported sample, verbatim, capped at 256 KB (key order kept). If it's larger, only the field examples are stored, and the import report says so.
- `docSchema.imported {at, fileName?, format: sample|ndjson|json-schema, samples, fieldCount}`: survives later edits. Editing a node still sets `source = manual`, as today.
- `DocField.example`: the first non-null value seen for the field, truncated to 200 characters (strings) or summarised (`[3 items]`, `{…}`). For JSON Schema this is `examples[0]`, `const`, or `default`.
- These fields are round-tripped by `Spec.UnmarshalJSON`. The OC-050 bundle drops `reference` and `example` (design-time only) so customer data never ends up in an exported bundle.

### Inference (server, `internal/docschema`)
- `InferFromSamples` gains the `example` capture and returns `constants []ConstantHint {fieldId, value, reason}`. A field is hinted when:
  - it's a string, number or boolean with the same value in every sample (at least 2 samples); or
  - its name is in the discriminator dictionary (`type`, `docType`, `_type`, `kind`, `schemaVersion`, `version`), for any number of samples.
- `POST /doc-schemas/infer` returns `{docSchema, warnings, constants, reference?}`.

### UI (`src/features/composer`)
- `ImportDialog.tsx`:
  - A paste editor with the JSON linter, a *Choose file…* button, and a drop zone (the button is the keyboard equivalent of dropping).
  - A format badge (sample / N samples / JSON Schema).
  - The import report.
  - Replace or add. Replacing a design that has bindings or mappings needs confirmation and lists what will be removed.
- Tree rows show the example value dimmed after the type (`orderDate: datetime · 2024-03-01T10:00:00Z`). The inspector shows it in full.
- Constant hints appear in the Problems panel as info items: *Keep `type` as the constant "order"?* **Apply** creates a constant field mapping (OC-020 `constant` op). Nothing is applied automatically.
- The preview pane gains a *Reference* tab, showing the customer's document next to the generated one.

## Acceptance criteria & unit tests

### AC1 — Import a file with several samples
**Given** a `.ndjson` file with 3 order documents where `couponCode` appears in only one, **when** it's dropped on the empty composer, **then**:
- the tree keeps the file's key order;
- `orderDate` is `datetime` with format `iso8601`;
- `couponCode` isn't required and every always-present field is;
- `items` is `array<object{…}>`, merged across all samples;
- the report says "3 samples · 14 fields".

| Test | Location | Verifies |
|---|---|---|
| `TestInfer_AC1_ExamplesAndOrder` | `internal/docschema/infer_test.go` | |
| `ImportDialog.test.tsx` › `AC1: drop ndjson file` | `src/features/composer/ImportDialog.test.tsx` | |

### AC2 — JSON Schema import in the composer
**Given** a draft 2020-12 schema with `required`, `format: date-time`, `description` and `examples`, **then** the composer tree has the same required flags, formats, descriptions and examples. Unsupported keywords are listed as warnings.

| Test | Location | Verifies |
|---|---|---|
| `ImportDialog.test.tsx` › `AC2: JSON Schema sniffed and imported` | `src/features/composer/ImportDialog.test.tsx` | |

### AC3 — Reference document and examples persist
**Given** an imported sample, **when** the mapping is saved and reloaded, **then** `docSchema.reference` and each field's `example` are unchanged. **Given** a 300 KB sample, **then** `reference` is omitted, examples are kept, and the report warns. **Given** a bundle export (OC-050), **then** neither is present in it.

| Test | Location | Verifies |
|---|---|---|
| `TestSpec_AC3_ReferenceRoundTrip` | `internal/mapping/spec_test.go` | |
| `TestInfer_AC3_ReferenceCap` | `internal/docschema/infer_test.go` | |

### AC4 — Constant suggestions
**Given** 3 samples all with `"type": "order"` and `"schemaVersion": 2`, **then** two constant hints are returned. **When** the user applies one, **then** a constant field mapping with that value exists and the field counts as mapped. **Given** one sample only, **then** only the dictionary names are hinted.

| Test | Location | Verifies |
|---|---|---|
| `TestInfer_AC4_ConstantHints` | `internal/docschema/infer_test.go` | |
| `ProblemsPanel.test.tsx` › `AC4: apply constant hint` | `src/features/composer/ProblemsPanel.test.tsx` | |

### AC5 — Import onto a node keeps existing work
**Given** `items[]` bound to `order_items` with `quantity` mapped, **when** `{"quantity":1,"discount":0.1}` is imported onto `items[]`, **then**:
- `discount` is added;
- `quantity` keeps its ID, binding and mapping;
- the report lists `quantity` as "already present, kept".

| Test | Location | Verifies |
|---|---|---|
| `composerOps.test.ts` › `AC5: import onto node keeps mapped fields` | `src/features/composer/composerOps.test.ts` | |

### AC6 — One inference engine
**Given** the same input, **then** the composer import and the Design tab import produce identical designs, ignoring IDs. `composerOps.inferField` no longer exists.

| Test | Location | Verifies |
|---|---|---|
| `ImportDialog.test.tsx` › `AC6: same design as Design tab` | `src/features/composer/ImportDialog.test.tsx` | |

### AC7 — Accessible import
**Then** the dialog passes axe, every drop action has a button equivalent, and the report is announced in a live region.

| Test | Location | Verifies |
|---|---|---|
| `Composer.a11y.test.tsx` › `AC7: import dialog` | `src/features/composer/Composer.a11y.test.tsx` | |

## Implementation notes (2026-09-30)
- `docschema.Import(kind, content, fileName)` is the single entry point behind `POST /doc-schemas/infer`, for both the Design tab and the composer. It sets `imported {at, fileName, format, samples, fieldCount}` and `reference` (the first sample, compacted, ≤ `MaxReferenceBytes` = 256 KB; above that there's a warning and only examples are kept). `fieldCount` counts named fields, not array item nodes, which matches the composer's report.
- Examples: `DocField.example` holds scalars only (string, number, boolean), strings truncated to 200 runes plus "…". Containers carry none, because the tree already shows their children. JSON Schema examples come from `const`, `examples[0]` or `default`, and *Export JSON Schema* writes them back as `examples`, so they round-trip. `docschema.Validate` rejects examples over 1 KB (`EXAMPLE_TOO_LARGE`) and references over 256 KB (`REFERENCE_TOO_LARGE`).
- Constant hints are stored on the design (`docSchema.constantHints`, not in `imported`), so importing onto a node can add some without turning the mapping into an "imported design". Hints are skipped for fields inside arrays. The Problems panel lists hints for fields that still exist and aren't mapped. **Keep as constant** uses `setConstant`; **Dismiss** removes the hint.
- Sampling a live Couchbase collection (`…/infer-schema`) strips examples and hints (`docschema.StripExamples`), so production values never land in mapping metadata.
- Web: `composerOps.inferField`, `pasteSample` and `fromSample` are gone, replaced by `importDesign(spec, serverDesign, nodeId | null)` → `{spec, added, kept}`. The dialog dry-runs it to build the report. Files can be dropped on the tree (on a container row: import there; on the root or empty space: replace) or on the dialog. The Sample pane shows the reference beside the generated document (toggle *Reference*).
- **Open (OC-050):** the bundle must strip `docSchema.reference`, `constantHints` and field `example`s (AC3's last clause). The OC-050 story already lists this.
