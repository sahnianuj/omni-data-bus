# OC-172 — The release and build are always shown clearly

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E7 Foundations 2.5 | P0 | 2 | OC-058 | Implemented |

## User story
As a **user, support engineer or customer** running the tool, I want every page to show which release I'm on (`2.5.0`) and which build, in words I can quote, so that I can tell at a glance whether I have the version that fixes my problem.

## Background (gap found 2026-10-17)
OC-058 added the footer, the details popover and Settings › About. But the release comes from `git describe --tags --always --dirty`, and the repository has **no release tags**. So every build, including the images we hand to customers, shows a bare commit (`Omni Data Bus 7f6cea2-dirty`), and the footer never says "2.0" or "2.5". The footer is also hidden when the Composer is maximized, which is where customers spend most of their time.

## Scope
**In:**
- **`VERSION` file** at the repository root (`2.5.0`) as the single source of the release. Every build path reads it: Makefile, Dockerfile, the release workflow, the static `odb` binaries and the all-in-one image.
  - Tags (`v2.5.0`) must match it. The release workflow fails otherwise.
- **Separate release and build:**
  - `version` = the release from `VERSION`, with `-dev` appended for untagged builds (`2.5.0-dev`);
  - `build` = the short commit, plus the CI run number when there is one (`7f6cea2+418`), plus `dirty` when the tree had changes.
  - `GET /api/v1/version` returns both, adding `build` (`commit` stays).
- **Footer text** is always `Omni Data Bus 2.5.0 · build 7f6cea2 · 17 Oct 2026`, with the channel badge (`dev`, `rc`) after it. It's never a bare hash.
  - When the Composer is maximized, a compact `v2.5.0` stays in the bottom-right corner, and it opens the same popover.
- **Everywhere else the release appears**, it uses the same text:
  - `odb --version`;
  - the `start` event of `odb run`;
  - the header comment of the exported `migrate.sh` and `mappings.json` (OC-158/159);
  - the verification report (OC-029);
  - the sign-in page (release only).

**Out:** automatic version bumping; update checks (OC-058 makes no outbound calls).

## Implementation details
- Makefile: `VERSION ?= $(shell cat VERSION)`, `CHANNEL` is `release` when `git describe --exact-match` equals `v$(VERSION)`, else `dev`, and `BUILD ?= $(shell git rev-parse --short HEAD)$(if $(CI_RUN),' #'$(CI_RUN))`. They're passed with `-ldflags -X …buildinfo.version/build/channel`.
- `buildinfo.Info` gains `Build`. `Dirty` is computed from `git status --porcelain` at build time, not from the version suffix.
- The web `buildInfoText` (Copy build info) adds `Build:`.

## Acceptance criteria & unit tests

### AC1 — A dev build shows the release
**Given** a build from an untagged commit with `VERSION=2.5.0`, **then** the footer reads `Omni Data Bus 2.5.0-dev · build <short commit> · <date>` with the `dev` badge, and `odb --version` prints the same release and build.

| Test | Location | Verifies |
|---|---|---|
| `TestBuildInfo_OC172_AC1_DevBuild` | `backend/internal/buildinfo/buildinfo_test.go` | |
| `BuildInfoFooter.test.tsx` › `OC-172 AC1` | `web/src/components/BuildInfoFooter.test.tsx` | |
| `make verify-version` | `Makefile` | Builds odb, checks it reports VERSION's release and the build |

### AC2 — Tag and VERSION must agree
**Given** the tag `v2.5.1` while `VERSION` says `2.5.0`, **then** the release workflow fails before building, with "tag v2.5.1 doesn't match VERSION 2.5.0".

| Test | Location | Verifies |
|---|---|---|
| `release.yml` › `check-version` step | `.github/workflows/release.yml` | |

### AC3 — Visible while maximized
**Given** the Composer maximized, **then** `v2.5.0` is visible and opens the build details.

| Test | Location | Verifies |
|---|---|---|
| `AppShell.test.tsx` › `OC-172 AC3` | `web/src/components/AppShell.test.tsx` | |

### AC4 — Exports carry it
**Then** the exported `migrate.sh` header, `mappings.json` and the verification report name the release and build.

| Test | Location | Verifies |
|---|---|---|
| `TestExport_OC172_AC4_VersionHeader` | `backend/internal/export/script_test.go` | |

## Implementation notes (2026-10-17)
- `VERSION` (repository root) holds `2.5.0`. The Makefile derives `VERSION` = `2.5.0` on the commit tagged `v2.5.0`, else `2.5.0-dev`. `BUILD` = short commit plus `+<CI_RUN>` (`7f6cea2+418`); `+` rather than ` #`, because `#` starts a Makefile comment and spaces break `-ldflags -X`. `DIRTY` comes from `git status --porcelain`. All four go through `-ldflags` into `buildinfo` (`buildID`, `dirty`), in the Makefile, `deploy/docker/Dockerfile`, `scripts/appliance.sh` and `release.yml`.
- `release.yml`'s `meta` job fails a tag whose release part differs from `VERSION` ("tag v2.5.1 doesn't match VERSION 2.5.0"). PR dry runs use `<VERSION>-pr<N>`, and every build gets `build = <sha7>+<run number>`.
- `buildinfo.Info.Build` (JSON `build`) defaults to the short commit, and `Info.Summary()` gives "2.5.0 · build 7f6cea2". `odb --version` adds a `Build:` line when the build isn't just the commit. A plain `go build` without ldflags still says `dev`.
- Web: the footer and Settings › About show `build` (falling back to the short commit). `BuildInfoBadge` (`v2.5.0`, bottom-right) replaces the footer while a page is maximized and opens the same details. *Copy build info* has the `Build:` line.
- Exports: the `migrate.sh` and `README.md` headers say "odb 2.5.0 (build 7f6cea2+418)". **`mappings.json` is unchanged**: the bundle schema has `additionalProperties: false`, and 2.0 runners validate against it, so a new field would make them refuse 2.5 exports. The bundle already carries `odbVersion`, and the verification report already records version and commit.
- `make verify-version` builds `odb` and checks that `odb version --json` reports `VERSION`'s release and the build.

- **Fixed after review (2026-10-17): development runs are stamped too.**
  - `make dev` / `scripts/dev.sh` and `make run-backend` ran a plain `go run`, so the footer read *Omni-Data-Bus dev · build none*.
  - They now pass the same `-X buildinfo.*` flags as `make build` (`DEV_LDFLAGS`, without `-s -w`), and `VITE_ODB_VERSION` to Vite. The footer now reads *2.5.0-dev · build 8eb2bc0* (dirty while the tree has uncommitted changes).
  - Run directly, `scripts/dev.sh` works out the same values from `VERSION` and git.
- **Changed after review (2026-10-18): a shorter footer.**
  - The footer reads *v2.5.0-dev · build 8eb2bc0 · Oct 18, 2026* followed by the DEV (or RC) badge, without the product name.
  - *dirty* and *Mixed versions* show only in the build popover, which also keeps the mixed-versions explanation.
  - Tests: `BuildInfoFooter.test.tsx` and `AppShell.test.tsx`; the e2e `build-info.spec.ts` now matches `v<version> · build …`.
