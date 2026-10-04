# OC-107 — Conversion workspace: review, edit and version converted functions

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E15 PL/SQL to Couchbase UDFs | P0 | 5 | OC-105 | Not started |

## User story
As a **developer**, I want to see each PL/SQL unit next to its generated UDF, jump between matching lines, fix the TODOs in an editor and keep versions, so that I can finish the conversions the tool couldn't complete on its own.

## Scope
**In:**
- **Functions area → Convert** page: unit list (rating, conversion state `not converted`/`draft`/`converted`/`stale`/`deployed`, TODO count) and a two-pane workspace: PL/SQL source (read-only) on the left, generated SQL++/JS on the right.
- Code editor: CodeMirror 6 (`@codemirror/lang-sql` with a PL/SQL dialect, `@codemirror/lang-javascript`, `@codemirror/merge` for version diffs), lazy-loaded so the rest of the app's bundle doesn't grow; this story adds a bundle-size check for the main chunk.
- Source map: selecting a line highlights its counterpart in the other pane; findings and TODOs are gutter markers with the fix hint in a tooltip (height is premium: no stacked banners, OC-062).
- Editing creates a new `conversions` version with `origin: manual`; a generated version never overwrites a manual edit (re-convert creates a new version and offers a 3-way merge view).
- Version history with diff, restore, and "accept TODO" (mark resolved with a note).
- Syntax and deploy-readiness checks on save (JS parse, SQL++ parse through the target's `EXPLAIN`-free parse endpoint when available, or `PREPARE` in a sandbox scope per the spike).
- Role rules: viewers read only; editors edit.

**Out:** deploying (OC-108), running (OC-109).

## Acceptance criteria & unit tests

### AC1 — Two panes and line mapping
**Given** a converted unit, **when** the user clicks PL/SQL line 12, **then** the generated lines from it are highlighted on the right, and the other way round.

### AC2 — Manual edits are kept
**Given** a manual edit (v3) on top of a generated v2, **when** the unit is re-converted, **then** v4 is created as generated, v3 stays the current version, and the merge view shows v2 → v4 changes against v3.

### AC3 — Bundle budget
**Given** the production build, **then** the editor code is in its own chunk loaded only by the Convert page, and the main chunk grows by less than 5 KB gzipped.

| Test | Location | Verifies |
|---|---|---|
| `ConvertWorkspace.test.tsx` › `OC-107 AC1` | `web/src/features/functions/ConvertWorkspace.test.tsx` | AC1 |
| `TestConversions_AC2_ManualKept` | `internal/plsql/conversions_test.go` | AC2 |
| `bundle-budget.test.ts` (new) | `web/scripts/bundle-budget.test.ts` | AC3 |
