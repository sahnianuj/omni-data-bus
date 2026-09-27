# OC-015 — Target document designer

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E4 Design & Map | P0 | 13 | OC-007 | Implemented |

## User story
As a **data engineer**, I want to define the JSON document structure for the target collection by generating it from the table, dropping in a sample document or JSON Schema, sampling existing documents, or building it by hand, so that the target model fits how my application reads data rather than just mirroring the relational table.

## Implementation details

### Model (`internal/docschema`)
Uses `DocField` from [architecture §6.2](00-architecture.md#62-mapping-spec-mappingsspec-stored-in-mappingsspec).
```go
type FieldType string // string|integer|number|boolean|datetime|object|array|any
type DocField struct {
    ID          string     `json:"id"`           // "fld_" + nanoid(10); stable across renames
    Name        string     `json:"name"`
    Type        FieldType  `json:"type"`
    Required    bool       `json:"required,omitempty"`
    Description string     `json:"description,omitempty"`
    Children    []DocField `json:"children,omitempty"` // object
    Items       *DocField  `json:"items,omitempty"`    // array element
    Format      string     `json:"format,omitempty"`   // datetime: "iso8601"|"epoch_ms"|"date"; string: "uuid"|"email"
}
```
Rules enforced by `docschema.Validate`:
- Sibling names are unique and 1–128 characters.
- Names must not contain `.` or `[`, because paths use those characters.
- Depth ≤ 16.
- An array has exactly one `items`, and an object has no `items`.
- The total field count is ≤ 2,000.
- The root is an object.

### Four ways to create a design
1. **Generate from table** (`docschema.FromTable(table, opts)`). This is the default on mapping creation.
   - Options: `naming: camelCase | snake_case | preserve` (default camelCase), `nestByPrefix: bool` (default false; when true, `billing_street`, `billing_city` → `billing: {street, city}` if ≥ 2 columns share a prefix), and `includeTypeField`.
   - Types come from `jsonType`, and `required` from NOT NULL.
2. **Import a JSON sample** (drop a `.json` file or paste). `docschema.InferFromSamples(docs []json.RawMessage)`:
   - Accepts one object, an array of objects, or NDJSON, up to 5 MB and 1,000 docs.
   - Merges types across samples: int+float → number; mixed scalar types → `any` with a warning; null-only → `any`. A field present in every sample → `required`. Arrays merge the item schemas of their elements.
   - ISO-8601 date-like strings → `datetime` (regex plus `time.Parse` check); UUID-like → `string, format uuid`.
3. **Import JSON Schema** (draft 2020-12 or draft-07 subset) via `docschema.FromJSONSchema`.
   - Supports `type`, `properties`, `required`, `items`, `format` (date-time, date, email, uuid), `description`, `$ref` to local `$defs` (cycles detected → error), and `anyOf`/`oneOf` of null + T → T (nullable).
   - Unsupported keywords are ignored, with a list of warnings returned.
4. **Sample existing collection** (when the collection has documents). `DocumentSampler.Sample(ks, n=50)`:
   - Prefers the KV range scan `collection.Scan(gocb.SamplingScan{Limit: 50}, nil)` (server ≥ 7.6).
   - Falls back to the query `SELECT RAW c FROM ks c LIMIT 50` when the Query service is available.
   - The samples feed `InferFromSamples`. If neither method works, the option is disabled with the reason.

`POST /api/v1/doc-schemas/infer` handles options 2 and 3 (`{kind: "json-sample"|"json-schema", content}`), and `POST /connections/{id}/…/collections/{c}/infer-schema` handles option 4. Both return `{docSchema, warnings[]}` for the UI to apply. When a design is replaced, the mapping's existing field mappings are **re-attached by path** where possible, and orphans are listed for the user.

### Designer UI (left pane of the mapping workbench, also standalone at `…/mappings/{mid}/design`)
```
┌ Document design ─────────────────────────── [Generate ▾] [Import ▾] ─┐
│ customer::{customer_id}                    ⓘ document key (OC-016)   │
│ ▾ { } document                                                       │
│    ├─ customerId      integer   *required         ● mapped           │
│    ├─ ▾ { } name       object                                        │
│    │    ├─ first       string                     ● mapped           │
│    │    └─ last        string                     ○ unmapped          │
│    ├─ email           string  ‹email›             ● mapped           │
│    ├─ ▾ [ ] tags       array<string>              ◐ suggested         │
│    └─ + Add field                                                    │
│  ⤓ Drop a .json sample or JSON Schema anywhere here                  │
└──────────────────────────────────────────────────────────────────────┘
```
- **Tree editor.** Inline rename (double-click or F2), a type dropdown (TypeBadge), a required toggle, and a description in the row's *…* menu. *+ Add field* works at any object level, and there are actions for *Add nested object*, *Add array*, *Wrap in object* (select fields → group into a new object), *Unwrap*, *Duplicate*, and *Delete* (confirmation if mapped). Siblings can be reordered by drag and drop and moved into objects (dnd-kit). Multi-select uses Shift/Cmd-click.
- **Drop zone.** The whole designer accepts drag-and-drop of `.json` files. On drop it sniffs whether the content is a JSON Schema (has `$schema`, or `type` plus `properties`) or a sample, then shows a **replace or merge** dialog with a diff preview: added (green), removed (red), and type-changed (amber) fields.
- **Generate ▾** menu: *From source table* (with naming and nest-by-prefix options, preview before apply) and *From existing documents in collection*.
- **Import ▾** menu: *JSON sample…* (file or paste editor with a JSON linter) and *JSON Schema…*.
- **Toggle *JSON view*.** A read-only pretty sample document built from the design with placeholder values, plus an *Export JSON Schema* download.
- **Undo/redo** (⌘Z / ⇧⌘Z) through a Zustand history middleware with a 100-step limit.
- The mapping status dots on the right are driven by the mapping store (OC-019).

### Frontend state (`src/features/docschema/store.ts`)
- The Zustand store is normalized (`fieldsById`, `childrenById`, `parentById`), so large schemas update in O(1).
- Selectors derive `pathById` and the tree for rendering.
- All ops are pure reducers in `docschemaOps.ts`: `addField`, `rename`, `setType`, `move`, `wrap`, `unwrap`, `remove`, and `replaceSchema(new, strategy)`, so they can be unit-tested without React.

## Acceptance criteria & unit tests

### AC1 — Generate from table
**Given** the `customer` table (customer_id int4 PK NOT NULL, first_name text, billing_street, billing_city, created_at timestamptz), **when** generated with camelCase and nest-by-prefix, **then** the fields are `customerId: integer (required)`, `firstName: string`, `billing: {street, city}`, and `createdAt: datetime (format iso8601)`. With `naming:preserve` the names are identical to the columns.

| Test | Location | Verifies |
|---|---|---|
| `TestFromTable_AC1_CamelCaseAndTypes` | `internal/docschema/generate_test.go` | |
| `TestFromTable_AC1_NestByPrefix` | `internal/docschema/generate_test.go` | Prefix shared by only 1 column → not nested |
| `TestFromTable_AC1_PreserveNaming` | `internal/docschema/generate_test.go` | |
| `TestToCamel_AC1_Cases` | `internal/naming/naming_test.go` | `order_ID2` → `orderId2`, `URL` → `url`, `already_camel` |

### AC2 — Infer from JSON samples
**Given** samples `[{a:1,b:"x"},{a:2.5,c:null},{a:3,b:"y",d:"2024-01-01T00:00:00Z"}]`, **then** `a:number (required)`, `b:string`, `c:any`, `d:datetime`, and none of b, c, d is required. **Given** a nested array of objects, **then** the item schema is the union of the element fields. **Given** input over 5 MB or invalid JSON, **then** a 422 with the parse position.

| Test | Location | Verifies |
|---|---|---|
| `TestInfer_AC2_MergesTypesAndRequired` | `internal/docschema/infer_test.go` | |
| `TestInfer_AC2_ArrayOfObjectsUnion` | `internal/docschema/infer_test.go` | |
| `TestInfer_AC2_NDJSONAndArrayInputs` | `internal/docschema/infer_test.go` | |
| `TestInfer_AC2_DatetimeAndUUIDDetection` | `internal/docschema/infer_test.go` | |
| `TestInfer_AC2_RejectsOversizeAndInvalid` | `internal/docschema/infer_test.go` | Error includes line and column |

### AC3 — Import JSON Schema
**Given** a draft 2020-12 schema with `$defs` / `$ref`, nullable `anyOf`, `required`, and `format: date-time`, **then** the tree mirrors it. **Given** a cyclic `$ref`, **then** a 422 `SCHEMA_REF_CYCLE`. Unsupported keywords (`patternProperties`) produce warnings, not errors.

| Test | Location | Verifies |
|---|---|---|
| `TestFromJSONSchema_AC3_RefsAndNullable` | `internal/docschema/jsonschema_test.go` | |
| `TestFromJSONSchema_AC3_CycleDetected` | `internal/docschema/jsonschema_test.go` | |
| `TestFromJSONSchema_AC3_UnsupportedWarns` | `internal/docschema/jsonschema_test.go` | |
| `TestToJSONSchema_AC3_RoundTrip` | `internal/docschema/jsonschema_test.go` | Export → import yields an equal tree (ignoring IDs) |

### AC4 — Sample existing collection
**Given** server ≥ 7.6, **then** `SamplingScan` is used. **Given** 7.2 with Query, **then** the N1QL fallback is used. **Given** neither, **then** the option is disabled with the reason `SAMPLING_UNAVAILABLE`.

| Test | Location | Verifies |
|---|---|---|
| `TestSampler_AC4_PrefersRangeScan` | `internal/connectors/target/couchbase/sampler_test.go` | Fakes |
| `TestSampler_AC4_FallsBackToQuery` | `internal/connectors/target/couchbase/sampler_test.go` | |
| `TestSampler_AC4_Unavailable` | `internal/connectors/target/couchbase/sampler_test.go` | |

### AC5 — Schema validation
**Given** duplicate sibling names, a name containing `.`, depth 17, an array without items, or more than 2,000 fields, **then** `Validate` returns field-path-specific errors, and the UI marks the offending rows red with messages.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC5_Table` | `internal/docschema/validate_test.go` | |
| `docschemaOps.test.ts` › `AC5: rename rejects duplicate sibling` | `src/features/docschema/docschemaOps.test.ts` | |

### AC6 — Tree editing operations
**Given** the designer, the user can add, rename, retype, reorder, move into an object, wrap selected fields into a new object, unwrap, and delete. Every operation is undoable and redoable. Deleting a mapped field asks for confirmation and removes its mapping.

| Test | Location | Verifies |
|---|---|---|
| `docschemaOps.test.ts` › `AC6: add/rename/setType` | `src/features/docschema/docschemaOps.test.ts` | |
| `docschemaOps.test.ts` › `AC6: move preserves ids and updates paths` | `src/features/docschema/docschemaOps.test.ts` | |
| `docschemaOps.test.ts` › `AC6: wrap and unwrap are inverses` | `src/features/docschema/docschemaOps.test.ts` | |
| `history.test.ts` › `AC6: undo/redo 100-step cap` | `src/features/docschema/history.test.ts` | |
| `DocDesigner.test.tsx` › `AC6: keyboard F2 rename and Delete confirm when mapped` | `src/features/docschema/DocDesigner.test.tsx` | |

### AC7 — Drag-and-drop import with replace or merge
**Given** a `.json` file dropped on the designer, **then** it is classified as a schema or a sample, a diff preview is shown, and the choice between *Replace* and *Merge* (add new fields, keep existing) is applied. Field mappings are re-attached by path, and orphaned mappings are listed.

| Test | Location | Verifies |
|---|---|---|
| `sniffJson.test.ts` › `AC7: detects JSON Schema vs sample` | `src/features/docschema/sniffJson.test.ts` | |
| `schemaDiff.test.ts` › `AC7: added/removed/type-changed` | `src/features/docschema/schemaDiff.test.ts` | |
| `TestReattachMappings_AC7_ByPath` | `internal/mapping/validate_test.go` | Orphans returned |
| `DocDesigner.test.tsx` › `AC7: drop shows diff dialog and applies merge` | `src/features/docschema/DocDesigner.test.tsx` | Simulated DataTransfer |

### AC8 — Performance
**Given** a 1,000-field schema, **then** typing a rename re-renders only the affected row (profiled via render counter), and the tree scrolls virtualized.

| Test | Location | Verifies |
|---|---|---|
| `DocDesigner.test.tsx` › `AC8: rename re-renders single row` | `src/features/docschema/DocDesigner.test.tsx` | Render counter via React Profiler |
