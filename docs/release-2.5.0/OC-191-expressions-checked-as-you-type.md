# OC-191 — Expressions: checked as you type, with the fix in the message

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P1 | 3 | OC-047, OC-138 | Implemented |

## User story
As a **mapping author** writing an expression for a field in the composer, I want to see **as I type** whether it works, and what to write instead when it doesn't. That way I fix it in the Expression box, instead of finding out from a wall of errors when I press Preview.

## Background (2026-10-22)
An author wanted a document field `paid_in_out::<id>` and typed:

```
"paid_in_out::" + $doc.id
```

The Expression box gave no feedback. *Use expression* saved it, and the error only showed up at Preview. The obvious next attempt, `"paid_in_out::" + $root.order_id`, was worse: it passed every check, then failed on **every row** at Preview with:

```
EXPR_ERROR: invalid operation: string + int64 (1:17)
 | "paid_in_out::" + $root.order_id
 | ................^
```

What goes wrong:

| Problem | Cause |
|---|---|
| **No feedback while typing** | The composer's Expression box is a plain text area. Only the saved mapping is validated. |
| **`$doc` isn't a name** | Templates read the document as `${doc.…}`, but expressions read the row as `$root`. The message "unknown name $doc" doesn't say so. |
| **Text + number compiles, then fails on every row** | Columns read through `$root`, `$parent` or a relation are untyped maps to expr, so `"text" + $root.order_id` compiles. It fails only at run time, once the value turns out to be an `int64`. |
| **Text + a plain column is cryptic** | A bare column (`order_no`) has no type at compile time, so `"SO-" + order_no` fails with "mismatched types string and unknown". |
| **Functions from other languages** | `string(x)`, `toString(x)` and similar give "unknown name string" and no list of what exists. |

## Scope
**In:**
- **Live check in the composer's Expression box.** The text as typed, after a 400 ms pause, goes through the same preview a run uses (`specOverride` with the draft as the field's expression, 5 samples):
  - an error for **this field** (a compile error from the 422's `fieldErrors`, or a row error at the field's path) is shown under the box, and *Use expression* is disabled;
  - otherwise the value for the first sample document is shown (`→ "paid_in_out::1001"`);
  - other fields' problems don't block this one.
- **Compile-time check of `+` that joins text with a value that isn't text** (`TRANSFORM_INVALID`), reported at that value:
  - a string literal (or `upper`/`lower`/`trim`/`concat`/`now`) joined to a column known to be an integer, number, boolean or datetime, read through `$root`, `$parent` or a relation, or to a number literal;
  - a string literal joined to a **bare** column of any type, which can't compile anyway;
  - the message gives the working rewrite of the whole chain, for example `use concat("SO-", order_id, "/", customer_id) to join them`.
- **Hints on unknown names** in the composer, both while typing and for a saved expression's issues:
  - `$doc` (and any other `$` name): "the document's own row is $root (as in $root.id) and the enclosing row $parent";
  - `string`, `toString`, `int`, `format` and similar: the list of functions, with `concat("a", 1)` for text and numbers.

**Out:**
- The canvas view's *Expression* transform step (plain expressions, no `$root`): no live check yet.
- Making `+` join text with numbers, or with nulls. `concat` does both, and the messages point to it.
- Arithmetic on bare columns in composer expressions (`order_id + 1`). It still fails to compile; `$root.order_id + 1` works.

## Implementation details
- `backend/internal/transform/composed.go`:
  - `exprScope` keeps the row's own `table` for the types of bare columns.
  - `textPlus` walks the AST. It collects the `+` nodes, then checks each chain from its top, so a chain is reported once with one rewrite. `exprScope.typeOf` gives the JSON type of literals, helper calls and columns.
  - It runs in `exprScope.check`, before `expr.Compile`, so it reports first.
- `web/src/features/composer/expressionCheck.ts` (new):
  - `useExpressionCheck(projectId, mappingId, spec, fieldId, text, enabled)` returns `idle`, `checking`, `ok` with the value, or `error` with the message;
  - `expressionHint(message)` adds the hints above.
- `web/src/features/composer/ComposerInspector.tsx`:
  - the check runs while the draft differs from the saved expression;
  - *Use expression* waits for a fix;
  - saved expression issues go through `expressionHint`.

## Acceptance criteria & unit tests

### AC1 — Text joined to a number is caught when the mapping is checked
**Given** `"paid_in_out::" + $root.order_id` where `order_id` is an integer, **then** validation reports `TRANSFORM_INVALID` at `$root` with `use concat("paid_in_out::", $root.order_id) to join them`, and no row fails at Preview. A chain gets one issue with the whole rewrite. Text + text, `concat(…)`, number + number and helper + text are not reported.

| Test | Location | Verifies |
|---|---|---|
| `TestComposed_ExpressionTextPlus` | `backend/internal/transform/composed_test.go` | `$root.order_id`, a chain, a bare text column; the column of each issue; expressions that stay valid |

### AC2 — The Expression box says what's wrong while you type, and waits for a fix
**Given** `"paid_in_out::" + $doc.id` typed in the Expression box, **then** the error shows under it with the `$root` hint, and *Use expression* is disabled. **When** it becomes `concat("paid_in_out::", $root.order_id)`, **then** the first sample's value shows, and *Use expression* is enabled.

| Test | Location | Verifies |
|---|---|---|
| `checks an expression as it is typed: the server says why, Use expression waits for a fix` | `web/src/features/composer/Inspector.test.tsx` | The 422 `fieldErrors` for the field's path, the hint, the disabled button, then the value and the enabled button |

## Implementation notes (2026-10-22)
- **The `$doc` and function hints are in the web app, not the server.** Adding them to the backend's expression compile path cost about 7% on `TestApply_AC7_TemplatesBudget` (245k rows/s against 265k at HEAD). They never run per row, so this is a code-layout effect, but `make test` runs that budget. With the hints in the web app, the transform package measures 262–272k rows/s. `odb` and the API still say "unknown name $doc" without the hint.
- **Why text + a column that may be null still fails:** `+` can't join text with a value that's missing on some rows. That's run-time data, so it isn't reported at compile time. `concat` skips nulls, which is why the messages suggest it.
- The live check uses the template editor's (OC-138) preview pattern. The server's issue paths and the client's `pathsById` both use `items[].name`, so errors under arrays are matched.
