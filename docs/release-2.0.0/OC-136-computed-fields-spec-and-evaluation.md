# OC-136 — Computed fields: spec, evaluation order and template values

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Computed Fields | P0 | 5 | OC-020, OC-037, OC-047 | Not started |

## User story
As a **data engineer**, I want to add a target field whose value is computed from the document's **other target fields**, for example `fullName` from `firstName` and `lastName`, or `label` from `product.sku` and `product.name`, so that the document carries derived values my application needs and that no source column holds.

## Background (gap found 2026-10-06)
- A target field gets its value from source columns plus a transform pipeline (OC-020). The `constant` op sets a fixed value, and the `expression` op (expr-lang) can combine **source columns** (`row.first_name + " " + row.last_name`).
- Nothing can read the **target document**: values after their transforms, embedded objects (`product.name`), array items, aggregates or other computed fields. A user has to know which source column feeds each field, and fields assembled from relations (OC-043/044/063) aren't reachable from `row` at all.
- The expression helpers are limited to `upper, lower, trim, len, round, coalesce, now, concat`: no `substring`, `replace`, `indexOf` or padding. The language isn't JavaScript either, which is what Couchbase users write in Eventing and UDFs. OC-137 adds JavaScript.

This story adds the model, the evaluation order and the first, simplest kind of computed value: a **template**. OC-137 adds JavaScript expressions on top of the same model.

## Scope
**In:**
- A computed field: a `fieldMappings` entry with no sources and a `computed` rule (`lang: template | js`). Any target field can be computed: at the root, in an embedded object, or in an array's items, in canvas and composed mappings alike.
- **Templates:** the body of a JavaScript template literal, such as `${doc.firstName} ${doc.lastName}` or `ORD-${self.orderId}`. Only `${path}` substitutions are allowed (member paths, no calls or operators). The result is always a string. A template can be turned into a JavaScript expression 1:1 by wrapping it in backticks (OC-138).
- **Names in scope:**

| Name | Is | Example |
|---|---|---|
| `doc` | The root document as it will be written | `doc.customer.name` |
| `self` | The object holding the computed field: the root, an embedded object or the current array item | `self.qty` in `items[]` |
| `parent` | The object holding `self` (undefined at the root) | `parent.orderId` from an item |
| `index` | The 0-based position of `self` in its nearest array (undefined outside arrays) | `${index}` |
| `row` | The source row of the field's scope, every column, mapped or not (read-only) | `row.created_by` |

- **Evaluation order.** After the document is assembled (relations, aggregates, array shapes and filters, OC-043–OC-046, OC-063) and before null handling, the size guard and overflow handling (OC-046). Computed fields run in dependency order: a field that reads another computed field runs after it. Deeper fields run before shallower ones when a shallower field reads a whole container (`doc.items`).
- **Static checks at compile time** (`transform.Compile` → validation issues with the field path, line and column):
  - `COMPUTED_UNKNOWN_FIELD`: a path that isn't in the document design (`doc.fistName`).
  - `COMPUTED_CYCLE`: `a` reads `b`, which reads `a`. The issue lists the cycle.
  - `COMPUTED_OUT_OF_SCOPE`: `self`/`parent`/`index` where they don't exist, or `row.<column>` for a column the field's scope doesn't have.
  - `COMPUTED_TEMPLATE_SYNTAX`: anything other than a path inside `${…}`, with "Use JavaScript" as the fix (OC-138).
- **Values:** a missing field or `null` renders as an empty string in a template. An option `nullIfAnyMissing` makes the whole template `null` instead, so null handling (`omit`) can drop it. The field's own transform pipeline (OC-020) runs after the computed value (e.g. template → `upper`).
- The same evaluation in the pipeline, the preview (OC-022/049) and `odb run` (OC-051), because all three run the shared execution plan (OC-037).

**Out:** JavaScript expressions (OC-137); the editor UI (OC-138); rename and delete propagation (OC-139); computed values in the **document key** template (keys stay source-based: a computed key would break resume and directory look-up by key; possible later story); computed fields that write to other fields; reading other documents.

## Target shape example
```json
{
  "orderId": 1001,
  "orderRef": "ORD-1001",                              // template: ORD-${self.orderId}
  "customer": { "firstName": "Ada", "lastName": "Lovelace",
                "fullName": "Ada Lovelace" },          // template: ${self.firstName} ${self.lastName}
  "items": [
    { "lineNo": 1, "sku": "A-1",
      "product": { "name": "Pen" },
      "label": "1 · A-1 Pen" }                         // template: ${self.lineNo} · ${self.sku} ${self.product.name}
  ]
}
```

## Implementation details
- **Spec (`mapping.FieldMapping`):** `computed: { lang: "template" | "js", expr: string, nullIfAnyMissing?: bool, onError?: "fail-row" | "null" | "omit" }`. A mapping with `computed` must have `sources: []` and no `constant`/`expression` step (`COMPUTED_WITH_SOURCES`). Update `api/bundle.schema.json` and the OpenAPI mapping schema, keep v1 and existing v2 specs readable unchanged (2.0 DoD).
- **Path analysis (`internal/transform/computed`):** a shared parser for template substitutions and (OC-137) JavaScript member chains rooted at `doc`/`self`/`parent`/`row`. It returns the referenced field ids (resolved against `docSchema`, so references survive renames in OC-139). Computed paths in arrays use `[]`: `doc.items[].sku` is a dependency on the `sku` field of every item.
- **Dependency graph:** nodes are target field ids; edges from each computed field to the fields it reads, plus an edge from a container to every computed field under it. Topological order is computed once in `Compile`; a cycle is a compile issue and the mapping is invalid (MAPPING_INVALID gate, OC-049).
- **Execution:** after `Program.Apply` builds the ordered document (OC-020 AC6), a `computedPass` walks the plan's computed steps in order, resolving `self`/`parent`/`index` from the item being built. Template evaluation is native Go (pre-split literal parts and path getters), with no JavaScript runtime, so templates keep the hot path fast.
- **Row errors:** `COMPUTED_ERROR {fieldPath, code, message}` follows the field's `onError`: `fail-row` (default; counts toward the job's error threshold, OC-026), `null`, or `omit`. Templates can't fail at runtime except on the size limit (a result > 1 MB → `COMPUTED_RESULT_TOO_LARGE`).
- **Score and validity:** a valid computed field counts as mapped (OC-018 score, OC-049 coverage); an invalid one counts as unmapped and shows its issue.

## Acceptance criteria & unit tests

### AC1 — Template from other target fields
**Given** `customer.firstName` ← `first_name`, `customer.lastName` ← `upper(last_name)` and a computed `customer.fullName` = `${self.firstName} ${self.lastName}`, **then** the document has `"fullName": "Ada LOVELACE"` (it reads target values after their transforms).

| Test | Location | Verifies |
|---|---|---|
| `TestComputed_AC1_TemplateReadsTargetValues` | `backend/internal/transform/computed_test.go` | |

### AC2 — Scope names in arrays and embedded objects
**Given** `items[]` (o2m) with `product` (m2o under it) and a computed `items[].label` = `${index} ${self.sku} ${self.product.name} ${parent.orderId}`, **then** each item's label uses its own row, product and position, and the root's `orderId`.

| Test | Location | Verifies |
|---|---|---|
| `TestComputed_AC2_SelfParentIndexInArrays` | `backend/internal/transform/computed_test.go` | |
| `TestComputed_AC2_GroupedRows` | same | Items of a grouped document (OC-063) |

### AC3 — Dependency order and cycles
**Given** `b` = `${doc.a}!` and `a` = `${doc.x}`, both computed, **then** `a` is evaluated first. **Given** a root `summary` = `${doc.items}` style dependency on a container with computed item fields, **then** the items' fields are evaluated first. **Given** `a` reads `b` and `b` reads `a`, **then** `Compile` returns `COMPUTED_CYCLE` naming both paths and the job can't start (422 MAPPING_INVALID).

| Test | Location | Verifies |
|---|---|---|
| `TestComputed_AC3_TopologicalOrder` | `backend/internal/transform/computed_test.go` | |
| `TestComputed_AC3_ContainerDependsOnInnerComputed` | same | |
| `TestCompile_AC3_ComputedCycle` | `backend/internal/transform/compile_test.go` | |

### AC4 — Static validation
**Given** `${doc.fistName}`, **then** `COMPUTED_UNKNOWN_FIELD` at line 1, column 3. **Given** `${parent.x}` on a root field, **then** `COMPUTED_OUT_OF_SCOPE`. **Given** `${doc.a + 1}`, **then** `COMPUTED_TEMPLATE_SYNTAX`. **Given** a computed mapping that also has sources, **then** `COMPUTED_WITH_SOURCES`.

| Test | Location | Verifies |
|---|---|---|
| `TestCompile_AC4_ComputedIssues` | `backend/internal/transform/compile_test.go` | Table-driven, issue codes and positions |

### AC5 — Nulls and errors
**Given** `lastName` is null, **then** the template renders `"Ada "`; with `nullIfAnyMissing` the field is null, and with null handling `omit` it is absent. **Given** a result over 1 MB, **then** the row follows `onError` (`fail-row` → row error `COMPUTED_RESULT_TOO_LARGE`; `null`; `omit`).

| Test | Location | Verifies |
|---|---|---|
| `TestComputed_AC5_NullRendering` | `backend/internal/transform/computed_test.go` | |
| `TestComputed_AC5_OnErrorPolicies` | same | |

### AC6 — Same result everywhere
**Given** a golden mapping with computed fields, **then** preview, the server pipeline and `odb run` produce byte-identical documents, for the Couchbase and directory targets alike.

| Test | Location | Verifies |
|---|---|---|
| `TestParity_AC3_GoldenMappings` (adds `computed-template.json`) | `backend/internal/headless/parity_integration_test.go` | |
| `TestPreview_AC6_ComputedFields` | `backend/internal/preview/composed_integration_test.go` | |

### AC7 — No cost when unused, small cost when used
**Given** the OC-020 AC7 benchmark (25 fields, no computed fields), **then** throughput and allocations are unchanged (±3%). **Given** the same mapping plus 3 template fields, **then** throughput stays ≥ 250k rows/s on one core.

| Test | Location | Verifies |
|---|---|---|
| `BenchmarkApply_AC7_25Fields` (unchanged gate) | `backend/internal/transform/program_bench_test.go` | |
| `BenchmarkApply_AC7_ThreeTemplates` | same | |
