# OC-058 — Release & build information footer

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E7 Foundations 2.0 | P0 | 3 | OC-004, OC-032 | Implemented |

## User story
As a **user, operator or support engineer**, I want the running release and build information shown at the bottom of every page, and copyable in one click, so that I always know which version I'm using and can quote it exactly when I report a problem or check an upgrade.

## Scope
**In:**
- A persistent footer in the app shell showing the release, and on hover or expand the full build details.
- A richer `GET /api/v1/version`: release, commit, build time, Go version, metadata schema version, and the versions of the running worker nodes.
- Build metadata injected in every build path (Makefile, Dockerfile, release workflow, static `odb` binaries for OC-051).
- **Copy build info** for support tickets.
- A mismatch indicator when API and worker nodes run different versions (for example during a rolling upgrade, OC-057).
- The same information from the CLI: `odb --version` and in `odb run`'s `start` event (OC-051).

**Out:** release notes or a changelog viewer in the app (the footer links to the release page instead), and update checks against the internet (the app makes no outbound calls for this).

## Implementation details

### Build metadata (`internal/buildinfo`)
```go
// Set at build time with -ldflags "-X omnidatabus/internal/buildinfo.version=… ".
var (version = "dev"; commit = "none"; buildTime = ""; channel = "dev")
type Info struct {
    Version, Commit, BuildTime, Channel, GoVersion, Platform string
    Dirty bool
}
func Get() Info // GoVersion and Platform from runtime; Dirty when Version ends in "-dirty"
```
- The variables move from `package main` into `internal/buildinfo` so that the HTTP API, the CLI, logs, metrics and exports all read one source.
- `channel`: `release` for tagged builds (`v2.0.0`), `rc` for pre-release tags (`v2.0.0-rc.1`), `dev` otherwise.
- Makefile: `BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)` and `CHANNEL` derived from `VERSION`. The Dockerfile takes `BUILD_TIME` and `CHANNEL` build args. The release workflow sets them from the tag, and uses `SOURCE_DATE_EPOCH` for the build time so builds stay reproducible.
- Startup log line and metric: `odb_build_info{version,commit,channel,go_version} 1` (OC-031 convention).

### API
`GET /api/v1/version` (extends 1.0):
```json
{ "version": "2.0.0", "commit": "3f2a9c1", "buildTime": "2026-11-02T14:05:11Z", "channel": "release",
  "goVersion": "go1.25.3", "platform": "linux/amd64", "metadataSchemaVersion": 7,
  "nodes": { "api": ["2.0.0"], "worker": ["2.0.0"] }, "consistent": true }
```
- **Unauthenticated** requests get only `{version, channel}`: enough for the sign-in page, without handing commit or runtime details to anonymous callers (OC-033). `OC_EXPOSE_BUILD_INFO=false` hides even the version from anonymous callers.
- `nodes` lists the distinct versions of the nodes currently running. Each node writes a small heartbeat document in the metadata store (`nodes::<nodeId>`, `{role, version, commit, seenAt}`, with a 60 s expiry), refreshed every 20 s. Worker heartbeats already exist for jobs (OC-023), so this adds the version to them. `consistent: false` when more than one version is present.
- Cached for 10 s per API node.

### UI (`src/components/BuildInfoFooter.tsx`)
- A slim footer (24 px, muted text, `role="contentinfo"`) at the bottom of the app shell on every page, and on the sign-in page with the reduced data:
  `Omni-Data-Bus 2.0.0 · build 3f2a9c1 · 2 Nov 2026` (the date in the user's locale; the full UTC timestamp in the tooltip).
- Clicking it opens a popover with all fields, the connected nodes, a link to the release notes for that version (`https://github.com/<org>/omni-data-bus/releases/tag/v<version>`; hidden for `dev`), and a **Copy build info** button that copies a plain-text block:
  ```
  Omni-Data-Bus 2.0.0 (release)
  Commit: 3f2a9c1  Built: 2026-11-02T14:05:11Z  Go: go1.25.3  Platform: linux/amd64
  Metadata schema: 7   Nodes: api 2.0.0, worker 2.0.0   Browser: <user agent>
  ```
- Badges: `DEV` or `RC` for non-release channels, `dirty` when built from uncommitted changes, and an amber **Mixed versions** badge when `consistent` is false, whose popover lists which roles run which version.
- **Stale tab detection:** the SPA embeds the version it was built with. When the server's version differs (after an upgrade), the footer shows "A new version (2.0.1) is available — Reload". The SPA is served by the same binary, so a mismatch means an upgrade happened while the tab was open.
- The footer stays out of the way: it doesn't overlap the mapping canvas or composer (they size to the remaining height), it is included in print styles, and it passes the axe check in both themes.
- The Settings → About section shows the same details.

### CLI & exports
- `odb --version` prints the same text block as **Copy build info**. `odb version --json` prints the API JSON shape (without `nodes`).
- `odb run` includes `{version, commit}` in its `start` event (OC-051), and `report.json` records it, so imported external runs (OC-055) show which build produced them.
- Exported scripts' header comment already names the odb version (OC-053). It now includes the commit too.

## Acceptance criteria & unit tests

### AC1 — Build metadata injected everywhere
**Given** `make build` with `VERSION=v2.0.0`, **then** `odb --version` shows 2.0.0, the commit, a UTC build time and channel `release`. **Given** `v2.0.0-rc.1`, **then** channel `rc`. **Given** a plain `go build`, **then** `dev`/`none` with no crash.

| Test | Location | Verifies |
|---|---|---|
| `TestBuildInfo_AC1_ChannelFromVersion` | `internal/buildinfo/buildinfo_test.go` | |
| `TestBuildInfo_AC1_Defaults` | same | |
| `structure-test` › `AC1: image reports version` | `deploy/docker/structure-test.yaml` | Runs `odb --version` in the image |

### AC2 — Version API by audience
**Given** a signed-in user, **then** `GET /version` returns all fields. **Given** an anonymous request, **then** only `version` and `channel`. **Given** `OC_EXPOSE_BUILD_INFO=false`, **then** anonymous gets `{}`.

| Test | Location | Verifies |
|---|---|---|
| `TestVersionAPI_AC2_AuthenticatedFull` | `internal/httpapi/version_test.go` | |
| `TestVersionAPI_AC2_AnonymousReduced` | same | |

### AC3 — Footer on every page
**Given** any authenticated route, **then** the footer shows the release, short commit and localized build date. **Given** the sign-in page, **then** it shows the release only. **When** **Copy build info** is clicked, **then** the clipboard holds the documented block.

| Test | Location | Verifies |
|---|---|---|
| `BuildInfoFooter.test.tsx` › `AC3: renders release, commit, date` | `src/components/BuildInfoFooter.test.tsx` | MSW |
| `BuildInfoFooter.test.tsx` › `AC3: copy build info` | same | |
| `AppShell.test.tsx` › `AC3: footer present on all shell routes` | `src/components/AppShell.test.tsx` | |

### AC4 — Mixed versions
**Given** node heartbeats for api 2.0.0 and worker 1.0.1, **then** `consistent: false`, and the footer shows the **Mixed versions** badge listing both. **Given** a node whose heartbeat expired, **then** it's no longer counted.

| Test | Location | Verifies |
|---|---|---|
| `TestNodeRegistry_AC4_VersionsAndExpiry` *(integration)* | `internal/store/nodes_integration_test.go` | |
| `BuildInfoFooter.test.tsx` › `AC4: mixed versions badge` | `src/components/BuildInfoFooter.test.tsx` | |

### AC5 — Stale tab prompt
**Given** a SPA built as 2.0.0 and a server now reporting 2.0.1, **then** the footer shows the reload prompt. **Given** equal versions, **then** no prompt.

| Test | Location | Verifies |
|---|---|---|
| `BuildInfoFooter.test.tsx` › `AC5: stale tab reload prompt` | `src/components/BuildInfoFooter.test.tsx` | |

### AC6 — Channel badges & accessibility
**Given** channel `dev` or `rc`, or a dirty build, **then** the matching badge shows and the release-notes link is hidden for `dev`. The footer passes axe in light and dark themes and doesn't cover canvas content.

| Test | Location | Verifies |
|---|---|---|
| `BuildInfoFooter.test.tsx` › `AC6: badges and axe` | `src/components/BuildInfoFooter.test.tsx` | |
| `build-info.spec.ts` *(E2E)* | `web/tests/e2e/build-info.spec.ts` | Footer visible, canvas not overlapped |

### AC7 — Runner reports its build
**Given** `odb run`, **then** the `start` event and `report.json` contain `version` and `commit`. **Given** an imported external report, **then** the job page shows that build.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_AC7_BuildInfoInStartAndReport` | `internal/headless/progress_test.go` | |

## Implementation notes (as built)
- **`internal/buildinfo`** holds `version`, `commit`, `buildTime` and `channel` (ldflags `-X omnidatabus/internal/buildinfo.<name>`). A tag's `v` is dropped (`v2.0.0` → `2.0.0`). When no `CHANNEL` is passed, it's derived from the version: `release`, `rc` (also `-beta` and `-alpha`) or `dev`. `app.BuildInfo` is now an alias of `buildinfo.Info`.
- **Node registry**: a single `system::nodes` document holds every node's heartbeat and is updated with CAS. Each beat also prunes entries older than 60 s. We used one document instead of one `nodes::<id>` document per node because listing per-node documents needs a SQL++ index. With one document, reads stay KV-only and provisioning doesn't change. Every node beats every 20 s whatever its role (`internal/app/nodes.go`). An `all` node counts for both `api` and `worker`.
- **`/api/v1/version`** now runs after the session middleware, which never rejects a request, so it can tell signed-in callers from anonymous ones. If the metadata store fails, the response falls back to this node alone rather than an error, so the footer still renders.
- **Metric**: `oc_build_info{version,commit,channel,go_version} 1`, not `odb_build_info`. Every OC-031 metric, dashboard and the asset lint use the `oc_` prefix. There is a new alert, `OmniDataBusMixedVersions` (more than one version for 30 min).
- **SPA version**: taken from `VITE_ODB_VERSION`, which `make build` and the Dockerfile's web stage set to `VERSION`. A Vite dev build is `dev` and never shows the reload prompt. `/version` is refetched every 5 minutes and on window focus, so a tab left open notices an upgrade.
- **CLI**: `odb --version` and `odb version` print the text block. `odb version --json` prints `buildinfo.Info`.
- **AC7 is handed off**: `odb run`, `report.json`, imported runs and exported scripts don't exist yet. OC-051 (start event and report), OC-053 (the script header's commit) and OC-055 (the imported run's build on the job page) read `buildinfo.Get()` and own the `TestRun_AC7_BuildInfoInStartAndReport` test.
- The footer is `src/components/BuildInfoFooter.tsx`. Data hooks and the text block are in `src/features/system/buildInfo.ts`. The shared MSW server answers `GET /version` by default, because every page now calls it.
