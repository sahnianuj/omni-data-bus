# OC-004 — Design system & application shell

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E1 Foundation | P0 | 8 | OC-001, OC-002 | Implemented |

## User story
As a **user**, I want a polished, consistent, and accessible interface, so that the product feels trustworthy enough to point at production databases.
As a **frontend engineer**, I want reusable tokens, components, and page patterns, so that every feature looks and behaves the same way.

## Design direction
The product should read as a calm, precise, data-dense professional tool, in the spirit of Linear, Vercel, and Supabase Studio.
- **Neutral canvas, one accent.** Neutral zinc surfaces with one brand accent (indigo), and semantic colors reserved for status.
- **Density with breathing room.** A 4 px spacing grid, 14 px base text, and 36 px default control height (32 px compact in tables and canvas).
- **Motion is functional.** 150–200 ms ease-out on overlays and state changes, no decorative animation, and full respect for `prefers-reduced-motion`.
- **Show the data, not chrome.** Tables and trees are first-class. Type badges (`int`, `text`, `timestamptz`) render in a monospace font.

## Implementation details

### Tokens (`web/src/styles/tokens.css`, consumed by Tailwind v4 `@theme`)
| Token | Light | Dark | Use |
|---|---|---|---|
| `--background` | zinc-50 | zinc-950 | page |
| `--surface` | white | zinc-900 | cards, panels |
| `--surface-muted` | zinc-100 | zinc-800/60 | table headers, wells |
| `--border` | zinc-200 | zinc-800 | |
| `--foreground` | zinc-900 | zinc-50 | primary text |
| `--muted-foreground` | zinc-500 | zinc-400 | secondary text |
| `--primary` | indigo-600 | indigo-500 | CTAs, focus, selected |
| `--success` | emerald-600 | emerald-500 | connected, auto-mapped, completed |
| `--warning` | amber-500 | amber-400 | suggestion, lossy conversion |
| `--danger` | red-600 | red-500 | errors, destructive |
| `--info` | sky-600 | sky-500 | informational |
| `--ring` | indigo-500/40 | indigo-400/50 | focus ring (2 px + 2 px offset) |
| Radius | `--radius: 8px` (controls 6 px, cards 10 px, dialogs 12 px) | | |
| Shadow | `sm` for cards; `lg` for popovers and dialogs only | | |
| Font | Geist Sans (UI) and Geist Mono (types, keys, JSON), self-hosted via `@fontsource` | | |
| Type scale | 12/16, 13/18, **14/20 base**, 16/24, 20/28, 24/32, 30/36 | | |

Mapping-specific semantic tokens: `--map-auto` (emerald), `--map-suggested` (amber), `--map-manual` (indigo), `--map-unmapped` (zinc-400), `--map-error` (red). They are used identically by the canvas, the table view, the score, and the legend.

All foreground/background pairs meet contrast ≥ 4.5:1 (text) and ≥ 3:1 (UI boundaries). This is verified by the unit test in AC2.

### Component inventory (`src/components/ui`, shadcn/ui generated then themed)
Button (primary / secondary / ghost / destructive / link; sizes sm, md, icon; `loading` state), Input, Textarea, Select, Combobox (cmdk), Checkbox, Switch, RadioGroup, Tabs, Dialog, Sheet (side panel), Popover, Tooltip, DropdownMenu, Badge, Card, Table (TanStack Table wrapper with sticky header, sort, and virtualized rows via `@tanstack/react-virtual`), Tree (custom, virtualized, keyboard navigable), Progress, Skeleton, Toast (sonner), Alert, Breadcrumb, Stepper (custom), Separator, ScrollArea, Kbd, CodeBlock (JSON syntax highlighting via `shiki` light build), EmptyState, ErrorState, StatusDot, TypeBadge, CopyButton.

### App-level components (`src/components`)
- `AppShell`:
  - A collapsible left sidebar (240 px, or 64 px collapsed; the state persists in localStorage) with nav items Dashboard, Projects, Jobs, Settings. There is no top-level Connections item, because connections live inside their project (OC-007).
  - A workspace switcher at the top of the sidebar.
  - A user menu at the bottom with the theme toggle (light / dark / system), docs link, and sign out.
  - A top bar with breadcrumbs, a global command palette trigger (`⌘K`), and a help button.
- `PageHeader`: title, description, primary action, and secondary actions.
- `AsyncBoundary`: renders the skeleton, error, or empty state from a TanStack Query result; `ErrorState` shows the problem `title`, the `detail`, a *Retry* button, and a copyable request ID.
- `CommandPalette` (cmdk): navigate to any project (and its Source, Target, and Mappings tabs), and run "New project" and "Toggle theme".
- `ConfirmDialog`: for destructive actions. Irreversible ones require typing the resource name.
- `StatusBadge`: shared mapping from connection, job, and project states to color, icon, and label.

### Page patterns
1. **List page:** header + filters + table + empty state.
2. **Detail page:** header + tabs.
3. **Wizard page:** stepper rail on the left, content on the right, and a sticky footer with Back / Next / Save.
4. **Workbench page:** full-bleed, used by the designer, the mapping canvas, and the job monitor. It has resizable panels via `react-resizable-panels`, and the sidebar auto-collapses.

### Screen inventory for 1.0 (all use the patterns above)
Sign in / Sign up · Dashboard · Projects list · Project wizard (Source repository with test, Target repository with test, Tables, Design & Map, Review, Migrate) · Schema explorer (inside project) · Document designer · Mapping workbench · Job monitor · Jobs history · Settings (profile, workspace).

### Theming
`ThemeProvider` sets `class="dark"` on `<html>` from the preference `light | dark | system`, listens to `matchMedia`, and avoids a flash of the wrong theme through an inline script in `index.html` that reads localStorage before first paint.

### i18n
`i18next` with `en.json`. No hard-coded user-facing strings. An ESLint rule (`i18next/no-literal-string` in JSX) enforces this.

### Accessibility
- Radix gives focus management.
- Every icon-only button has an `aria-label`.
- The skip-to-content link is the first focusable element.
- `@axe-core/react` runs in dev, and `vitest-axe` runs in component tests.

### Storybook
Storybook 8 (Vite builder) documents every `ui` and app component, and a Chromatic-style visual snapshot runs in CI (`storybook test-runner` with image snapshots).

## Acceptance criteria & unit tests

### AC1 — App shell & navigation
**Given** an authenticated user, **when** any app route loads, **then** the sidebar shows Dashboard, Projects, Jobs, and Settings, with the active route highlighted (`aria-current="page"`). The collapse state persists across reloads, and breadcrumbs reflect the route hierarchy.

| Test | Location | Verifies |
|---|---|---|
| `AppShell.test.tsx` › `AC1: renders nav items and marks active route` | `src/components/AppShell.test.tsx` | MemoryRouter at `/projects` |
| `AppShell.test.tsx` › `AC1: persists collapsed state` | same | Toggle → localStorage → rerender collapsed |
| `Breadcrumbs.test.tsx` › `AC1: builds crumbs from route handles` | `src/components/Breadcrumbs.test.tsx` | |

### AC2 — Tokens meet contrast requirements in both themes
**Given** the token definitions, **then** every declared text pair has a contrast ratio ≥ 4.5:1 and every UI boundary pair ≥ 3:1, in both light and dark.

| Test | Location | Verifies |
|---|---|---|
| `tokens.test.ts` › `AC2: text pairs ≥ 4.5:1 (light, dark)` | `src/styles/tokens.test.ts` | Parses `tokens.css`, computes WCAG contrast |
| `tokens.test.ts` › `AC2: mapping state colors distinguishable` | same | ΔE between `--map-auto/suggested/manual/unmapped` ≥ 20 |

### AC3 — Theme switching without flash
**Given** the preference is `dark`, **when** the app loads, **then** `<html>` has class `dark` before React mounts. **When** the user picks `system`, the theme follows OS changes live.

| Test | Location | Verifies |
|---|---|---|
| `ThemeProvider.test.tsx` › `AC3: applies stored preference` | `src/app/ThemeProvider.test.tsx` | |
| `ThemeProvider.test.tsx` › `AC3: follows matchMedia when system` | same | Mocked matchMedia change event |
| `theme-init.test.ts` › `AC3: inline script sets class from localStorage` | `src/app/theme-init.test.ts` | Evaluates the inline script in jsdom |

### AC4 — Standard async states
**Given** a query in each state, **when** it is rendered via `AsyncBoundary`, **then**: pending → skeleton (`aria-busy="true"`); error → `ErrorState` with the problem title, detail, request ID, and a working Retry; empty → the provided `EmptyState`; success → children.

| Test | Location | Verifies |
|---|---|---|
| `AsyncBoundary.test.tsx` › `AC4: pending shows skeleton` | `src/components/AsyncBoundary.test.tsx` | |
| `AsyncBoundary.test.tsx` › `AC4: error shows problem and retry calls refetch` | same | |
| `AsyncBoundary.test.tsx` › `AC4: empty renders EmptyState` | same | |

### AC5 — Command palette
**Given** the user presses `⌘K` / `Ctrl+K`, **then** the palette opens with focus in the search box, lists navigation targets and actions, filters by fuzzy match, and executes the selected item on Enter. Esc closes it and restores focus.

| Test | Location | Verifies |
|---|---|---|
| `CommandPalette.test.tsx` › `AC5: opens on shortcut and focuses input` | `src/components/CommandPalette.test.tsx` | |
| `CommandPalette.test.tsx` › `AC5: filters and navigates on Enter` | same | |
| `CommandPalette.test.tsx` › `AC5: Esc restores focus` | same | |

### AC6 — Accessibility baseline
**Given** each `ui` component story, **when** axe runs, **then** there are zero violations of serious or critical impact. All interactive elements are reachable by keyboard with a visible focus ring.

| Test | Location | Verifies |
|---|---|---|
| `a11y.test.tsx` › `AC6: <Component> has no axe violations` (parameterized over all components) | `src/components/ui/a11y.test.tsx` | `vitest-axe` |
| `Button.test.tsx` › `AC6: loading state sets aria-busy and disables` | `src/components/ui/Button.test.tsx` | |

### AC7 — Destructive confirmation
**Given** a `ConfirmDialog` with `requireTypedName="prod-pg"`, **then** the confirm button stays disabled until the input exactly equals `prod-pg`.

| Test | Location | Verifies |
|---|---|---|
| `ConfirmDialog.test.tsx` › `AC7: enables confirm only on exact name` | `src/components/ConfirmDialog.test.tsx` | |

### AC8 — No literal strings
**Given** any JSX text node with a literal string outside the allow-list, **then** lint fails.

| Test | Location | Verifies |
|---|---|---|
| `eslint-config.test.ts` › `AC8: i18next/no-literal-string enabled for tsx` | `web/scripts/eslint-config.test.ts` | |

## Implementation notes (as built)
- **Fonts:** Geist Sans / Geist Mono (approved in the UI preview) instead of Inter / JetBrains Mono.
- **Token adjustments found by the contrast test (AC2):** light `--success-dot` is `#059669` (3.8:1 on white; `#10B981` was 2.5:1) and dark `--primary` is `#5D56F0` (white text 5.2:1, 3.4:1 against the dark surface). Both are visually indistinguishable from the approved preview.
- **Storybook is not included** in this story. Component behaviour and accessibility are covered by the Vitest suites (`a11y.test.tsx` runs axe on every core component); visual snapshots can be added with the E2E work in OC-034.
- The help button in the top bar was left out until there is a documentation site to link to.

