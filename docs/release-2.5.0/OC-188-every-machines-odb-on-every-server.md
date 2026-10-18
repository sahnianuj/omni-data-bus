# OC-188 — Every machine's odb on every server

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 5 | OC-182, OC-185, OC-186 | Implemented |

## User story
As an **operator who tests on a Mac and runs on a Linux server without internet access**, I want the export dialog to add odb for whichever machine I pick (Linux, Windows or Mac), whatever machine the Omni-Data-Bus server itself runs on, so that I don't have to find, download and copy the right binary myself.

## Background (found 2026-10-17)
*Add odb for a machine without internet access* lists macOS, Windows x86-64, Linux x86-64 and Linux ARM64. On a development server only one choice is enabled: **macOS (Intel)**, the server's own binary. The others are greyed out with no reason given.

The server can add only the binaries it has:
- **its own** (`os.Executable`), for its own OS and CPU. On a laptop that's darwin/amd64, because Go runs under Rosetta there;
- **those in `OC_EXPORT_DIST_DIR`**, the release archives an admin copied onto it, which no environment sets today.

A released server is in the same position: its README can link the downloads, but it can't put a Linux binary into the folder. Bundling every machine's odb in the image isn't an option. The seven archives are about 93 MB, and the image's gate is 40 MB (`make image-test`).

## Scope
**In:**
- **Fetch on first use.** A server without a dist folder gets a machine's odb from its release the first time an export asks for it:
  - from `OC_EXPORT_DOWNLOAD_URL`, which defaults to this version's GitHub release;
  - checked against **`SHA256SUMS` built into the server image**. The release's `image` job takes the `odb-sums` output (a few hundred bytes, plus `SIGNED`), so the image knows every archive's checksum and whether it's signed, with no cosign at run time;
  - cached in `OC_EXPORT_CACHE_DIR` (default: a folder under the server's temp directory; Helm: an `emptyDir`), so later exports don't download it again;
  - one download at a time per archive, timed out after 2 minutes. A checksum mismatch deletes the download and fails the export with `ODB_DOWNLOAD_INVALID`. Nothing is cached.
- **The dist folder** (`OC_EXPORT_DIST_DIR`) keeps priority: an air-gapped server, or one that shouldn't call out, uses what an admin put there.
  - Helm: `exports.distDir` mounts a volume, or an existing claim, holding a release's odb assets.
  - `.env.example` and the upgrade note say how to fill it from the release.
- **Options say what's available and why.** `GET /exports/options` returns, per machine:
  - `available` (local or cached);
  - `onDemand` (downloadable on first use: checksum known, download URL set);
  - `unavailable` (with a reason: no checksum in this build, downloads off, or the last download failed).

  The dialog:
  - **enables** available and on-demand machines. An on-demand machine says *downloaded on first export (≈12 MB)*, and Export shows *Getting odb for Linux x86-64…* while it fetches;
  - **disables** unavailable ones **with the reason** as their description, and a hint for admins (*set OC_EXPORT_DIST_DIR to the release's odb files*).
- **Development**: `make dev` and `scripts/dev.sh` run `make dist` when `dist/odb` is missing or older than the backend sources, and start the server with `OC_EXPORT_DIST_DIR=dist/odb`. A dev server then offers every machine, including the universal Mac binary when `lipo` is there. The files are unsigned, so the OC-185 note shows.
- **Fetch errors** (no route, 404, timeout) fail the export with a message naming the machine and the URL, not a generic 500. The machine then shows as unavailable, with that reason, for 10 minutes before trying again.

**Out:**
- building binaries on the server;
- mirrors other than the one download URL;
- prefetching every machine at startup (a later option: `OC_EXPORT_PREFETCH=all`).

## Implementation details
- `export.Dist` gains a `Fetcher`: `HTTPGet`, the expected checksums (from the embedded `SHA256SUMS`) and a cache dir. `Dist.Binary(p)` looks in the dist dir, then the cache, then downloads (verifying), then falls back to its own binary.
- `Dist.Available()` becomes `Dist.Machines()`, returning `[]Machine{Platform, State, Reason, Bytes}`. `Options.embeddable` stays as the available and on-demand platforms, for older clients.
- `deploy/docker/Dockerfile` copies `dist/odb/SHA256SUMS` and `SIGNED` (a build context file the release's `image` job downloads from `odb-sums`) to `/usr/local/share/odb/`. A local `make image` without them gets empty files, and only the server's own platform is available.
- `release.yml`: `image` needs `odb-sums` and downloads its `SHA256SUMS` and `SIGNED` into the build context.
- Development: in `scripts/dev.sh`, a `dist/odb/.stamp` holding the newest backend source mtime decides whether to rebuild.

## Acceptance criteria & unit tests

### AC1 — A development server offers every machine
**Given** `make dev`, **then** the dialog enables macOS (Intel or Apple silicon), Windows x86-64, Linux x86-64 and Linux ARM64. Exporting with Linux x86-64 adds a `bin/odb` that runs on Linux (`file` reports an ELF x86-64 executable).

| Test | Location | Verifies |
|---|---|---|
| `TestDevDist_OC188_AC1` (`scripts/dev.sh` stamp logic) | `scripts/dev_test.sh` or a Go test of the helper | |
| Manual | release checklist | Dialog on `make dev` |

### AC2 — Fetched once, verified, cached
**Given** a server without a dist folder whose image knows the checksums, **when** an export picks Linux x86-64, **then** the archive is downloaded from `OC_EXPORT_DOWNLOAD_URL`, checked against the embedded SHA-256 and cached, and the export holds `bin/odb`. A second export doesn't download again.

| Test | Location | Verifies |
|---|---|---|
| `TestDist_OC188_AC2_FetchVerifyCache` (httptest server) | `backend/internal/export/script_test.go` | |

### AC3 — A bad download is refused
**Given** a download whose SHA-256 differs, **then** the export fails with `ODB_DOWNLOAD_INVALID` naming the machine. Nothing is cached, and the next try downloads again.

| Test | Location | Verifies |
|---|---|---|
| `TestDist_OC188_AC3_ChecksumMismatch` | `backend/internal/export/script_test.go` | |

### AC4 — Unavailable machines say why
**Given** no dist folder and no download URL (or no checksum for a machine), **then** options mark the machine `unavailable` with its reason. The dialog shows the reason under the disabled choice, and the admin hint.

| Test | Location | Verifies |
|---|---|---|
| `TestOptions_OC188_AC4_MachineStates` | `backend/internal/export/handlers_test.go` | |
| `ExportDialog.test.tsx` › `OC-188 AC4` | `web/src/features/export/` | |

### AC5 — Air-gapped servers use the dist folder
**Given** `OC_EXPORT_DIST_DIR` holding the release's archives and no network, **then** every machine in it is available, and no download is attempted. Helm's `exports.distDir` mounts it.

| Test | Location | Verifies |
|---|---|---|
| `TestDist_OC188_AC5_DistDirFirst` | `backend/internal/export/script_test.go` | |
| `helm-unittest` › `exports.distDir` | `deploy/helm/omni-data-bus/tests/` | |

## Implementation notes (2026-10-18)
- **`backend/internal/export/fetch.go`**:
  - `Fetcher` holds the release URL, the image's checksum folder (`SumsDir`), the cache folder and an HTTP client. `Dist.Fetch` points to it.
  - `Dist.Binary(ctx, p)` looks in the dist folder, then the verified cache, then downloads (verified and cached), then uses the server's own binary.
  - Each download runs one at a time per archive, with the export's context and a 2-minute timeout. It's capped at 200 MB, written to a temporary file, SHA-256-checked, then renamed into the cache. A mismatch or failure leaves nothing behind.
  - A cached file is re-hashed on use: a tampered one is removed and fetched again.
- **Checksums**: `SHA256SUMS` and `SIGNED` come from the dist folder when it has them, else from the image (`/usr/local/share/odb`, `OC_EXPORT_SUMS_DIR`). So a released server without a dist folder now also lists the downloads in export READMEs.
- **`Dist.Machines()`** gives each platform one of three states:
  - `available`: in the dist folder, cached, or the server's own;
  - `onDemand`: the release's checksum is known and downloads are on;
  - `unavailable`, with a reason: downloads off and nothing local, no checksum (a development build), or a failed download in the last 10 minutes, with "Trying again after HH:MM".

  `GET /exports/options` returns `machines`, and `embeddable` = available + on demand.
- **Errors**: a failed download returns **502** with `ODB_DOWNLOAD_FAILED` (no route, an HTTP status, a timeout, naming the URL) or `ODB_DOWNLOAD_INVALID` (a checksum mismatch). Both are registered in `problem` and the OpenAPI code enum.
- **Settings**:
  - `OC_EXPORT_FETCH` (default `true`), `OC_EXPORT_CACHE_DIR` (default `$TMPDIR/odb-export-cache`) and `OC_EXPORT_SUMS_DIR` (default `/usr/local/share/odb`);
  - Helm: `exports.fetch`, `exports.cacheSize` (a 256 MiB `emptyDir` at `/var/cache/odb`, because the pod's `/tmp` is limited to 64 MiB), and `exports.distDir.existingClaim`/`mountPath` (a read-only PVC that sets `OC_EXPORT_DIST_DIR`).
- **Image**:
  - `deploy/docker/Dockerfile` copies `deploy/docker/odb-sums/` to `/usr/local/share/odb/`. The folder is kept by a `.gitkeep`, and its other files are ignored.
  - In `release.yml`, `image` now needs `odb-sums` and copies its `SHA256SUMS` and `SIGNED` into that folder before building.
  - The structure test checks the folder exists.
- **Development**:
  - `scripts/dev-dist.sh <version> [ldflags]` rebuilds `dist/odb` (all seven archives, plus the universal Mac binary with `lipo`) when the version changed or a backend `.go`/`go.mod`/`go.sum` is newer than `dist/odb/.stamp`. It takes about 58 s on an M1 Pro, and is instant when current.
  - `scripts/dev.sh` runs it **in the background** and exports `OC_EXPORT_DIST_DIR=dist/odb`. The server reads the folder on each request, so the other machines appear once the build is done, and startup isn't delayed. A failed build writes `dist-build.log`, which is ignored.
- **Web**:
  - An on-demand machine reads *Linux x86-64 — downloaded on first export*, with a note that it's fetched once, checked and kept.
  - Unavailable machines stay disabled, and a line under the select gives their reason, grouped by reason.
  - Export reads *Getting odb for Linux x86-64…* while it downloads.
- **Tests**:
  - `TestDist_OC188_AC2_FetchVerifyCache`, `TestDist_OC188_AC3_ChecksumMismatch`, `TestDist_OC188_DownloadFailed`, `TestOptions_OC188_AC4_MachineStates` and `TestDist_OC188_AC5_DistDirFirst` (`fetch_test.go`, against an `httptest` release);
  - `TestExportsAPI_OC188_DownloadFailure` (502);
  - `ExportDialog.test.tsx` › `OC-188 AC4`;
  - Helm `exports_test.yaml` (cache volume; air-gapped dist mount).
- **Not run here**:
  - **The Helm unit tests**: Docker Desktop was stopped. The chart lints with the local helm 3.4, and both value sets render as expected. Run `make helm-test` when Docker is back.
  - **AC1's dialog check** on a running `make dev`: `scripts/dev-dist.sh` was run and built every archive, and the server side is covered by `TestDist_OC188_AC5_DistDirFirst`.
