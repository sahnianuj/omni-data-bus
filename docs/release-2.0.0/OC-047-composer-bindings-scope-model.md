# OC-047 — Document composer: bindings, scope rules & plan compilation

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P0 | 8 | OC-037, OC-043, OC-044 | Not started |

## User story
As a **data engineer**, I want to define my target JSON structure first and then say which source table or relation feeds each object and array in it, so that I can produce exactly the document my application expects, whatever the source schema looks like.

## Scope
**In:**
- `mode: "composed"` mappings: the document is designed target-first, and each object or array node can be **bound** to a relation.
- A choice of **driving table** (one document per row), which can be any table or view, not only the table picked in the Tables step.
- Scope rules: which source columns each field may read. These are enforced in validation and used by the UI to filter mappable columns.
- Binding rules for each kind of node, including "array of one" and "first of many" adapters.
- Computed fields: constants and expressions across in-scope columns (OC-020 expressions).
- Compilation into the shared execution plan (OC-037), with no composer-specific runtime.

**Out:** the UI (OC-048); preview, validation UX and scoped auto-map (OC-049); several documents per source row (fan-out), and several driving tables per document (union), both post-2.0.

## Implementation details

### Bindings
A composed mapping is a normal v2 spec with `mode: "composed"`. A binding is simply a relation whose `targetFieldId` is the bound node. Composer-only metadata lives on the node in `docSchema`:
```json
{ "id": "fld_items", "name": "items", "type": "array",
  "binding": { "relationId": "rel_items", "adapter": null },
  "items": { "id": "fld_item", "type": "object", "children": [ … ] } }
```

| Node | Bound to | Adapter | Result |
|---|---|---|---|
| object | many-to-one / one-to-one | — | Nested object (null or omitted if no row) |
| object | one-to-many / many-to-many | `first` (with orderBy) | First related row as an object; the warning "only the first of N rows is used" appears in the preview when N > 1 |
| array | one-to-many / many-to-many | — | Array of items (shapes per OC-045) |
| array | many-to-one | `wrap` | 0- or 1-item array |
| object | *(unbound)* | — | Grouping only: its fields share the parent's scope (for example `shipping: {street, city}` from root columns) |
| array | *(unbound)* | — | Literal array built from explicit element mappings, such as `phones: [home_phone, mobile_phone]`; nulls dropped when `dropNulls` |

### Scope rule (`internal/mapping/scope.go`)
`Scope(field)` = the root table + every relation bound on the path from the root to the field + the lookup-only relations attached to any of them.
- A mapping source outside the field's scope → `SOURCE_OUT_OF_SCOPE`, with the message "items[].productName reads products.name, but products isn't bound on the path to this field; bind it under items or add a lookup."
- The UI uses the same function (TS mirror, shared fixtures) to list only mappable columns in the inspector.

### Driving table
- `root.table` can be changed in composed mode. Changing it re-validates every binding: relations whose parent join no longer resolves are flagged, not deleted.
- The Tables step lists composed mappings by their driving table with a "Composed" badge.

### Computed fields
- `constant` (typed literal) and `expression` (OC-020 `expr` over in-scope columns, with `$root` and `$parent`) mappings.
- Expressions referencing columns of several relations in scope are allowed, for example `concat($root.order_no, "-", items.line_no)`.

### Compilation
- `plan.Compile` treats bindings exactly like canvas relations. The composer adds no runtime code, which keeps runner parity (D3).
- The plan hash of a composed mapping and of an equivalent canvas mapping is identical.

## Acceptance criteria & unit tests

### AC1 — Binding kinds
**Given** each row of the bindings table, **then** the compiled document matches the expected golden output, including the `first` and `wrap` adapters.

| Test | Location | Verifies |
|---|---|---|
| `TestComposed_AC1_BindingMatrix` | `internal/transform/composed_test.go` | Golden per row |

### AC2 — Scope enforcement
**Given** `items[].productName ← products.name` with `products` bound under `items`, **then** valid. **Given** `products` not bound, **then** `SOURCE_OUT_OF_SCOPE`. **Given** a root-level field reading an items column, **then** `SOURCE_OUT_OF_SCOPE`.

| Test | Location | Verifies |
|---|---|---|
| `TestScope_AC2_Rules` | `internal/mapping/scope_test.go` | |
| `scope.test.ts` › `AC2: matches server fixtures` | `src/features/composer/scope.test.ts` | Shared `testdata/scope-cases.json` |

### AC3 — Grouping and literal arrays
**Given** an unbound `shipping` object with `street ← ship_street`, **then** `shipping.street` is filled from the root row. **Given** `phones: [home_phone, mobile_phone]` with `dropNulls` and a null mobile, **then** `phones: ["…"]`.

| Test | Location | Verifies |
|---|---|---|
| `TestComposed_AC3_GroupingAndLiteralArrays` | `internal/transform/composed_test.go` | |

### AC4 — Driving table change
**Given** a composed mapping driven by `orders`, **when** it is changed to `order_items`, **then** relations whose parent join columns don't exist on `order_items` are flagged `BINDING_BROKEN`, and none are removed.

| Test | Location | Verifies |
|---|---|---|
| `TestComposed_AC4_ChangeDrivingTable` | `internal/mapping/composed_test.go` | |

### AC5 — Same plan as canvas
**Given** a composed mapping and a canvas mapping that describe the same document, **then** their plan hashes are equal and their output is byte-identical.

| Test | Location | Verifies |
|---|---|---|
| `TestCompile_AC5_ComposedEqualsCanvas` | `internal/plan/compile_test.go` | |

### AC6 — Cross-scope expressions
**Given** `items[].ref ← concat($root.order_no, "-", line_no)`, **then** each item has `ref: "SO-1001-1"`, etc. **Given** an expression referencing an out-of-scope column, **then** `SOURCE_OUT_OF_SCOPE` at the expression's position.

| Test | Location | Verifies |
|---|---|---|
| `TestComposed_AC6_CrossScopeExpression` | `internal/transform/composed_test.go` | |
