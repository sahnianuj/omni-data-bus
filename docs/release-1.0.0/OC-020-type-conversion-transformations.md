# OC-020 — Type conversion & transformation library

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E4 Design & Map | P0 | 8 | OC-010, OC-015 | Implemented |

## User story
As a **data engineer**, I want source values converted to correct JSON types by default, and I want to apply transformations (trim, concat, date formats, lookups, expressions) to a mapping, so that the documents in Couchbase are clean and shaped the way my application expects.

## Implementation details

### Two layers (`internal/transform`)
1. **Encoders** turn a pgx-decoded Go value into a canonical JSON-ready value (`any`: nil, bool, int64, float64, json.Number, string, `map[string]any`, `[]any`) according to [architecture §6.4](00-architecture.md#64-logical-type-normalization-postgresql--jsontype). Rows are decoded with pgx's type map, and one encoder per column is chosen at compile time by OID.
   - `numeric`: pgx `pgtype.Numeric` → `json.Number` when precision ≤ 15, otherwise a string. Options: `numericMode: auto | number | string`.
   - `int8`: int64. Option `bigintAsString` when |v| > 2^53−1: auto (per value) | always | never.
   - `timestamptz` → RFC 3339 UTC with `Z`, microsecond precision trimmed of trailing zeros. `timestamp` → no zone (option `assumeTimezone: "UTC"` appends `Z`). `date` → `YYYY-MM-DD`.
   - `bytea` → std base64. `uuid` → lowercase string. `json`/`jsonb` → decoded into `any` (uses `json.Decoder.UseNumber` to preserve precision). `interval` → ISO-8601 duration (`P1Y2M3DT4H5M6S`).
   - Arrays → `[]any` of the element encoder. NaN/Inf → nil with a row warning `NON_FINITE_NUMBER`.
2. **Transform steps**, an ordered pipeline per field mapping, applied after encoding:

| Op | Args | Behaviour |
|---|---|---|
| `toString` | — | Canonical string form |
| `parseInteger` / `parseNumber` / `parseBoolean` | `{strict:bool}` | Parse strings (`"1"`, `"true"`, `"yes"`, `"Y"` for bool). A strict failure is a row error; non-strict gives null plus a warning |
| `toDatetime` | `{inputFormat?, timezone?}` | Parse strings or epochs into datetime |
| `formatDatetime` | `{format: iso8601\|date\|epoch_ms\|epoch_s\|custom, pattern?, timezone?}` | Custom uses Go layout converted from a friendly pattern (`yyyy-MM-dd HH:mm`) |
| `trim` / `lower` / `upper` | — | Strings only (non-strings pass through) |
| `substring` | `{start, length}` | Rune-based |
| `replace` | `{pattern, replacement, regex:bool}` | Regex via RE2 (`regexp`), compiled once |
| `concat` | `{separator, skipNulls:true}` | Joins all sources (multi-source mappings) |
| `split` | `{separator, trim:true}` | String → array of strings |
| `default` | `{value}` | Replace null with a value |
| `nullIf` | `{value}` | Value → null when it equals the arg |
| `constant` | `{value}` | Ignores sources (mapping has 0 sources) |
| `lookup` | `{map:{…}, fallback: keep\|null\|value}` | Static value mapping, e.g. `A→"Active"` |
| `round` | `{digits}` | Numbers |
| `jsonParse` | — | A string containing JSON → the value |
| `mask` | `{keepLast:4, char:"*"}` | PII masking |
| `hash` | `{algo: sha256}` | Hex digest (pseudonymization) |
| `expression` | `{expr}` | `expr-lang/expr`. Env: `value`, `row` (map of encoded columns), and helpers (`upper`, `lower`, `now` fixed to job start, `coalesce`). Compiled with `expr.Env(env)`, `expr.AsAny()`, and `expr.Optimize(true)`. There is no I/O, and runaway programs are prevented by `expr.MaxNodes(500)` and a per-evaluation budget |

- Per-field **nullHandling override** (`include | omit`), inheriting the spec default.
- **Compilation:** `transform.Compile(spec, table) (*Program, []Issue)` resolves column indexes, encoders, and steps into a flat slice of closures per target field, plus the key renderer (OC-016) and the doc skeleton builder. Validation issues (bad regex, bad expression, type mismatch) are reported **at compile time** with the field path, and are also used by OC-022 validation.
- **Execution:** `Program.Apply(row []any, buf *DocBuffer) (key string, doc []byte, warnings []Warning, err *RowError)`.
  - Builds the nested document into a reusable ordered map structure, then serializes with a pooled encoder. Field order follows the design order, for readable documents.
  - Uses `github.com/goccy/go-json` (or `encoding/json/v2` once stable) for speed. The serializer must preserve `json.Number`.
  - No reflection in the hot path.
- **Row errors** carry `{fieldPath, op, code, message}`. Codes: `PARSE_FAILED`, `TYPE_MISMATCH`, `EXPR_ERROR`, `KEY_*` (OC-016), `DOC_TOO_LARGE` (> 20 MB serialized).

### UI (Inspector transform editor)
- A pipeline list of steps as cards, reorderable by drag. *+ Add step* opens a searchable palette grouped by category (Convert, Text, Date/time, Values, Privacy, Advanced). Each op has a small typed form (zod schema per op).
- The expression step uses a Monaco-lite editor (CodeMirror 6) with autocomplete for `value`, `row.<column>`, and helpers, and shows inline errors from server compile validation.
- The live before/after preview comes from OC-019 AC8.
- Suggested conversions surface as one-click chips: e.g., for a `timestamptz → integer` mapping: *"Convert to epoch ms?"*.

## Acceptance criteria & unit tests

### AC1 — Default encoders per PostgreSQL type
**Given** each type in §6.4 with representative values (including edge cases: numeric(38,10), int8 max, NaN, bytea empty, timestamptz with a non-UTC offset, interval with months, int4[] with nulls, jsonb with large numbers), **then** the encoded JSON matches the golden file.

| Test | Location | Verifies |
|---|---|---|
| `TestEncoders_AC1_Golden` | `internal/transform/encode_test.go` | `testdata/encoders.golden.json` |
| `TestEncoders_AC1_BigintAsStringModes` | `internal/transform/encode_test.go` | |
| `TestEncoders_AC1_NumericModes` | `internal/transform/encode_test.go` | |
| `TestEncoders_AC1_JSONBPreservesPrecision` | `internal/transform/encode_test.go` | `12345678901234567890` stays exact |
| `TestEncoders_AC1_Integration` *(integration)* | `internal/transform/encode_integration_test.go` | Values selected from real PG with pgx → golden |

### AC2 — Transform ops behave as specified
**Given** each op with table-driven cases (null inputs, non-string inputs, unicode, strict vs non-strict), **then** the outputs and errors match.

| Test | Location | Verifies |
|---|---|---|
| `TestOps_AC2_<Op>` (one per op: `ToString`, `ParseInteger`, `ParseNumber`, `ParseBoolean`, `ToDatetime`, `FormatDatetime`, `Trim`, `Lower`, `Upper`, `Substring`, `Replace`, `Concat`, `Split`, `Default`, `NullIf`, `Constant`, `Lookup`, `Round`, `JSONParse`, `Mask`, `Hash`) | `internal/transform/ops_test.go` | |
| `TestFormatDatetime_AC2_FriendlyPatternConversion` | `internal/transform/ops_test.go` | `yyyy-MM-dd'T'HH:mm` → Go layout |

### AC3 — Pipeline composition & null handling
**Given** `[trim, nullIf(""), default("n/a"), upper]` on `"  "`, **then** the output is `"N/A"`. **Given** nullHandling `omit` and a null result, **then** the field is absent from the document; with `include` it is `null`. Nested objects whose children are all omitted are omitted too.

| Test | Location | Verifies |
|---|---|---|
| `TestPipeline_AC3_Composition` | `internal/transform/program_test.go` | |
| `TestApply_AC3_NullHandlingIncludeOmit` | `internal/transform/program_test.go` | |
| `TestApply_AC3_EmptyObjectOmitted` | `internal/transform/program_test.go` | |

### AC4 — Expressions are sandboxed and validated
**Given** `value * 1.2`, **then** it evaluates. **Given** `row.first_name + " " + row.last_name`, **then** it works for multi-column access. **Given** a syntax error or an unknown identifier, **then** compile-time issues carry a position. **Given** an expression that exceeds MaxNodes, **then** compilation is rejected. There are no filesystem, network, or reflection builtins.

| Test | Location | Verifies |
|---|---|---|
| `TestExpression_AC4_Evaluates` | `internal/transform/expr_test.go` | |
| `TestExpression_AC4_CompileErrors` | `internal/transform/expr_test.go` | |
| `TestExpression_AC4_MaxNodes` | `internal/transform/expr_test.go` | |
| `TestExpression_AC4_NoDangerousBuiltins` | `internal/transform/expr_test.go` | Env allow-list asserted |
| `TestExpression_AC4_NowIsJobStart` | `internal/transform/expr_test.go` | Deterministic across rows |

### AC5 — Compile-time validation
**Given** a spec with a bad regex, a `lookup` without a map, `concat` with one source, or a type-incompatible pipeline end (e.g., the final type is array but the target is string), **then** `Compile` returns issues keyed by field path and op index.

| Test | Location | Verifies |
|---|---|---|
| `TestCompile_AC5_Issues` | `internal/transform/compile_test.go` | Table-driven |

### AC6 — Document assembly & limits
**Given** a design with nested objects and arrays, a type field, and a key template, **then** `Apply` returns the correct key and a JSON document with fields in design order. **Given** a serialized doc > 20 MB, **then** a `DOC_TOO_LARGE` row error.

| Test | Location | Verifies |
|---|---|---|
| `TestApply_AC6_NestedDocumentGolden` | `internal/transform/program_test.go` | |
| `TestApply_AC6_FieldOrderFollowsDesign` | `internal/transform/program_test.go` | |
| `TestApply_AC6_DocTooLarge` | `internal/transform/program_test.go` | |

### AC7 — Hot-path performance
**Given** a 25-field mapping with 5 transform steps, **then** `Apply` runs at ≥ 300k rows/s on one core with ≤ 3 allocations per row (beyond the output buffer).

| Test | Location | Verifies |
|---|---|---|
| `BenchmarkApply_AC7_25Fields` | `internal/transform/program_bench_test.go` | `b.ReportAllocs` with the threshold helper |

### AC8 — Transform editor UI
**Given** the Inspector, the user can add, reorder, configure, and remove steps. Invalid args show inline zod errors. Expression errors from the server display at the right position. Suggested conversion chips add the proper step.

| Test | Location | Verifies |
|---|---|---|
| `TransformPipelineEditor.test.tsx` › `AC8: add/reorder/remove steps` | `src/features/mapping/TransformPipelineEditor.test.tsx` | |
| `opSchemas.test.ts` › `AC8: zod schema per op validates args (parity with the Go compilers)` | `src/features/mapping/opSchemas.test.ts` | Parity with Go arg validation via a shared fixture |
| `ExpressionEditor.test.tsx` › `AC8: shows server diagnostics` | `src/features/mapping/ExpressionEditor.test.tsx` | |
| `TransformPipelineEditor.test.tsx` › `AC8: suggestion chip adds step` | `src/features/mapping/TransformPipelineEditor.test.tsx` | |
