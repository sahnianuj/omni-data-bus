# OC-182 — The export runs anywhere: `config.json`, `mappings.json` and the `odb` command, with no script

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 5 | OC-180, OC-181 | Implemented |

## User story
As an **operator**, I want an export folder that has nothing OS-specific in it and is run by the same `odb` command on macOS, Windows and Linux, so that I can test it on my laptop and then copy the same folder to the Linux server and run the same commands.

## Background (2026-10-17)
With OC-180 and OC-181, everything `migrate.sh` does is in odb except getting odb itself. The script also brings costs:
- bash 3.2+, curl or wget, and sha256sum or shasum on the target machine;
- a bats suite on two shells in CI;
- a file that security reviews read line by line;
- and it doesn't run on Windows.

The typical flow is to test the export on a Mac or Windows laptop, then run it on a Linux server. A binary for one OS inside the folder breaks that flow: the folder tested on the laptop isn't the folder that runs on the server. So odb is **installed once per machine**, like any CLI, and the export holds only what the migration does and where it runs.

## Scope
**In:**
- **The export holds no script and no binary by default:**

  | File | Mode | Contents |
  |---|---|---|
  | `config.json` | 0600 | where (OC-155) |
  | `mappings.json` | 0644 | what (OC-158) |
  | `README.md` | 0644 | how to run it |

  `migrate.sh` is no longer exported. The same folder runs unchanged on every OS.
- **The same commands everywhere.** The README starts with:
  ```
  odb preflight
  odb run
  odb status
  ```
  These are run in the export folder from Terminal, PowerShell, cmd or a Linux shell. Then come the exit codes, the `ODB_*` settings and the Docker command (OC-183).
- **Installing odb**: the README's *Install odb* section gives, for each OS, the release download link and its SHA-256 (from the server's checksums), plus one line to put it on `PATH`:
  - macOS: universal, signed and notarized (OC-185);
  - Windows: `odb.exe`, signed (OC-186);
  - Linux x86-64 and ARM64: static.

  `odb version` must report the export's release. If it doesn't, preflight says which version to install (the version check `migrate.sh` used to do).
- **Optional: odb for a machine without internet access.** The *Options* step keeps one checkbox, unchecked by default: **Add odb for a machine without internet access**, with the machine (macOS, Windows, Linux x86-64 or Linux ARM64; OC-185 sets the default).
  - It adds `bin/odb` (or `bin/odb.exe`). The README then says to run `bin/odb preflight` (`bin\odb.exe` on Windows) or to add `bin` to `PATH`.
  - The rest of the folder is unchanged, so it still runs on other machines with an installed odb.
  - When the server has no odb binaries, the checkbox is disabled, and the note says to install odb on the target machine instead.
- **The success panel and the run history** list the files that were exported.
- **The API**: `POST /projects/{id}/exports` keeps `includeOdb`, and omitting it now means no binary.
  - A 2.0 request with `script.runtime: "embedded"` adds `bin/odb` for that platform.
  - `X-Odb-Bundle-Sha256` and report import (OC-055) are unchanged, because they hash `mappings.json`.

**Out:** package-manager installs (Homebrew, winget, apt): a follow-up.

## Implementation details
- `backend/internal/export`:
  - `script.go` stops rendering `migrate.sh.tmpl`, and the included binary goes under `bin/`;
  - `README.md.tmpl` is rewritten with *Run* first, then *Install odb* per OS;
  - the template, `bats_test.go` and `testdata/bats/` are kept for one release for the OC-181 AC2 compatibility test, then deleted in 3.0.
- `web/src/features/export/ExportDialog.tsx`: the checkbox goes back to unchecked by default with the new label, and the `export.run.*` and `export.files.*` strings change. This reverts the 2026-10-17 change that checked it by default.
- `api/openapi.yaml`: the descriptions of `includeOdb` and the export response.

## Acceptance criteria & unit tests

### AC1 — One folder, every OS
**Given** a default export, **then** the zip holds exactly `config.json` (0600), `mappings.json` and `README.md`. **When** it's unzipped on macOS, Windows and Linux with odb installed, **then** `odb preflight` passes on all three without any change to the folder.

| Test | Location | Verifies |
|---|---|---|
| `TestExport_OC182_AC1_Files` | `backend/internal/export/handlers_test.go` | |
| `export-odb.spec.ts` › `OC-182 AC1` | `web/tests/e2e/` | Runs on the Linux, macOS and Windows CI runners (OC-184) |

### AC2 — The installed odb must match
**Given** an installed odb from another release, **then** `odb preflight` exits with 4 and says "this export needs odb 2.5.0; you have 2.0.1", with the download link from the README.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_OC182_AC2_VersionMismatch` | `backend/cmd/odb/run_test.go` | |

### AC3 — odb for an offline machine
**Given** *Add odb* for Linux x86-64, **then** the zip also holds `bin/odb` (0755), and `bin/odb preflight` passes in the unzipped folder. **Given** Windows, **then** it holds `bin/odb.exe`.

| Test | Location | Verifies |
|---|---|---|
| `TestExport_OC182_AC3_OfflineBinary` | `backend/internal/export/handlers_test.go` | |

### AC4 — The dialog
**Then** the Options step shows *Add odb for a machine without internet access*, unchecked. Checking it shows the machine choice. With no odb on the server, the checkbox is disabled and the note explains why.

| Test | Location | Verifies |
|---|---|---|
| `ExportDialog.test.tsx` › `OC-182 AC4` | `web/src/features/export/` | |

### AC5 — Older requests
**Given** a 2.0 request body (`runtime: "script"`, `script.runtime: "embedded"`, linux/amd64), **then** the export is the same folder with `bin/odb` for linux/amd64.

| Test | Location | Verifies |
|---|---|---|
| `TestExport_OC182_AC5_LegacyRequest` | `backend/internal/export/handlers_test.go` | |

## Implementation notes (2026-10-17)
- **`export.RenderScript`** writes `README.md`, `config.json` (0600) and `mappings.json`. With `includeOdb`, it adds `bin/` and `bin/odb` (0755), or `bin/odb.exe` for Windows (`OdbPath`). `migrate.sh` is no longer rendered into exports. `GenericScript` and the template stay for the 2.0 compatibility tests until 3.0.
- **Auto-embed is gone.** The server no longer adds its own odb when there are no download checksums (`EmbedWhenNoDownload`, `fallbackPlatform`, `Options.autoEmbed` were removed), because an OS-neutral folder must not carry a binary nobody asked for. Without downloads, the README says to ask the administrator for odb or to export with odb added.
- **README.md** is rewritten, in this order:
  1. the files;
  2. *Run it* (`odb preflight / run / status`, the same on every OS; `bin/odb …` when odb is included);
  3. *Install odb*: a table of release downloads with SHA-256, for the platforms with a checksum (`downloads`), plus install lines for macOS/Linux and, when there's a Windows download, PowerShell;
  4. Docker (OC-183);
  5. settings (flag + `ODB_*`), exit codes and report import.

  Platform names come from `PlatformLabel` (`Linux x86-64`, `macOS (Apple silicon)`…).
- **Version check (AC2)**: the existing OC-050 range gate is kept rather than an exact match. A 2.5 export runs on odb `>=2.5.0 <3.0.0`, which is friendlier for an installed odb. The message now reads "This export needs odb >=2.5.0 <3.0.0; this is odb 2.0.1. Install a matching odb (README.md, Install odb)." An odb older than 2.5 prints its own (older) wording, because its code can't change. The README says "odb 2.5.0 or a later 2.x release".
- **API**: the `includeOdb` and `ExportOptions` descriptions are updated and `autoEmbed` is removed from `api/openapi.yaml`. `schema.d.ts` is regenerated.
- **Web**:
  - The checkbox is *Add odb for a machine without internet access*, unchecked by default. This reverts the earlier checked-by-default change.
  - The machine choice uses platform names, defaulting to one the server has odb for.
  - The checkbox is disabled, with the reason, when the server has none.
  - The success panel lists `config.json`, `mappings.json`, `README.md` (and `bin/odb`).
  - The history's runtime column says *Export folder*.
- **Tests**:
  - backend: `TestExport_OC182_AC1_Files`, `TestExport_OC182_AC2_NoBinary`, `TestExport_OC182_AC3_OfflineBinary`, the updated `TestExportsAPI_AC1_ZipContents` and `TestExportsAPI_OC160_AC2_OlderRequests` (AC5), and the README golden;
  - web: `ExportDialog.test.tsx` (`OC-182 AC4`, three cases).
- **AC1's three-OS run** is OC-184's end-to-end test (Linux, macOS and Windows runners). The README's Docker command is OC-183's.
