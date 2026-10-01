# OC-038 — Directory target repository (per project): configure, test, save

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E8 Directory Target | P0 | 5 | OC-036 | Implemented |

## User story
As a **data engineer**, I want to set my project's target to a directory, test that the tool can write there, and pick or create sub-folders for each mapping, so that migrated documents are stored as individual JSON files.

## Scope
**In:**
- Directory connection config: path, layout, compression, pretty-printing, file mode, durability, manifest and max document size (see [architecture §6.2](00-architecture.md#62-directory-target-connection-config)).
- **Test connection**: path checks, a write/read/delete probe, free space and inodes.
- Location browser: list and create sub-folders under the root (the directory equivalent of OC-013/OC-014).
- Server-side path allowlist, so users can't write anywhere on the worker's file system.
- Helm and compose: a volume for directory targets, mounted into worker pods.

**Out:** object storage (S3/GCS/Azure Blob), post-2.0. Network shares work only if they are mounted into the worker as a normal path.

## Implementation details

### Config & validation (`internal/connectors/target/directory`)
| Field | Default | Rules |
|---|---|---|
| `path` | — | Absolute, cleaned (`filepath.Clean`), inside one of `OC_DIRECTORY_ROOTS` after resolving symlinks (`filepath.EvalSymlinks`); otherwise `DIRECTORY_OUTSIDE_ALLOWED_ROOTS` |
| `layout` | `flat` | `flat` \| `sharded` \| `key-path` (OC-039) |
| `pretty` | `false` | Two-space indent when true |
| `compression` | `none` | `none` \| `gzip` (file extension becomes `.json.gz`) |
| `fileMode` | `0640` | Octal, `0600`–`0666`; directories get `+x` for each read bit |
| `durability` | `none` (was `file` until OC-079) | `none` (no fsync) \| `file` (fsync the file) \| `file+dir` (also fsync the parent directory after rename) |
| `manifest` | `true` | Write `_odb/manifest/*.jsonl` (OC-039) |
| `maxDocBytes` | 20 MiB | 1 KiB – 1 GiB |

- `OC_DIRECTORY_ROOTS` (comma-separated) is empty by default, which **disables** the directory kind on the server with the message "Directory targets are disabled; ask your administrator to set OC_DIRECTORY_ROOTS". The headless runner (OC-051) has no allowlist: it writes where the operator's bundle or flags say.
- A directory connection has no secret. The stored document has `config` only.

### Test connection
Each check reports pass, warn or fail with a message:
1. The path exists, or `createIfMissing` is set and the parent exists.
2. It is a directory, not a file.
3. A write/read/delete probe of `.odb-probe-<uuidv7>` succeeds.
4. `statfs` free bytes and inodes: warn below 5 GiB or 1M inodes.
5. File system type (`ext4`, `xfs`, `nfs`, `apfs`, …): warn on network file systems that `durability: file+dir` may be slow.
6. Case sensitivity (create `A` then stat `a`): reported so that OC-039 can warn about case-folding collisions.

### Location browser
- `ListLocations(parent)` lists immediate sub-folders (not files), skipping names starting with `.` or `_odb`, with approximate file counts from `ReadDir` capped at 10,000 ("10,000+").
- `CreateLocation({folder})`: folder names are validated (no `..`, no separators except `/` for nesting, max 3 levels, each segment ≤ 64 bytes, `[A-Za-z0-9._-]`).
- Default folder for a new mapping: the mapping's type value (for example `orders`).

### Deployment
- Helm: `directoryTarget.enabled`, `directoryTarget.persistence.{existingClaim|storageClass,size,accessMode}`, mounted at `/data/odb-out` in worker pods (and API pods, for browsing and verification). `OC_DIRECTORY_ROOTS=/data/odb-out`. With `mode: split` and more than one worker, the claim must be `ReadWriteMany`; the chart fails to render with a clear message otherwise.
- Compose: a named volume `odb-out` mounted at `/data/odb-out`.

## Acceptance criteria & unit tests

### AC1 — Allowlist enforcement
**Given** `OC_DIRECTORY_ROOTS=/data/odb-out`, **then** `/data/odb-out/shop` is accepted, while `/etc`, `/data/odb-out/../etc` and a symlink inside the root pointing to `/etc` are rejected with `DIRECTORY_OUTSIDE_ALLOWED_ROOTS`. **Given** an empty allowlist, **then** the kind is reported as disabled.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC1_AllowedRoots` | `internal/connectors/target/directory/config_test.go` | Table-driven, includes symlink escape |
| `TestRegistry_AC1_DisabledWithoutRoots` | same | |

### AC2 — Test connection checks
**Given** a writable temp dir, **then** all checks pass and no probe file remains. **Given** a read-only dir, **then** check 3 fails with `DIRECTORY_NOT_WRITABLE`. **Given** a file instead of a dir, **then** check 2 fails.

| Test | Location | Verifies |
|---|---|---|
| `TestTest_AC2_Writable` | `internal/connectors/target/directory/test_test.go` | |
| `TestTest_AC2_ReadOnly` | same | Skipped when running as root |
| `TestTest_AC2_NotADirectory` | same | |
| `TestTest_AC2_LowSpaceWarns` | same | Fake statfs |

### AC3 — Folder browser & create
**Given** a root with `orders/` and `customers/` and a file `x.json`, **then** only the two folders are listed. **When** the user creates `archive/2026`, **then** it exists. **Given** `../x`, **then** `LOCATION_INVALID`.

| Test | Location | Verifies |
|---|---|---|
| `TestListLocations_AC3_FoldersOnly` | `internal/connectors/target/directory/browser_test.go` | |
| `TestCreateLocation_AC3_ValidatesNames` | same | |
| `DirectoryBrowser.test.tsx` › `AC3: lists and creates folders` | `src/features/targets/DirectoryBrowser.test.tsx` | |

### AC4 — No secret stored
**Given** a saved directory connection, **then** the stored document has no `secret` field, and the response has no `passwordSet` flag.

| Test | Location | Verifies |
|---|---|---|
| `TestPutTarget_AC4_DirectoryNoSecret` | `internal/httpapi/connections_test.go` | |

### AC5 — Helm guards
**Given** `mode: split`, 2 worker replicas and a `ReadWriteOnce` claim, **then** `helm template` fails with the RWX message.

| Test | Location | Verifies |
|---|---|---|
| `helm unittest` › `AC5: directory target requires RWX in split mode` | `deploy/helm/omni-data-bus/tests/directory_test.yaml` | |

## Implementation notes (as built)
- **No `OC_DIRECTORY_ROOTS` (product decision, 2026-09-29).** Nothing needs configuring: the directory kind is on out of the box. Users pick the output folder in the Target step with a **folder picker** (Choose folder… → browse → New folder → Use this folder); typing a path still works. The server allows a folder inside its **default areas**: the home folder of the account running odb, and `/data` when it exists (the image creates `/data/odb-out`, owned by the server user, where Helm and compose mount the output volume). Paths are resolved through symlinks first. A path that goes through a hidden folder (`~/.ssh`, `.config`…), is outside the areas, or reaches outside them through a symlink or `..` is `DIRECTORY_OUTSIDE_ALLOWED_ROOTS`. The kind is disabled only on a server with neither area.
- **Picker API** (editors only): `GET /target-kinds/directory/folders?path=` lists the areas (no path) or a folder's visible sub-folders, with `parent` for Up. `POST` with `{parent, name}` creates a folder; it's audited as `folder.create`. Folder names are letters, digits, spaces, `.`, `_`, `-`, not starting with a dot.
- **Test connection** checks are `enabled`, `path` (allowed), `exists` (created when `createIfMissing` is on), `isdir`, `probe` (write, read and delete `.odb-probe-<uuid>`), `space` (warns below 5 GiB or 1M inodes), `fstype` (warns on a network file system with `durability: file+dir`) and `case` (reports case sensitivity for OC-039/OC-040). statfs is implemented for darwin and linux; other platforms report free space as unknown.
- **The browser** (`/connections/{id}/locations`) lists sub-folders without hidden or `_odb` ones. File counts are capped at 10,000 (`docCountMore`). Folders nest up to 3 levels. Creating and stat'ing a folder re-checks, through symlinks, that it stays inside the target folder. Directory targets also got a basic `Preflight`: the folder exists or is created, and a warning if it isn't empty. OC-040 extends it.
- **AC4:** a directory connection's API representation has no `secrets` block (`Connection.secrets` is now optional in the API). `TestPutTarget_AC4_DirectoryNoSecret` is in `internal/connections`, because `httpapi` can't import that package.
- **AC5 (Helm):** `directoryTarget.{enabled,mountPath,persistence}` adds a PVC (kept on uninstall) mounted at `/data/odb-out` in every pod. A split deployment with more than one worker (or autoscaling) needs `ReadWriteMany` or an `existingClaim`; rendering fails otherwise. Compose (prod, e2e) mounts an `odb-out` volume there.
