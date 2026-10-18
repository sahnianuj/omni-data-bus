# OC-189 — Apache-2.0: licensed, and ready to distribute

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E12 Harden & Ship 2.5 | P0 | 8 | | Implemented |

## User story
As the **maintainer**, I want Omni-Data-Bus released under the Apache License 2.0, with every artifact carrying its license and its dependencies' notices, so that anyone (customers, partners, the community) can use, change and redistribute it, and so that a dependency that would break that can't slip in.

## Background (audit 2026-10-17)
- **The repository has no license.** There's no `LICENSE`, `NOTICE`, SPDX header, `license` field or OCI license label. Legally it's "all rights reserved", so nobody else may redistribute it.
- **The dependencies are compatible.** Everything compiled or bundled into what we ship is under permissive licenses:

  | Shipped in | Packages | Licenses |
  |---|---|---|
  | `odb` / the server binary (Go) | 56 modules | 25 Apache-2.0, 19 MIT, 12 BSD |
  | The web app bundle (npm production deps) | 76 packages | 70 MIT, 2 Apache-2.0, 1 ISC, 1 0BSD, **2 OFL-1.1** (`@fontsource/geist-sans`, `geist-mono`) |
  | The image | distroless `static-debian12` | Apache-2.0 base; Debian packages (ca-certificates MPL-2.0, tzdata public domain, base-files GPL data files, aggregated, not linked) |

  - **The fonts** (OFL-1.1) may be bundled with Apache-2.0 software if their license travels with them, and they keep their own license.
  - **No copied third-party source** was found ("copied from", "adapted from"…).
  - **Test-only modules** (testcontainers…) aren't compiled into anything we ship.
- **The all-in-one image** (OC-072/073) **bundles Couchbase Server Community Edition**, which is under Couchbase's own license, not Apache-2.0. `deploy/appliance/NOTICE` already says so. That image is an aggregate: our part is Apache-2.0, and CE keeps its license.
- **Couchbase** is a trademark of Couchbase, Inc., and the project uses the name throughout. Apache-2.0 §6 grants no trademark rights, so the docs should say so.

## Decisions (2026-10-18, by the code owner, following capellactl's CAP-044)
- **Copyright holder: Couchbase, Inc.** `NOTICE` reads `Copyright 2026 Couchbase, Inc.` and credits the author on a separate line: `Author: Anuj Sahni, Principal Cloud & Solutions Architect`.
- **No per-file license headers.** The terms live only in `LICENSE`, and the copyright in `NOTICE`, as in capellactl.
- **No CLA or DCO.** Contributions are made under the repository's license (Apache-2.0 §5).
- **License: Apache-2.0 for the whole repository**, docs included.
- **File and tool names follow capellactl**: `THIRD_PARTY_NOTICES.md`, `scripts/licence-policy.json`, `make notices` and `make notices-check`.

## Scope
**In:**
- **Repository files**:
  - `LICENSE`: the canonical Apache-2.0 text, unmodified;
  - `NOTICE`: the product name, `Copyright 2026 Couchbase, Inc.`, the author line, the trademark note and pointers to `LICENSE` and `THIRD_PARTY_NOTICES.md`;
  - a *License* section in `README.md`, and a *Dependencies and licences* section in the contributor docs (`make notices` after a dependency change).
- **Third-party notices**: `make notices` writes `THIRD_PARTY_NOTICES.md`, listing every shipped dependency with its version, license and project, and the license texts that must travel with them:
  - Go with `go-licenses` over `./cmd/odb`;
  - web with a production-deps report;
  - the fonts' OFL text.
- **A license gate**: `make notices-check` (offline) fails CI when a **shipped** dependency is missing from `THIRD_PARTY_NOTICES.md`, its license differs, or its license isn't in `scripts/licence-policy.json`.
  - **Allowlist**: Apache-2.0, MIT, BSD-2/3-Clause, ISC, 0BSD, Unlicense, CC0-1.0, BlueOak-1.0.0, OFL-1.1 (fonts only), and MPL-2.0 (unmodified, file-level).
  - **Not allowed**: GPL, LGPL, AGPL, SSPL, BUSL and unknown licenses.
  - **Changing the list** needs the code owner's approval, as in capellactl.
- **Every artifact carries the licenses**:
  - **The image**: `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.md` in `/usr/share/doc/omni-data-bus/`, and the OCI label `org.opencontainers.image.licenses="Apache-2.0"`.
  - **The odb archives** (`.tar.gz`, `.zip`, OC-185/186): `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.md` beside `odb`, so a binary handed on still carries its license.
  - **An export with odb added** (OC-182): `bin/LICENSES/` holds the same three files.
  - **The web app**: *Settings › About › Open-source licenses* opens `/licenses`, which lists the shipped dependencies and their texts and is served from the embedded bundle (no network).
  - **The Helm chart**: `annotations: artifacthub.io/license: Apache-2.0`, plus `LICENSE` in the chart package.
  - **`web/package.json`**: `"license": "Apache-2.0"`.
- **The all-in-one image keeps its own label**: `org.opencontainers.image.licenses="Apache-2.0 AND LicenseRef-Couchbase-CE"`. Its README and the release notes say that the image as a whole isn't Apache-2.0, and its NOTICE keeps the CE terms.
- **Before publishing**: a full-history scan (`gitleaks detect --log-opts=--all`) for secrets, plus a check for customer data and internal hostnames. Findings are fixed, rewriting history if needed, before the repository is made public. This is a release-checklist item.

**Out:**
- relicensing the dev compose's **Couchbase Server Enterprise** image: it's pulled by the user and not redistributed (the compose README notes EE needs a license for production);
- choosing a foundation or governance model;
- legal review, which is the holder's.

## Implementation details
- `go-licenses` (`github.com/google/go-licenses`) is run with `go run` at a pinned version and uses the module cache.
- For the web, a small Node script reads `node_modules/*/package.json` for the production dependency tree, because `license-checker` is unmaintained.
- `/licenses` is a static page generated at build time from `THIRD_PARTY_NOTICES.md` into `web/public/licenses.html`.

## Acceptance criteria & unit tests

### AC1 — The repository is Apache-2.0
**Then** `LICENSE` is the canonical Apache-2.0 text (its SHA-256 matches the published one), `NOTICE` names the holder and the fonts, and `README.md` has a License section.

| Test | Location | Verifies |
|---|---|---|
| `TestLicense_OC189_AC1` | `backend/internal/buildinfo/license_test.go` | LICENSE checksum (the same apache.org text as capellactl's, SHA-256 `cfc7749b…3d30`) |

### AC2 — Copyright and author recorded
**Then** `NOTICE` names the product and contains `Copyright 2026 Couchbase, Inc.` and the author line, and `odb --version` prints `Licensed under Apache-2.0 · Copyright 2026 Couchbase, Inc.`

| Test | Location | Verifies |
|---|---|---|
| `TestLicense_OC189_AC2_NoticeAndVersion` | `backend/internal/buildinfo/license_test.go`, `backend/cmd/odb/main_test.go` | |

### AC3 — Copyleft can't slip in
**Given** a shipped dependency under GPL-3.0 (a test fixture), **then** `make notices-check` fails and names it. The current tree passes.

| Test | Location | Verifies |
|---|---|---|
| `notices-check` job, plus a fixture test | `.github/workflows/ci.yml`, `scripts/notices_test.go` | |

### AC4 — Every artifact carries the licenses
**Then** the image has `/usr/share/doc/omni-data-bus/{LICENSE,NOTICE,THIRD_PARTY_NOTICES.md}` and the license label. Every odb archive has the three files beside `odb`. An export with odb has `bin/LICENSES/`. The chart has the annotation.

| Test | Location | Verifies |
|---|---|---|
| `structure-test.yaml` (files, label) | `deploy/docker/` | |
| `make verify-dist` (archive contents) | `Makefile` | |
| `TestExport_OC189_AC4_OdbLicenses` | `backend/internal/export/script_test.go` | |

### AC5 — Licenses in the app
**Then** *Settings › About* links *Open-source licenses*, which lists every shipped dependency with its license, offline.

| Test | Location | Verifies |
|---|---|---|
| `About.test.tsx` › `OC-189 AC5` | `web/src/features/settings/` | |

### AC6 — The all-in-one image is labeled honestly
**Then** its label is `Apache-2.0 AND LicenseRef-Couchbase-CE`, and its README and NOTICE say what each part is licensed under.

| Test | Location | Verifies |
|---|---|---|
| `structure-test.yaml` | `deploy/appliance/` | |

### AC7 — Safe to publish
**Then** a full-history secret scan is clean (or every finding is resolved), recorded in the release checklist before the repository goes public.

| Test | Location | Verifies |
|---|---|---|
| `gitleaks` job (full history, on demand) | `.github/workflows/ci.yml` | |

## Implementation notes (2026-10-18)
- **`LICENSE`**: the unmodified apache.org text, the same file as capellactl's (SHA-256 `cfc7749b…3d30`).
- **`NOTICE`**:
  - `Omni-Data-Bus`, `Copyright 2026 Couchbase, Inc.` and `Author: Anuj Sahni, Principal Cloud & Solutions Architect`;
  - the Apache-2.0 statement, a pointer to `THIRD_PARTY_NOTICES.md`, the Geist fonts' attribution ((C) 2023 Vercel, OFL-1.1) and the Couchbase trademark note (Apache-2.0 §6).
  - There are no per-file headers and no CLA or DCO, as decided above.
- **`scripts/third-party-notices.py`** (`make notices`, `make notices-check`), adapted from capellactl's:
  - it reads the Go modules compiled into `./cmd/odb` for linux, darwin and windows from the module cache (57 modules), and the npm production tree from `web/node_modules`, with node's resolution (76 packages);
  - it classifies each licence file's text, and an npm package's declared `license`;
  - it writes the tables and every licence text (identical texts grouped), 164 KB in all;
  - it keeps the copies in `backend/internal/legal/` and `deploy/helm/omni-data-bus/LICENSE` identical;
  - `--check` (offline) fails on a stale file, a stale copy or a disallowed licence.

  OpenTelemetry (Apache-2.0 AND BSD-3-Clause) and `gopkg.in/yaml.v3` (Apache-2.0 AND MIT) are listed with both licences.
- **`scripts/licence-policy.json`**: capellactl's list (without the Python-only ids), plus BlueOak-1.0.0 and CC0-1.0. OFL-1.1 is allowed only for `@fontsource/` packages, through a new `only` key. Tests: `scripts/test_third_party_notices.py` (GPL, LGPL, AGPL, SSPL, BUSL and unknown refused; OR/AND/WITH expressions; the font licence only for fonts; text classification).
- **`backend/internal/legal`** embeds the three files:
  - `odb --version` prints a third line, `Licensed under Apache-2.0 · Copyright 2026 Couchbase, Inc.`;
  - the server serves `GET /licenses.txt` (public, plain text, in the OpenAPI spec);
  - an export with odb adds `bin/LICENSES/{LICENSE,NOTICE,THIRD_PARTY_NOTICES.md}`, and its README lists the folder.
- **Artifacts**:
  - **The image**: `/usr/share/doc/omni-data-bus/{LICENSE,NOTICE,THIRD_PARTY_NOTICES.md}` and the OCI labels `licenses=Apache-2.0`, `vendor=Couchbase, Inc.`, `authors=Anuj Sahni`. The structure test checks the files and the label.
  - **The odb archives**: each `.tar.gz` and `.zip` from `scripts/dist.sh` holds the three files beside odb. `make verify-dist` checks them, and the release's macOS and Windows jobs keep them when they repackage signed binaries. Verified on the locally built archives.
  - **The all-in-one image**: labeled `Apache-2.0 AND LicenseRef-Couchbase-CE`. It carries Omni-Data-Bus's three files, and its own `NOTICE.all-in-one` (rewritten) explains the two licences; so does its README.
  - **The Helm chart**: `annotations: artifacthub.io/license: Apache-2.0`, `maintainers: Anuj Sahni`, and `LICENSE` in the chart folder.
  - **`web/package.json`**: `"license": "Apache-2.0"`, `"author": "Anuj Sahni (Couchbase, Inc.)"`.
- **The app**: *Settings › About* shows the licence and copyright line and the author, and links *Open-source licences* (`/licenses.txt`, proxied by Vite in development).
- **Docs**: a *License* section in `README.md` (plus the contents link), *Dependencies and licences* under the contributor workflow, and a release-notes item.
- **AC7**: `gitleaks` over the full history (23 commits) found 7 matches. All are test or development values, reviewed one by one and listed with reasons in `.gitleaksignore`: fixture passwords that the export scanner test must find, the e2e and dev master keys (`dev-only…`, `e2e-only…`), Helm test values, and an invalid key and a truncated PEM block that tests expect to be rejected. The rescan finds no leaks, and CI runs it on every push (`secret-scan`). The untracked `.env` (git-ignored) wasn't flagged.
- **Tests**:
  - `TestLicense_OC189_AC1` and `TestLicense_OC189_AC2_NoticeAndVersion` (`internal/legal`, not `buildinfo`: the files live there);
  - the updated `buildinfo` Text test and `TestVersion_AC1_LdflagsInjected`;
  - `TestExport_OC189_AC4_OdbLicenses`, `TestLicenses_OC189_AC5` (httpapi), and `SettingsPage.test.tsx` › `OC-189 AC5`;
  - the policy tests above;
  - CI jobs `notices` and `secret-scan`.
- **Not run here**: Docker Desktop was stopped, so the container structure tests (image and all-in-one) and the Helm unit tests haven't run. Both Dockerfiles were changed, and the chart lints with the local helm. Run `make image-test`, `make appliance-test` and `make helm-test` when Docker is back.
