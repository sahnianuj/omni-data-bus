# OC-028 — Migration monitor UI (live progress)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E5 Migration | P0 | 8 | OC-004, OC-023, OC-026, OC-027 | Implemented |

## User story
As a **data engineer**, I want a clear, live view of my migration, with a progress bar, rows per second, ETA, per-table progress, and errors as they happen, and controls to pause, resume, or cancel, so that I stay confident and in control while data streams into Couchbase.

## Implementation details

### Layout (`/projects/{p}/jobs/{jobId}`, Detail page)
```
┌ Project › Jobs › #J-0142                                   ● Running   [⏸ Pause] [✕ Cancel] [⋯] ┐
│                                                                                                 │
│  Migrating 3 tables  ·  PostgreSQL shop@db1  →  Couchbase shop.sales                           │
│                                                                                                 │
│  ████████████████████████████████▌░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  33.4 %                    │
│  402,113 of 1,203,441 rows  (exact)                         ETA 37 s · elapsed 19 s           │
│                                                                                                 │
│  ┌ Throughput ──────────┐ ┌ Written ─────────┐ ┌ Skipped ────┐ ┌ Failed ────────┐              │
│  │ 21.4k docs/s  ▁▃▅▇▆▇ │ │ 401,990          │ │ 0           │ │ 123  (0.03%) ⚠ │              │
│  │ peak 25.0k           │ │ 312 MB           │ │             │ │ view errors →  │              │
│  └──────────────────────┘ └──────────────────┘ └─────────────┘ └────────────────┘              │
│                                                                                                 │
│  [Overview] [Tables] [Errors 123] [Options] [Log]                                               │
│  Tables:                                                                                        │
│   ✓ public.customer → customer        120,345 / 120,345   100%   ████████████   done          │
│   ● public.orders   → orders          281,768 / 1,083,096  26%   ███░░░░░░░░░   migrating      │
│   ○ public.address  → address                —   /  40,112    —                 queued         │
│                                                                                                 │
│  Live errors  (latest)                                                                          │
│   15:04:03  orders  PARSE_FAILED  total: "12,5" is not a number   key [918273]                 │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘
```
- **Main progress bar.**
  - The primary color is animated with a subtle striped shimmer while running, and turns solid on pause.
  - It is emerald when completed, amber for `completed_with_errors`, red on `failed`, and zinc on cancel.
  - The percentage is tabular-numbered (`font-variant-numeric: tabular-nums`) so the text doesn't jitter. The bar width transition is 400 ms linear, so 1 s ticks look continuous.
  - The totals line shows "(estimate)" with a tooltip until the exact count arrives, then "(exact)".
  - `role="progressbar"` with `aria-valuenow`, and `aria-valuetext="33 percent, about 37 seconds remaining"`, throttled to change at most every 10 s for screen readers.
- **Stat tiles** (from the `StatTile` component, following the dataviz skill conventions):
  - Throughput with a 60-point sparkline (Recharts `AreaChart`, no axes, tooltip on hover). The ring buffer holds the last 60 snapshots on the client.
  - Written (count and bytes).
  - Skipped.
  - Failed (count and ratio, with a link to the Errors tab). The tile turns amber when > 0 and red when > 50% of the threshold.
- **Status badge and control buttons** by state:
  - `running` → Pause and Cancel;
  - `pausing` → a disabled "Pausing…" spinner;
  - `paused` → Resume and Cancel;
  - `queued` → Cancel plus "Waiting for a worker…";
  - `failed` → Resume (when resumable) and "Retry failed rows" (when applicable);
  - terminal states → *Run again* (opens the Review step with the same options).
  - Cancel asks for confirmation: "Documents already written stay in Couchbase."
- **Tables tab:** per-mapping rows with phase icons and mini bars. Clicking a row opens the mapping (read-only while running).
- **Errors tab:** OC-026.
- **Options tab:** the job options, read-only.
- **Log tab:** a timeline of status transitions and notable events (snapshot taken, exact count done, partitions planned, concurrency reduced by AIMD, resumed from checkpoint, verification results), from `GET /jobs/{id}/events/log`, which is backed by a small `job_events` table the worker appends to.
- **Connection indicator:** a small "Live" dot in the header, which changes to "Reconnecting…" (amber) from the SSE hook state.
- **Completion:** when the job reaches a terminal state, the page shows a **summary card** at the top: "✓ Migrated 1,203,318 documents in 58 s (avg 20.7k/s)", 123 failed, verification results (OC-030), and buttons for *Download report* (JSON/CSV), *View errors*, *Retry failed rows*, and *Open in Couchbase* (a link to the Capella or Web UI docs browser for the keyspace, when the URL is known). The browser tab title reflects progress while running ("33% · Omni-Data-Bus") and completion ("✓ Done · Omni-Data-Bus"), and an optional browser notification (permission asked once) is shown on completion.

### Jobs list (`/jobs`) and dashboard mini-bars
Reuse the `JobProgressBar` component (compact variant) and the multiplexed SSE stream (OC-027).

### Components
`JobMonitorPage`, `JobProgressBar`, `StatTile`, `ThroughputSparkline`, `JobControls`, `MappingProgressList`, `LiveErrorFeed`, `JobTimeline`, `JobSummaryCard`. The state comes entirely from the `['job', id]` query cache, which is fed by `useJobEvents`.

## Acceptance criteria & unit tests

### AC1 — Progress bar reflects the snapshot
**Given** a snapshot with percent 33.41, processed 402,113, total 1,203,441 (exact), and ETA 37 s, **then** the bar width is 33.41%, the label reads "33.4 %", the totals read "402,113 of 1,203,441 rows (exact)", and ETA and elapsed are formatted. ARIA attributes are present.

| Test | Location | Verifies |
|---|---|---|
| `JobProgressBar.test.tsx` › `AC1: width, label, aria` | `src/features/jobs/JobProgressBar.test.tsx` | |
| `format.test.ts` › `AC1: formatDuration 37s, 4m 05s, 2h 03m` | `src/lib/format.test.ts` | |
| `JobProgressBar.test.tsx` › `AC1: estimate label with tooltip` | `src/features/jobs/JobProgressBar.test.tsx` | |

### AC2 — State-driven colors & controls
**Given** each job status, **then** the bar color and the visible and enabled controls match the specification table.

| Test | Location | Verifies |
|---|---|---|
| `JobControls.test.tsx` › `AC2: controls per status (table-driven)` | `src/features/jobs/JobControls.test.tsx` | |
| `JobProgressBar.test.tsx` › `AC2: color per status` | `src/features/jobs/JobProgressBar.test.tsx` | |

### AC3 — Live updates
**Given** successive SSE progress events, **then** the tiles, the bar, and the per-table rows update without a page refresh, and the sparkline appends points (max 60). **Given** the SSE connection drops, **then** the header indicator shows "Reconnecting…".

| Test | Location | Verifies |
|---|---|---|
| `JobMonitorPage.test.tsx` › `AC3: updates on events` | `src/features/jobs/JobMonitorPage.test.tsx` | Mock EventSource |
| `ThroughputSparkline.test.tsx` › `AC3: ring buffer caps at 60` | `src/features/jobs/ThroughputSparkline.test.tsx` | |
| `JobMonitorPage.test.tsx` › `AC3: reconnecting indicator` | `src/features/jobs/JobMonitorPage.test.tsx` | |

### AC4 — Pause, resume, cancel from the UI
**Given** running, **when** Pause is clicked, **then** `POST /pause` is called and the button shows "Pausing…" until the status event arrives. **When** Cancel is clicked, **then** the confirm dialog appears and confirming calls `/cancel`. **Given** paused, **then** Resume calls `/resume`.

| Test | Location | Verifies |
|---|---|---|
| `JobControls.test.tsx` › `AC4: pause optimistic state` | `src/features/jobs/JobControls.test.tsx` | MSW |
| `JobControls.test.tsx` › `AC4: cancel confirm` | `src/features/jobs/JobControls.test.tsx` | |
| `JobControls.test.tsx` › `AC4: resume` | `src/features/jobs/JobControls.test.tsx` | |

### AC5 — Failed tile and live error feed
**Given** failed > 0, **then** the Failed tile is amber with the ratio and a link. **Given** error-sample events, **then** the feed shows the latest 20, newest first, with friendly messages.

| Test | Location | Verifies |
|---|---|---|
| `StatTile.test.tsx` › `AC5: failed tile thresholds` | `src/features/jobs/StatTile.test.tsx` | |
| `LiveErrorFeed.test.tsx` › `AC5: keeps latest 20 newest first` | `src/features/jobs/LiveErrorFeed.test.tsx` | |

### AC6 — Completion summary
**Given** a `completed` event, **then** the summary card shows the totals, duration, average throughput, and verification result, the report download links exist, the document title shows "✓ Done", and a browser notification is fired if permission was granted.

| Test | Location | Verifies |
|---|---|---|
| `JobSummaryCard.test.tsx` › `AC6: renders final metrics and actions` | `src/features/jobs/JobSummaryCard.test.tsx` | |
| `JobMonitorPage.test.tsx` › `AC6: title and notification on completion` | `src/features/jobs/JobMonitorPage.test.tsx` | Notification API mocked |

### AC7 — Timeline
**Given** job log events, **then** the Log tab shows a chronological timeline with icons per event type, including "Concurrency reduced to 32 (cluster busy)" and "Resumed from checkpoint".

| Test | Location | Verifies |
|---|---|---|
| `TestJobEventLog_AC7_AppendAndList` | `internal/jobs/eventlog_test.go` | |
| `JobTimeline.test.tsx` › `AC7: renders events in order` | `src/features/jobs/JobTimeline.test.tsx` | |

### AC8 — Accessibility & visual quality
The page passes axe with no serious violations in all states, numbers use tabular figures, and dark mode renders with correct tokens (Storybook snapshots for running, paused, completed, completed_with_errors, and failed).

| Test | Location | Verifies |
|---|---|---|
| `JobMonitorPage.test.tsx` › `AC8: axe clean in each state` | `src/features/jobs/JobMonitorPage.test.tsx` | |
| Storybook visual snapshots › `JobMonitor/*` | `src/features/jobs/JobMonitor.stories.tsx` | |

## Implementation notes
- The project has no Storybook, so the `JobMonitor/*` visual snapshots are replaced by the axe checks in every state (`JobMonitorPage.test.tsx` › `AC8`) and a browser run on the dev stack (1,002,000 documents in 19 s, with a pause and resume mid-run, verified from the Review step to the summary card).
- Recharts isn't a dependency; the throughput sparkline is a small SVG (2px line, soft fill, hover readout, accessible name summarising the range).
- The Throughput tile shows the live EWMA while running and the run's average once finished; "peak" is the highest measured per-second rate.
- The confirm button of the cancel dialog reads "Cancel migration" so it can't be confused with the dialog's own dismiss button.
- "Open in Couchbase" is shown only when a console URL is known; it isn't in 1.0.
