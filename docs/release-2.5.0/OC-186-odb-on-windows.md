# OC-186 — odb on Windows: the same commands as on macOS and Linux

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 5 | OC-181 | Implemented |

## User story
As an **operator who tests exports on a Windows laptop**, I want `odb preflight`, `odb run` and the rest to behave on Windows exactly as they do on the Linux server, so that a test on my laptop tells me what will happen in production.

## Background (2026-10-17)
odb builds for Windows with no code changes (`CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/odb` gives a 51 MB `odb.exe`). But it has never been released, signed or tested on Windows. Before OC-182 it couldn't have been used anyway: `migrate.sh` needs bash.

## Scope
**In:**
- **Release**:
  - `scripts/dist.sh` and `release.yml` build `windows/amd64` as `odb_<v>_windows_amd64.zip` (a zip, not a tarball), included in `SHA256SUMS`.
  - The release also builds `windows/arm64` as the same kind of zip, but it isn't tested in CI.
- **Signed**: Authenticode with the Couchbase code-signing certificate (`signtool sign /fd sha256 /tr <timestamp URL> /td sha256`) on a `windows-2022` runner.
  - `signtool verify /pa` must pass, or the release fails.
  - This prevents the SmartScreen "Windows protected your PC" warning on a downloaded `odb.exe`.
- **Same behavior as Linux**:
  - **Ctrl-C** (and closing the console) saves the checkpoint and exits with 5. Go delivers `os.Interrupt` on Windows, but not `SIGTERM`; the run code mustn't depend on the latter.
  - **Paths**: `--config`, `--mappings`, `--state-dir` and `ODB_*` accept `C:\…` paths and forward slashes. The OC-180 lookup "beside the binary" handles `odb.exe`. A directory target's `path` in `config.json` may be a Windows path when run on Windows.
  - **Files**: `config.json`'s 0600 check (if any) becomes "readable only by you" via the file ACL on Windows, or is skipped with a note. `odb-state` writes use replace-by-rename, which on Windows must retry when the file is briefly locked (antivirus).
  - **Terminal**: the OC-181 text output works in Windows Terminal, PowerShell and cmd. Colors are used only when the console supports VT sequences, and the output is plain otherwise.
  - **Exit codes** are the same numbers (`$LASTEXITCODE` / `%ERRORLEVEL%`).
- **Directory target on Windows**: file names that are invalid on Windows (`:`, `*`, `?`, `"`, `<`, `>`, `|`, reserved names like `CON`) are refused at preflight, with the key that produced them. An export tested on Windows thus won't silently differ from Linux.
- **The README** (OC-182) shows the PowerShell form only where it differs, for setting `ODB_*` (`$env:ODB_STATE_DIR="D:\state"`).

**Out:** Windows Server as a production target (it should work but isn't tested); an MSI installer; winget.

## Acceptance criteria & unit tests

### AC1 — The end-to-end test on Windows
**Then** OC-184's `export-odb.spec.ts` passes on the `windows-2022` CI runner:
1. export;
2. unzip;
3. `odb preflight`, `odb run`, `odb verify`;
4. a Ctrl-C during `odb run`, then `odb resume`.

The resulting `report.json` totals equal the Linux run's.

| Test | Location | Verifies |
|---|---|---|
| `export-odb.spec.ts` (windows-2022) | `web/tests/e2e/` | |
| `TestCLI_OC186_AC1_InterruptResume` (`//go:build windows`) | `backend/cmd/odb/run_windows_test.go` | |

### AC2 — Windows paths
**Given** `--state-dir C:\odb\state` and a config with a directory target `D:\out`, **then** the run writes there, and `odb status` reads it back.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_OC186_AC2_WindowsPaths` (`//go:build windows`) | `backend/cmd/odb/run_windows_test.go` | |

### AC3 — Invalid file names caught at preflight
**Given** a directory target with a key template that produces `order:1`, **then** preflight exits with 4 on Windows and names the key template. On Linux it passes.

| Test | Location | Verifies |
|---|---|---|
| `TestPreflight_OC186_AC3_WindowsFileNames` | `backend/internal/headless/preflight_test.go` | |

### AC4 — Signed
**Then** the release fails unless `signtool verify /pa odb.exe` passes. A browser-downloaded `odb.exe` runs with no SmartScreen prompt (release checklist).

| Test | Location | Verifies |
|---|---|---|
| `release.yml` › `signtool verify` step | `.github/workflows/release.yml` | |

## Implementation notes (2026-10-17)
- **Build**:
  - `scripts/dist.sh` builds `odb_<v>_windows_amd64.zip` and `odb_<v>_windows_arm64.zip`, each holding `odb.exe`, reproducibly (fixed mtime, `zip -X`), and includes them in `SHA256SUMS`;
  - `make verify-dist` checks each zip holds `odb.exe`;
  - `export.Platforms` gains `windows_amd64` and `windows_arm64`; `Archive()` names the `.zip`, `binaryFromArchive` reads `odb.exe` from it, and exports add it as `bin/odb.exe`.
- **Release** (`odb-windows`, `windows-2022`):
  - It unpacks both zips and smoke-tests `odb.exe version` on amd64.
  - On a published tag it signs both with `signtool sign /fd sha256 /tr <timestamp> /td sha256`, then requires `signtool verify /pa`.
  - It repackages the zips, lists them in `SIGNED`, and `odb-sums` merges them. The signed archives win over the unsigned build's files of the same name (`cp -n`).
  - Secrets: `WINDOWS_SIGNING_PFX` (base64) and `WINDOWS_SIGNING_PFX_PASSWORD` in the `release` environment, plus an optional `WINDOWS_TIMESTAMP_URL` variable.
  - **Caveat**: code-signing certificates issued since June 2023 keep their key on an HSM and can't be exported as a PFX. With a cloud HSM (Azure Trusted Signing, DigiCert KeyLocker…), replace the *Sign* step with the provider's setup. The `verify /pa` check stays.
- **Behavior**:
  - **Ctrl-C / Ctrl+Break** are delivered by Go as `os.Interrupt`, and closing the console as `SIGTERM`. The existing drain path handles both, so the checkpoint is saved and the exit code is 5.
  - **The console** is switched to UTF-8 (`SetConsoleOutputCP(65001)`, `cmd/odb/console_windows.go`), so `·` and names in any script print correctly in cmd and PowerShell. The text output uses no colors, so no VT setup is needed.
  - **State writes** (`writeAtomic`, `replaceFile`) go through `headless.rename`, which on Windows retries a replace-by-rename for about two seconds when antivirus or the indexer briefly holds the file.
  - **The config.json mode**: odb has no 0600 check to adapt (`Chmod` on Windows only sets read-only), so there's nothing to change.
- **AC3, changed**: document file names were already Windows-safe on every OS. pathcodec (OC-039) encodes `:` as `%3A`, so `orders::1` is written as `orders%3A%3A1.json` on Linux, macOS and Windows alike. Refusing at preflight would have been wrong. The one gap was **folders**: `mapping.ValidateFolder` (and its web mirror `validFolderName`) now refuses Windows-reserved names (`con`, `aux.json`, `com1`…) and names ending in `.`, **on every OS**. A folder that passes on the laptop therefore also works on the server.
- **Tests**:
  - `TestRunner_OC186_AC1_InterruptResume` (`cmd/odb/runner_integration_test.go`, `-tags integration`) runs this test binary as odb (`TestMain`, `ODB_TEST_AS_ODB=1`) in an export folder: preflight, a throttled run interrupted with SIGINT (Ctrl+Break in a new process group on Windows, `interrupt_windows_integration_test.go`) → exit 5, status, resume, verify. It then checks the report (10,000 orders, every one once) and the encoded file names. **It passed locally against the dev stack's PostgreSQL** (macOS).
  - `TestRun_OC186_AC2_WindowsPaths` (`//go:build windows`), `TestValidateFolder_OC186_AC3_WindowsNames`, `validFolderName (OC-186)` (web), `TestExport_OC186_WindowsZip`, and `ExportDialog.test.tsx` › `OC-186`.
  - **CI**: a new `runner-windows` job in `ci.yml` starts the runner's preinstalled PostgreSQL, loads `deploy/compose/seed/source/*.sql`, then runs the odb, headless, export, mapping and pathcodec unit tests and the end-to-end test on Windows. It hasn't run yet: it runs on the next push.
- **Not verified here**: the Windows-only tests (no Windows machine here; they compile with `GOOS=windows`), Authenticode signing (needs the certificate) and the SmartScreen check (AC4, release checklist).
