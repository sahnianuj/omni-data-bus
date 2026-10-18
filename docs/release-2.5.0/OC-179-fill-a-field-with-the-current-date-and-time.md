# OC-179 — Fill a field with the current date and time, in the format it needs

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 Mapping Canvas | P1 | 3 | OC-020, OC-166, OC-178 | Implemented |

## User story
As a **data engineer** completing a document, I want to fill a field that has no source column, such as `updatedAt` or `migratedAt`, with the current date and time, written as epoch milliseconds, epoch seconds, ISO 8601 or the same format as the document's other dates, so that the customer's design is complete without inventing a column or writing an expression.

## Background (gap found 2026-10-17)
Designs often have a timestamp that no source column holds. In the cart design, `updatedAt` has no column in `pos.carts` and is left unresolved, and the user can only mark it not populated (OC-166).

The engine can already produce the value, but only through an expression:
- `now()` (OC-020 AC4) returns the job's start time as ISO 8601, the same for every row.
- A `formatDatetime` step turns it into `epoch_ms`, `epoch_s`, `date` or a custom pattern, in any time zone.

Building that requires knowing both the expression language and the step, and nothing checks that the format suits the field's type: ISO text into an `integer` field fails only at run time.

## Scope
**In:**
- **A value source: Current date and time.**
  - **Where:** the value-source choice in the Canvas Inspector (`ValueSourceControl`) and in the Composer Inspector gains *Current date and time*, next to *Source column*, *Fixed value* and *Template*.
  - **Fields it's offered on:** scalar fields (`string`, `integer`, `number`, `any`); not objects, arrays or booleans.
  - **What it replaces:** choosing it replaces what filled the field (a mapping or a *not populated* mark), in one undo step.
- **Which time:**
  - **When the run started** (default): every document of the run gets the same time, as `now()` does. It identifies the run, and repeated previews agree.
  - **When the document is written:** each document gets its own time. That's useful for incremental runs and CDC, where `updatedAt` should move on each write.
- **Format:**
  - **Same as `<field>`:** the format of another date field in the same document, for example *Same as createdAt (epoch ms)*. It's offered when the document has one, and it is the default for such a document. The format is copied from that field's `formatDatetime` step, its design `format`, or its example, so the dates in one document match. This is "whatever format the source dates use".
  - **Epoch milliseconds**, **Epoch seconds**, **ISO 8601** (UTC, `2026-10-17T14:03:22.418Z`), **Date** (`2026-10-17`) or **Custom** (`yyyy-MM-dd HH:mm`).
  - Except for epochs, the formats take a **time zone**, as `formatDatetime` does (default UTC).
  - **Default when nothing matches:** epoch milliseconds for an `integer` or `number` field; ISO 8601 for a `string` field, or the design's `format` (`date-time` → ISO 8601, `date` → Date). An example in the design (`1714557600000`, `"2024-05-01T10:00:00Z"`) picks its format.
  - **Live sample:** a sample of the value shows next to the choice and updates as the format changes.
- **Checked against the field's type.** Text formats into a numeric field, or a fractional epoch into an `integer`, are a mapping error, `NOW_FORMAT_TYPE`. The fix changes the format to one that fits the field.
- **Shown like any other mapping:**
  - **Canvas:** the field's row shows a clock badge (*now · epoch ms*) and no line, since no column is read.
  - **Composer:** the tree row reads *now (epoch ms)*.
  - **Preview and sample:** they show the preview's time.
  - **Coverage sheet** (OC-169): *current date and time (when the run started, epoch ms)*.
- **It counts as done** (OC-178) and runs the same everywhere: in the migration job, the exported bundle and `odb`.
- **Mapping row:** `sources: []` and one step, `{ op: "now", args: { at: "run" | "write", format, pattern?, timezone? } }`.
  - A server-side op beside `formatDatetime`, whose formatting it reuses.
  - `at: "write"` reads the clock when each document is written.
  - Old specs need no migration. Bundles that use it need `odb` 2.5, like the rest of 2.5 (`minWorkerVersion`).

**Out:**
- **Proposing it from the design:** for example, *"set at write time"* in a field's description (an OC-166 hint). That's a follow-up once the value source exists.
- **Dates relative to now** (`now + 30 days`): use an expression.
- **Keeping the first value on re-runs** (a `createdAt` set once): an upsert overwrites it, as every other mapping does.

## Implementation details
- **Backend:**
  - `transform/datetime.go` › `compileNow(args, env)` reuses `compileFormatDatetime`'s format, pattern and location handling. `at: "run"` returns `env.Now` (the job start, `Options.Now`); `at: "write"` reads `time.Now()` on each call.
  - The op is registered with the steps, its arguments are validated in `mapping/validate.go`, and `NOW_FORMAT_TYPE` is checked against the field's type.
  - The OpenAPI `TransformStep.op` documentation gains `now`.
- **Formats from the document:** `docschema/hints.go` › `dateFormatOf(field)` handles the design's `format` and example. `mapping` › `documentDateFormats(spec)` handles the other fields' `formatDatetime` steps. The web mirrors both (`mappingOps.ts` › `dateFormats`).
- **Web:**
  - `mappingOps.ts` › `setNow(spec, fieldId, { at, format, pattern, timezone })`, and `valueSourceOf` returns `'now'`.
  - The `NowSourceEditor` component is used by both inspectors. Its live sample is computed in the browser with the same format rules (`formatSample`).
  - The badge goes in `MappingCanvas` (`relationBadge`'s slot), and `DocumentTree` shows the row's source text.
- **Headless and export:** nothing changes beyond the op itself; the runner compiles the same program (parity test: `headless/parity_integration_test.go`).

## Acceptance criteria & unit tests

### AC1 — Choose it, with a format that fits
**Given** the cart mapping with `updatedAt` (`integer`) unmapped and `createdAt` mapped with `formatDatetime epoch_ms`, **when** the user selects `updatedAt` and picks *Current date and time*, **then**:
- the format defaults to *Same as createdAt (epoch ms)*;
- the sample shows a 13-digit number;
- the field counts as done.

**Given** a `string` field with design format `date-time` and no other date field, **then** the default is ISO 8601 UTC.

| Test | Location | Verifies |
|---|---|---|
| `nowSource.test.ts` › `OC-179` | `web/src/features/mapping/nowSource.test.ts` | `setNow`, `dateFormats`, `defaultNow`, the sample's formats, `fits` |
| `Inspector.test.tsx` › `OC-179 AC1` | `web/src/features/mapping/Inspector.test.tsx` | Canvas: choice, default, sample |
| `Inspector.test.tsx` › `OC-179 AC1` | `web/src/features/composer/Inspector.test.tsx` | Composer: the same |

### AC2 — Written as chosen, at the chosen time
**Given** `at: "run"`, **then** every document of a run carries the job's start time in the chosen format. **Given** `at: "write"`, **then** two documents written a moment apart differ. The formats `epoch_ms`, `epoch_s`, `iso8601`, `date` and `custom` with a time zone match `formatDatetime`'s output for the same instant.

| Test | Location | Verifies |
|---|---|---|
| `TestNow_OC179_RunAndWrite` | `backend/internal/transform/datetime_test.go` | Same per run; differs per write; formats as `formatDatetime` |
| `TestNow_OC179_InADocument` | `backend/internal/transform/datetime_test.go` | A sourceless `now` mapping compiles and writes the run's start in every document |

### AC3 — A format that doesn't fit the field is an error, with a fix
**Given** `updatedAt` is `integer` and the format is ISO 8601, **then** validation reports `NOW_FORMAT_TYPE` (error) with *Use epoch milliseconds*. Applying it fixes the mapping.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_OC179_NowFormatType` | `backend/internal/mapping/validate_test.go` | Both directions; the fix keeps `at` (and the time zone where it applies) |
| `fixes.test.ts` › `OC-179 AC3` | `web/src/features/mapping/fixes.test.ts` | `replace-step` |

### AC4 — Shown where mappings are shown
**Then:**
- the Canvas row shows *now · epoch ms* and no line;
- the Composer tree row reads *now (epoch ms)*;
- the coverage sheet says *current date and time (when the run started, epoch ms)*;
- the preview shows a value.

| Test | Location | Verifies |
|---|---|---|
| `MappingCanvas.test.tsx` › `OC-179 AC4` | `web/src/features/mapping/MappingCanvas.test.tsx` | Badge and its title; no line |
| `TestSheet_OC179_Now` | `backend/internal/mapping/validate_test.go` | The source column of the sheet |

## Implementation notes (2026-10-18)
- **Backend:**
  - `transform/datetime.go`: `compileNow`. Formatting moved into `dateFormatter`, which `formatDatetime` now shares, so the two write the same value for the same instant. `at: "run"` formats `Options.Now` once at compile time; `at: "write"` reads the clock per document.
  - `now` is a sourceless op, like `constant`. A pipeline ending in `now` skips the compiler's `TYPE_MISMATCH`: validation reports `NOW_FORMAT_TYPE` instead, with its fix (`replace-step`, added to the OpenAPI `Fix.kind` enum).
  - Text formats into a `boolean`, object or array field are an error with no fix (the UI doesn't offer *Now* there).
  - `mapping.DescribeNow` gives the wording for the duplicate-target message (OC-176) and the coverage sheet.
  - `RequiresV25` lists *current date and time*, so 2.0 workers leave such jobs alone during a rolling upgrade.
  - `op-args.json` (shared with the web) gained the `now` cases.
- **Web:**
  - `mapping/nowSource.ts` holds `setNow`, `nowOf`, `dateFormats`, `defaultNow`, `fits`, `formatSample` and `describeNowText`. They live in their own module rather than in `mappingOps.ts`.
  - `formatSample` mirrors Go's RFC 3339 output: a trailing-zero-free fraction, `Z` in UTC, otherwise the zone's offset. Its custom patterns use OC-020's pattern letters.
  - `ValueSourceControl` offers **Date** on `string`, `integer`, `number`, `datetime` and `any` fields. `computed/NowSourceEditor.tsx` is shown by both inspectors.
  - The Canvas shows a clock badge with the full wording in its title. The Composer tree reads *now (epoch ms)*.
  - `now` is also in the transform palette, under dates, as the op-schema parity test requires.
- **Parity test:** the parity integration test compares the job's and `odb`'s documents byte for byte, and they start at different times, so a `now` field isn't added to it. Both runners compile the same program, which `TestNow_OC179_InADocument` covers.
- **Docs:** `docs/user-guide/computed-fields.md` gains "The current date and time".
- **Changed after review (2026-10-18):**
  - The choice is labelled **Date**, not *Now*; its tooltip still says *the current date and time*.
  - The inspectors now follow the value source. **Source** shows the column picker or how to drag one; **Fixed** shows the value box (with the current value as its placeholder); **Template** and **Date** show their editors.
  - Before, the constant box showed under every choice, so *Source* and *Fixed* looked the same.
