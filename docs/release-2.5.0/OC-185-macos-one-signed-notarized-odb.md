# OC-185 — macOS: one signed, notarized odb for Intel and Apple silicon

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 5 | OC-182 | Implemented |

## User story
As a **customer on a Mac**, I want the odb I download (from the README's link, or in an export's `bin/`) to open with no warning, whichever Mac I have, so that my first command is `odb preflight` and not a Gatekeeper dialog or a support ticket.

## Background (found 2026-10-17)
An exported odb was rejected on an Apple M1 Pro that had Rosetta installed. The CPU type wasn't the cause. The cause is that the release builds the darwin binaries with `go build` and never signs them with an Apple Developer ID or notarizes them. cosign signs only `SHA256SUMS`, which macOS doesn't check.
- A browser download marks the zip, and every file unzipped from it, as quarantined.
- Gatekeeper then refuses an unnotarized binary: "odb can't be opened because Apple cannot check it for malware".
- So every customer on macOS hits this, and the only way past it is a Terminal command (`xattr`) that we'd be asking them to run against a security check.

There's a second problem. The customer has to choose between darwin/amd64 and darwin/arm64, and most people don't know which Mac they have. The dev server even offered amd64 to an Apple silicon Mac.

**Options considered:**
- **An OS-independent runner** (Docker, a JVM, Python or WASM). Rejected: each needs something installed first, which is more friction than a binary.
- **`xattr` instructions in the README** (OC-182). Kept only as the fallback for development builds.

## Scope
**In:**
- **One macOS binary**: `lipo -create` of darwin/amd64 and darwin/arm64 gives `odb_<v>_darwin_universal.tar.gz`. It runs natively on both kinds of Mac.
- **Signed and notarized in `release.yml`**, on a `macos-14` runner:
  1. `codesign --sign "Developer ID Application: …" --options runtime --timestamp odb`;
  2. `xcrun notarytool submit --wait` with an App Store Connect API key;
  3. `spctl --assess --type execute` must accept the binary, or the release fails.
  - The secrets: the Developer ID certificate (`.p12`) and its password, and the notary API key ID, issuer and `.p8`. They're held in the `release` environment, so pull requests never see them.
  - `SHA256SUMS` covers the signed binary, and its cosign signature is unchanged.
- **The machine choice** for OC-182's offline binary and the README's *Install odb* section:
  - It lists **macOS (Intel or Apple silicon)**, **Windows** (OC-186), **Linux x86-64** and **Linux ARM64**.
  - The default is the exporter's own OS, from `navigator.userAgentData.platform` or the user agent, falling back to Linux x86-64.
  - `GET /exports/options` reports `darwin_universal`. A 2.0 request for `darwin_amd64` or `darwin_arm64` gets the universal binary.
- **Development builds say so**:
  - the options carry `signed: false` when the server's macOS odb isn't notarized;
  - the dialog then shows "This development build's odb isn't signed, so macOS will block it. Use a release build, or run `xattr -d com.apple.quarantine odb` once";
  - the README says the same only for such builds.
- **The README's download link** for macOS points to the universal tarball.

**Out:**
- **Stapling.** A bare Mach-O binary can't carry a stapled ticket, so Gatekeeper checks notarization online the first time it runs. A Mac with no internet at all would still see a prompt. That needs a signed, notarized and stapled `.pkg`, which is a follow-up if a customer needs it. Air-gapped migrations are almost always on Linux.
- **Windows** (Authenticode signing): a follow-up together with a Windows build.

## Implementation details
- `scripts/dist.sh`: build both darwin architectures, then `lipo` on macOS. On Linux CI, skip the universal binary and keep the per-arch tarballs.
- `release.yml`: a new `odb-macos` job (`needs: odb`). It downloads the darwin artifacts, then signs, notarizes and assesses the universal binary, tars it and adds it to `SHA256SUMS` before cosign signs that file.
- `export.Dist`: `darwin_universal` is a platform. `fallbackPlatform` prefers it for macOS servers, and `Options` gains `signed map[platform]bool`.

## Acceptance criteria & unit tests

### AC1 — Opens with no prompt
**Given** a release odb for macOS, downloaded with a browser on an Apple silicon Mac and again on an Intel Mac, **when** `odb preflight` runs in an export folder, **then** it runs with no Gatekeeper dialog.

| Test | Location | Verifies |
|---|---|---|
| `release.yml` › `spctl --assess` step | `.github/workflows/release.yml` | Fails the release if the binary isn't accepted |
| Manual release check (both Macs, quarantined download) | release checklist | |

### AC2 — One file for both Macs
**Then** `lipo -archs odb` prints `x86_64 arm64`, and `odb version --json` reports the release on both.

| Test | Location | Verifies |
|---|---|---|
| `make verify-dist` | `Makefile` | |

### AC3 — Three plain choices, defaulted to my OS
**Given** the dialog opened on a Mac, **then** *macOS (Intel or Apple silicon)* is selected. Windows, Linux x86-64 and Linux ARM64 are the other choices.

| Test | Location | Verifies |
|---|---|---|
| `ExportDialog.test.tsx` › `OC-185 AC3` | `web/src/features/export/` | |

### AC4 — Older requests and dev builds
**Given** a request for `darwin_arm64`, **then** the export has the universal binary. **Given** a development build, **then** the dialog and the README carry the unsigned-build note, and a release build never does.

| Test | Location | Verifies |
|---|---|---|
| `TestExport_OC185_AC4_DarwinUniversal` | `backend/internal/export/handlers_test.go` | |
| `ExportDialog.test.tsx` › `OC-185 AC4` | `web/src/features/export/` | |

## Implementation notes (2026-10-17)
- **`scripts/dist.sh`** builds `odb_<v>_darwin_universal.tar.gz` with `lipo` when it's available (macOS); on Linux it's skipped. `make verify-dist` checks every archive reports `VERSION`, and that the universal one is `x86_64 arm64` and runs under both (`arch -x86_64` / `arch -arm64`). Verified locally on an M1 Pro.
- **`release.yml`**:
  - `binaries` (Linux) uploads the unsigned archives as `odb-unsigned`.
  - **`odb-macos`** (`macos-14`) builds the universal binary and checks both architectures. On a published tag, from the `release` environment, it then:
    1. imports the Developer ID certificate into a temporary keychain;
    2. runs `codesign --options runtime --timestamp` and `codesign --verify --strict`;
    3. runs `notarytool submit --wait` with an App Store Connect API key;
    4. requires `spctl --assess --type open --context context:primary-signature` to report `source=Notarized Developer ID`. `--type execute` only accepts app bundles, so it can't assess a command-line tool.

    PR dry runs use the `release-check` environment and skip signing.
  - **`odb-sums`** merges every `odb-*` artifact, writes `SHA256SUMS` over the `.tar.gz` and `.zip` archives and `SIGNED` (the archives signed for their OS), and cosign-signs `SHA256SUMS` as before. `release` now needs `odb-sums`. `actionlint` is clean.
  - **Secrets to create in the `release` environment**: `APPLE_DEVELOPER_ID_P12` (base64), `APPLE_DEVELOPER_ID_P12_PASSWORD`, `APPLE_SIGNING_IDENTITY`, `APPLE_NOTARY_KEY_P8`, `APPLE_NOTARY_KEY_ID`, `APPLE_NOTARY_ISSUER`. **Until they exist, the signing steps of a tag build fail.**
- **Server**:
  - `export.Platforms` gains `darwin_universal`. `Dist.Preferred` gives older `darwin_amd64` and `darwin_arm64` requests the universal binary when the dist has it.
  - `Dist.Signed()` reads `SIGNED`. `GET /exports/options` returns `signed`, and `includeOdb.arch` accepts `universal`.
  - The README's download table shows one *macOS (Intel or Apple silicon)* row when there is a universal archive.
  - An included odb that isn't signed gets the `xattr` note (macOS) or the SmartScreen note (Windows).
- **Web**:
  - `machines()` lists macOS (Intel or Apple silicon), Windows x86-64, Linux x86-64 and Linux ARM64. A development server that only has its own per-architecture Mac build lists that build instead.
  - `ownMachine()` picks the default from the browser (`userAgentData.platform`, `platform`, the user agent).
  - An unsigned Mac or Windows choice shows the development-build note.
- **Tests**: `TestExport_OC185_AC4_DarwinUniversal`, and in `ExportDialog.test.tsx`: `OC-185 AC3`, `OC-185 AC4` and the `ownMachine` cases.
- **Not verified here**: AC1, a quarantined browser download opening with no prompt on both Macs. It needs a signed release, and it's a release-checklist item.
