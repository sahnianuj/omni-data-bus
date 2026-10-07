# OC-137 — JavaScript computed fields: sandboxed runtime and helpers

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Computed Fields | P0 | 8 | OC-136 | Not started |

## User story
As a **data engineer**, I want to compute a field with a JavaScript expression over the document's other fields, using the string functions I already know (`concat()`, `substring()`, `slice()`, `replace()`, `padStart()`, `toUpperCase()`, …), conditions and arithmetic, so that I can derive values a template can't express without writing code outside the tool.

## Background
OC-136 adds computed fields and templates. A template can only join values. Customers ask for things like:
- `doc.email.substring(0, doc.email.indexOf("@"))`: the user name of an email
- `doc.firstName.concat(" ", doc.lastName).trim()`
- `doc.phone.replace(/[^0-9]/g, "").slice(-10)`
- `self.qty * self.unitPrice`, `doc.items.length`, `doc.items.reduce((s, i) => s + i.qty, 0)`
- `doc.status === "A" ? "Active" : "Inactive"`
- `String(self.lineNo).padStart(3, "0")`

JavaScript is what Couchbase users write in Eventing functions and JavaScript UDFs, and 4.0 converts PL/SQL to JavaScript UDFs. We use it here too, rather than extending the expr-lang `expression` step (OC-020), which reads source columns only.

## Scope
**In:**
- `computed.lang: "js"`: one JavaScript **expression** (ES2020 syntax supported by the engine: arrow functions, template literals, optional chaining `?.`, nullish coalescing `??`, regex literals, array methods). No statements at the top level. A multi-line expression is allowed (an IIFE such as `(() => { … })()` covers the rare block-bodied case).
- The same names in scope as templates (OC-136): `doc`, `self`, `parent`, `index`, `row`.
- All built-in `String`, `Number`, `Math`, `Array`, `Object`, `JSON` and `RegExp` methods, and a small helper set:

| Helper | Does |
|---|---|
| `coalesce(a, b, …)` | First argument that isn't null or undefined |
| `isBlank(v)` | null, undefined, or a string of whitespace |
| `formatDate(v, pattern, tz?)` | Same friendly patterns as `formatDatetime` (OC-020): `formatDate(doc.createdAt, "yyyy-MM-dd")` |
| `now()` | The job's start time (ISO 8601), the same for every row |
| `hash(v, algo?)` | Hex digest, `sha256` by default (same as the `hash` op) |

- **Deterministic and sandboxed:** no `require`, `import`, `eval`, `Function`, timers, I/O or global state between documents. `Math.random()` is unavailable and `Date.now()` / `new Date()` return the job start time, so re-runs, resume (OC-029) and server-vs-runner parity give the same documents.
- **Limits:** a per-evaluation time budget (`OC_COMPUTED_TIMEOUT_MS`, default 50 ms), a maximum result size (1 MB), and a maximum source length (8 KB). Exceeding one is a row error that follows the field's `onError` (OC-136).
- **Results:** the field gets the JSON value of the result: string, number, boolean, null, array or object. `undefined` omits the field. The result is then converted to the field's declared type by the non-strict converters (OC-020); a value that can't be converted is a row warning `COMPUTED_TYPE_MISMATCH` and the field is null.
- **Number precision:** integers beyond ±2^53−1 and numerics with more than 15 significant digits reach JavaScript as **strings** (the same rule as `bigintAsString`/`numericMode: auto`, OC-020), so no digit is silently lost. A JavaScript number result is written in the shortest round-trip form, and integers without a decimal point.
- **Compile-time checks:** syntax errors with line and column (`COMPUTED_JS_SYNTAX`); disallowed globals (`COMPUTED_JS_FORBIDDEN`: `require`, `eval`, `Function`, `globalThis`, `process`, `setTimeout`, …); and the static path checks of OC-136 for every member chain rooted at `doc`/`self`/`parent`/`row`. Dynamic access (`doc[name]`) is allowed and reported as an info issue (`COMPUTED_DYNAMIC_ACCESS`: "can't be checked or kept up to date on rename").
- The 1.0 expr-lang `expression` step keeps working in existing mappings and stays editable. The step palette offers **JavaScript value** instead of it for new steps (OC-138).

**Out:** statements and functions shared across fields (a library of user functions, possible later story); async code; reading other documents or the network; JavaScript in key templates (see OC-136).

## Implementation details
- **Engine:** `github.com/dop251/goja` (pure Go, MIT, no cgo: fits `CGO_ENABLED=0` and the static `odb` binary). Record the decision as D6 in [00-architecture.md](00-architecture.md).
- **Compile once, run per worker:** each expression is compiled to a `*goja.Program` in `transform.Compile`, wrapped as `(function(doc, self, parent, index, row) { "use strict"; return (<expr>); })`. Each pipeline worker owns one `goja.Runtime` (runtimes aren't goroutine-safe) with the frozen helper globals installed once. Runtimes are pooled per mapping in the preview service.
- **Lazy document views:** `doc`/`self`/`parent` are passed as `goja.DynamicObject`/`DynamicArray` proxies over the Go ordered document. Only the fields an expression touches are converted, and the proxies are read-only (writes throw `TypeError` in strict mode). This keeps the cost proportional to what the expression reads, not to document size.
- **Sandbox:** start from an empty global object and install only the allowed built-ins and helpers; delete `eval`/`Function` constructors; override `Math.random` and `Date` as above. A static check over the goja AST rejects forbidden identifiers at compile time (defence in depth, and a better message).
- **Time budget:** a per-worker watchdog goroutine calls `Runtime.Interrupt` when an evaluation exceeds the budget; `ClearInterrupt` before the next one. No per-evaluation timer allocation.
- **Errors:** a thrown JavaScript error becomes `COMPUTED_ERROR` with the message and the line/column inside the expression; the stack trace is not stored (it can contain values).
- **Data safety:** row errors and preview output must not carry document values beyond what the 1.0 error report already holds (OC-026: error values are truncated to 256 characters and follow its redaction rules).

## Acceptance criteria & unit tests

### AC1 — String functions over target fields
**Given** these expressions on a document with `email: "ada@example.com"`, `firstName: "Ada"`, `lastName: "Lovelace"`, `phone: "+1 (555) 010-2000"`, `lineNo: 7`, **then** the results are:

| Expression | Result |
|---|---|
| `doc.email.substring(0, doc.email.indexOf("@"))` | `"ada"` |
| `doc.firstName.concat(" ", doc.lastName)` | `"Ada Lovelace"` |
| `doc.phone.replace(/[^0-9]/g, "").slice(-10)` | `"5550102000"` |
| `String(doc.lineNo).padStart(3, "0")` | `"007"` |
| `doc.lastName.toUpperCase().split("").reverse().join("")` | `"ECALEVOL"` |
| `` `${doc.firstName[0]}. ${doc.lastName}` `` | `"A. Lovelace"` |

| Test | Location | Verifies |
|---|---|---|
| `TestComputedJS_AC1_StringFunctions` | `backend/internal/transform/computedjs_test.go` | Table-driven |

### AC2 — Numbers, conditions, arrays
**Given** `items` with `qty`/`unitPrice`, **then** `self.qty * self.unitPrice` in an item, `doc.items.reduce((s, i) => s + i.qty, 0)` and `doc.items.length` at the root, and `doc.status === "A" ? "Active" : "Inactive"` evaluate as written. **Given** a `bigint` value 9007199254740993, **then** the expression receives the string `"9007199254740993"`.

| Test | Location | Verifies |
|---|---|---|
| `TestComputedJS_AC2_NumbersConditionsArrays` | `backend/internal/transform/computedjs_test.go` | |
| `TestComputedJS_AC2_BigNumbersArriveAsStrings` | same | |

### AC3 — Results and types
**Given** a result of `undefined`, **then** the field is absent. **Given** an object or array result on an object or array field, **then** it is written as JSON. **Given** a field typed `integer` and a result `"12"`, **then** `12`; a result `"abc"`, **then** null and a `COMPUTED_TYPE_MISMATCH` warning.

| Test | Location | Verifies |
|---|---|---|
| `TestComputedJS_AC3_ResultConversion` | `backend/internal/transform/computedjs_test.go` | |

### AC4 — Sandboxed and deterministic
**Given** `require("fs")`, `eval("1")`, `new Function("return 1")`, `globalThis.x = 1` or `setTimeout`, **then** compilation fails with `COMPUTED_JS_FORBIDDEN`. **Given** `Math.random()`, **then** it isn't a function. **Given** `new Date().toISOString()` on two rows and two runs, **then** both rows and both runs get the job's start time. **Given** an expression that assigns `self.x = 1`, **then** a `TypeError` row error and the document is unchanged.

| Test | Location | Verifies |
|---|---|---|
| `TestComputedJS_AC4_ForbiddenGlobals` | `backend/internal/transform/computedjs_test.go` | |
| `TestComputedJS_AC4_Deterministic` | same | |
| `TestComputedJS_AC4_DocumentIsReadOnly` | same | |

### AC5 — Limits
**Given** `(() => { while (true) {} })()`, **then** the evaluation stops within the budget (+20 ms) with `COMPUTED_TIMEOUT`, the worker carries on with the next row, and `onError` applies. **Given** `"x".repeat(2_000_000)`, **then** `COMPUTED_RESULT_TOO_LARGE`. **Given** a source over 8 KB, **then** a compile issue.

| Test | Location | Verifies |
|---|---|---|
| `TestComputedJS_AC5_Timeout` | `backend/internal/transform/computedjs_test.go` | |
| `TestComputedJS_AC5_ResultAndSourceLimits` | same | |

### AC6 — Compile diagnostics
**Given** `doc.email.substring(0,` , **then** `COMPUTED_JS_SYNTAX` with line and column. **Given** `doc.emial.trim()`, **then** `COMPUTED_UNKNOWN_FIELD` at the path. **Given** `doc[key]`, **then** an info issue `COMPUTED_DYNAMIC_ACCESS`.

| Test | Location | Verifies |
|---|---|---|
| `TestCompile_AC6_ComputedJSIssues` | `backend/internal/transform/compile_test.go` | |

### AC7 — Concurrency and performance
**Given** 8 workers evaluating the same mapping, **then** no runtime is shared between goroutines (race-free under `go test -race` in CI). **Given** the OC-020 AC7 mapping plus 3 JavaScript fields of AC1's kind, **then** ≥ 60k docs/s on one core; a single `doc.a.concat(" ", doc.b)` evaluation allocates ≤ 12 times.

| Test | Location | Verifies |
|---|---|---|
| `TestComputedJS_AC7_RuntimePerWorker` | `backend/internal/transform/computedjs_test.go` | |
| `BenchmarkApply_AC7_ThreeJSFields` | `backend/internal/transform/program_bench_test.go` | Threshold helper |
| `BenchmarkComputedJS_AC7_Concat` | `backend/internal/transform/computedjs_bench_test.go` | `b.ReportAllocs` |
