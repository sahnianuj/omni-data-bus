# OC-192 — Templates: what's wrong shows while you write, not at Preview

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E14 Design-first Mapping | P1 | 2 | OC-138, OC-191 | Implemented |

## User story
As a **mapping author** building a field's value with a template, I want the template editor to tell me **while I write** that the template won't do what I mean, and what to write instead. That way I don't find out from a failed Preview.

## Background (2026-10-22)
An author wanted a document's `id` to be `paid_in_out::<id>`, and wrote the template in the style of an expression:

```
"paid_in_out::" + ${doc.id}
```

The template editor showed "No sample document yet" and nothing else, and Preview failed. Three things went wrong, and the editor said none of them:

| Problem | Cause |
|---|---|
| **The text is written like code** | A template is the value as written: this one produces `"paid_in_out::" + 7`, with the quotes and `+` in it. It's valid, so nothing flags it. |
| **The field reads itself** | On the `id` field, `${doc.id}` is the field being computed. The server reports `COMPUTED_CYCLE` "id reads itself", but doesn't say that the source row's `id` is `${row.id}`. |
| **The editor drops the server's reason** | The editor's live preview discarded a failed preview (`catch(() => setPreview(null))`), so a 422 naming the field never showed. |
| **Text into a number field fails on every row** | Text into an integer field passes the compiler, because `"42"` converts. A template with words in it never converts, so every row failed at Preview with `TYPE_MISMATCH`. |

## Scope
**In:**
- **Written like code → the template it means.** When the text is quoted parts and `${…}` (or bare `doc.x`) joined with `+`, the editor says "A template is the value as written, so the quotes and + would be in it." and offers **Use paid_in_out::${doc.id}**.
- **The server's reason shows.** When the live preview is refused (422), the `fieldErrors` entry for this field shows as the editor's error, unless the saved mapping's issues already say it.
- **"reads itself" says what to write.** At the document root, when the row has a column named like the field, the message adds: "To read the source row's id column, write ${row.id}."
- **Words into a number or boolean field** is a validation error (`COMPUTED_TEXT_TYPE`): `The template's text "paid_in_out::" can't become integer, so every document would fail. Make id a string field.` Digits and signs around a value (`-${row.n}0`) are still allowed. A template with transforms is left to the compiler.

**Out:**
- Making `+` work in templates. Templates have no operators; expressions (OC-191) use `concat`.

## Implementation details
- `web/src/features/computed/template.ts`: `asTemplate(text)` splits on `+` outside quotes and `${…}`. It returns the template when every part is a quoted string, a `${…}`, or a reference such as `doc.id`, and at least one part is quoted; otherwise null.
- `web/src/features/computed/ComputedEditor.tsx`:
  - the code-style note, with its fix button;
  - `previewError` from the 422's `fieldErrors` for the field's path.
- `backend/internal/transform/computed.go`: the `${row.<name>}` hint on `COMPUTED_CYCLE`.
- `backend/internal/mapping/validate.go`: `templateTextIssue` and `templateWords` (`COMPUTED_TEXT_TYPE`). The editor already shows `COMPUTED_*` issues.

## Acceptance criteria & unit tests

### AC1 — A template written like code is offered as the template it means
| Test | Location | Verifies |
|---|---|---|
| `asTemplate` | `web/src/features/computed/template.test.ts` | Double and single quotes, bare references, `+` inside quotes; real templates are left alone |
| `a template written like code offers the template it means` | `web/src/features/computed/ComputedEditor.test.tsx` | The note, the button, the text replaced |

### AC2 — The server's reason shows in the editor
| Test | Location | Verifies |
|---|---|---|
| `the server's reason shows when the preview can't run` | `web/src/features/computed/ComputedEditor.test.tsx` | A 422 for the field's path shows as an alert |

### AC3 — A field reading itself is told about `${row.…}`
| Test | Location | Verifies |
|---|---|---|
| `TestCompile_OC192_ReadsItselfSuggestsRow` | `backend/internal/transform/computed_test.go` | |

### AC4 — Words into a number field fail validation, not every row
| Test | Location | Verifies |
|---|---|---|
| `TestValidate_OC192_TemplateTextIntoNumber` | `backend/internal/mapping/validate_test.go` | Text before and after a value, an escaped `\${`, digits only, a string field |

## Implementation notes (2026-10-22)
- **The number-field check is in validation, not in the template compiler.** In `compileComputed` it cost about 9% on `TestApply_AC7_TemplatesBudget` (240k rows/s against 265k). It never runs per row; this is the same code-layout effect as in OC-191. In `mapping.Validate` the budget holds (262–268k).
  - Validation runs on the saved mapping, so the editor shows `COMPUTED_TEXT_TYPE` after the autosave.
  - Preview doesn't run validation, so in Preview the rows still fail with `TYPE_MISMATCH`.
