# OC-077 — Sample pane stands out from the canvas

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E10 Document Composer | P2 | 1 | OC-048, OC-049 | Implemented |

## User story
As a **data engineer** working in the composer, I want the Sample pane to have its own clearly different background, so that I can tell at a glance where the canvas ends and the generated document begins, and read the JSON easily.

## Background (2026-10-01)
The Sample pane (generated document and the customer's reference) had no background of its own: it showed the page's light grey, the same as the canvas above it.

## Scope
**In:**
- The Sample pane has its own background token, `--sample-surface`: a light cyan `#e6fbfd` in the light theme, a deep navy `#0f172a` in the dark theme. A dark "console" pane was tried first and found too heavy; grey tints don't stand out from the canvas, and blue/violet tints hide the indigo highlight of the selected key.
- Text colors are unchanged (the theme's own tokens).

**Out:** a user setting to choose the pane's color; the same treatment for the Canvas view's preview.

## Acceptance criteria & unit tests

### AC1 — Readable and distinct
**Given** either theme, **then** every text token used in the pane has at least 4.5:1 contrast on `--sample-surface`, which differs from the canvas background by ΔE ≥ 7 and from the selection highlight (`--primary-soft`) by ΔE ≥ 5.

| Test | Location | Verifies |
|---|---|---|
| `tokens.test.ts` › `sample pane` | `web/src/styles/tokens.test.ts` | Both themes |
| `SamplePane.test.tsx` (existing) | `web/src/features/composer/SamplePane.test.tsx` | Behavior unchanged |
