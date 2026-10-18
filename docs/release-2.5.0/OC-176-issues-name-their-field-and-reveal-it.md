# OC-176 — Issues: the field name stands out, and a click reveals it in the target pane

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 Mapping Canvas | P1 | 2 | OC-022, OC-174 | Implemented |

## User story
As a **data engineer** working through the Issues panel, I want each issue's field name to stand out (in green), and clicking the issue to bring that field into view and highlight it in the Canvas target pane, so that I can spot which field is meant and fix it without searching the document for it.

## Background (gap found 2026-10-17)
The Issues tab (`ValidationPanel`, OC-022) shows each issue as plain text, for example:

> Required field version has no mapping. Map a source, set a constant, or mark it optional.

- **The name is hard to spot.** The field's path is in the middle of the sentence, in the same style as the rest. On a design-first mapping (OC-163) the panel can hold dozens of these, and names like `version` or `type` read as ordinary words.
- **Go to leaves the Canvas.** For a field with no mapping, *Go to* switches to the Design tab (`setMode('design')`) and selects the field there. The user loses the Canvas, which is where they'd map the field: a column is dragged onto the target pane. Even on the Canvas, selecting a field doesn't scroll to it, and a field inside a collapsed object stays hidden.

## Scope
**In:**
- **The field name in green.** In every issue whose message contains its field's path (`path`, or `nodePath`), that path is shown in monospace and in the success colour (`text-success`), in both themes. This covers *Required field … has no mapping*, type mismatches, reference issues and others. Messages without a path are unchanged.
- **Clicking the issue reveals the field.** The issue's message is a button. Clicking it:
  - switches to the Canvas from the Table or Design tab (a composed mapping's Composer has no Issues panel);
  - selects the field (or the issue's mapping, when it has one);
  - expands the field's collapsed parents;
  - scrolls the target pane so the field is in view (`align: 'auto'`, OC-174);
  - flashes the row green for about 1.5 s. The row then stays selected.
- **Filters.** When the search or the filter hides the field, they are cleared so it can be shown.
- *Go to* is unchanged; it still opens the field in the Design tab, where its design is edited.

**Out:** the Key panel, the expression editors and the preview's row errors, which already point at their own place; a new colour token.

## Implementation details
- **Store:** `revealField(fieldId, selection?)` in `mapping/store.ts`. It does the following in one update:
  - selects the field or mapping;
  - removes the field's ancestors (`parentMap`) from `collapsed`;
  - moves to the Canvas from the Table or Design tab;
  - clears a filter;
  - increments `reveal.seq`.
- **Canvas:** an effect on `reveal.seq` scrolls `tv.scrollToIndex(tgtIndex.get(id), { align: 'auto' })`. The revealed row is keyed by the sequence, so the `odb-reveal` keyframes (`index.css`) start again on every click.
- **ValidationPanel:** `highlightPath(message, path)` splits the message around the path. The path renders as `<span class="font-mono text-success">`, and the whole message is a `button` labelled *Show path in the target pane*.

## Acceptance criteria & unit tests

### AC1 — The field name is green
**Given** the issue *Required field version has no mapping…*, **then** `version` is shown in its own monospace element with `text-success`, and the rest of the sentence is unchanged. **Given** an issue without a path, **then** it renders as before.

| Test | Location | Verifies |
|---|---|---|
| `ValidationPanel.test.tsx` › `OC-176 AC1` | `web/src/features/mapping/ValidationPanel.test.tsx` | Green monospace name; whole-word match; no path, no change |

### AC2 — A click reveals the field in the target pane
**Given** the Table tab and an issue on a field inside a collapsed object, **when** the user clicks the issue, **then**:
- the Canvas shows;
- the parent object is expanded;
- the field is selected and its row flashes;
- the target pane is scrolled to it.

**Given** a filter that hides the field, **then** the filter is cleared.

| Test | Location | Verifies |
|---|---|---|
| `ValidationPanel.test.tsx` › `OC-176 AC2` | `web/src/features/mapping/ValidationPanel.test.tsx` | Store: mode, selection, collapsed, filter, `reveal.seq` |
| `MappingCanvas.test.tsx` › `OC-176 AC2` | `web/src/features/mapping/MappingCanvas.test.tsx` | Target pane scrolled; the row carries the reveal flash |

## Implementation notes (2026-10-17)
- `ValidationPanel.tsx`:
  - `highlightPath` finds the path as a whole word, so `type` doesn't match inside `types`.
  - It tries `path` (with a leading `docSchema.` removed), then `nodePath`.
  - An issue with a `fieldId` renders its message as a button, titled *Show `path` in the target pane*.
- `store.ts`: `revealField(fieldId, selection?)` does everything in one update.
- `MappingCanvas.tsx`:
  - an effect on `reveal.seq` calls `tv.scrollToIndex(…, { align: 'auto' })`;
  - the revealed row is keyed `id#seq` with the `odb-reveal` class;
  - the keyframes in `styles/index.css` use `--success-soft` and `--success`, so the flash works in both themes. The reduced-motion rule shortens it.
- **Known:** a revealed row that is scrolled away and back plays the flash once more, because the virtualizer remounts it. This lasts until another field is revealed.
- **Added after review: a field filled twice (`DUPLICATE_TARGET`).**
  - The message named internal mapping ids that the UI never shows: *Field customer.userID has two mappings (map_762e0cf161, map_hawuu5lmp5); keep one.*
  - It now says what each mapping fills the field from: *Field customer.userID is filled twice: from customers.user_id and from carts.customer_id. Keep one.* This uses `mapping.DescribeMapping`; a constant, an expression, a computed value or *not populated* are named too.
  - The issue offers **Keep from customers.user_id** and **Keep from carts.customer_id**. Each removes the field's other mappings in one undo step (`fixes.ts` › `keepOnly`).
  - Tests: `validate_test.go` › `TestValidate_DuplicateTargetNamesBothSources`; `ValidationPanel.test.tsx` › `a field filled twice offers to keep either mapping`.
