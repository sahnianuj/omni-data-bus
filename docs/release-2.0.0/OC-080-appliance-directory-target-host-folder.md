# OC-080 — All-in-one image: write directory targets to a folder on the user's computer

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 5 | OC-038, OC-072, OC-074 | Implemented |

## User story
As a **data engineer** running the all-in-one image, I want a *Directory of JSON files* target to write into a folder on my own computer, and the folder picker to show me plainly which folders those are, so that I can open the migrated files directly, instead of them silently landing inside the container where I can't see them.

## Background (2026-10-01)
In the all-in-one image the server runs inside the container. The Target step's **Choose folder** browses the *server's* folders, which there means the container's: its only area was the app user's home, `/opt/couchbase/var/odb/home`, inside the Docker volume. The dialog looks like a normal folder browser, so users assume it is their machine; the files only come out with `docker cp`. Nothing was shared between the computer and the container except the volume.

## Scope
**In:**
- **Share a folder.** `run.sh start` bind-mounts a folder of the computer at `/data` in the container: `--output <dir>` or `ODB_OUTPUT_DIR`, default `~/omni-data-bus-output` (created if missing). `compose.yml` does the same with `ODB_OUTPUT_DIR` (default `./output`).
- **Tell the server what it is.** `run.sh` passes the folder's path on the computer (`ODB_OUTPUT_HOST_DIR`). When `/data` really is a mount, the entrypoint sets `OC_DIRECTORY_SHARED=/data=<path on the computer>`; otherwise `OC_DIRECTORY_CONTAINER_ONLY=true`.
- **Picker shows the truth.** The picker's areas carry a label and, for a shared one, its path on the computer: *"Your computer: /Users/ana/omni-data-bus-output"*. When a folder is shared, the container-only home area isn't offered. When none is, the home area is offered with a warning: *"Inside the app's container: not visible on your computer. To write to your computer, start the app with ./run.sh start --output <folder>."*
- **Paths on the computer everywhere it matters.** The picker's current folder, the folder field's hint, the saved target's summary and the Test connection result show the computer path next to the container path: *"/data/orders · on your computer: /Users/ana/omni-data-bus-output/orders"*.
- **Changing the folder.** A container's mounts are fixed when it's created, so `run.sh start --output <other>` on an existing container recreates it (the data volume is kept) and says so.
- **Linux permissions.** The app writes as uid 1000. On Linux, `run.sh` checks that uid 1000 can write the folder; if not, it grants it with an ACL (`setfacl -m u:1000:rwx`, plus the default ACL for new files) when `setfacl` is available, else stops with the exact `chown`/`setfacl` command to run. Docker Desktop (macOS, Windows) needs nothing. If the entrypoint's write check fails anyway, it logs the same fix and the app still starts; the folder is still offered (as *Your computer*), and Test connection's write check then fails with the permission error, which says more than a folder silently missing from the list.
- **Docs:** the customer guide's *Where your data lives* section, `run.sh help`, `compose.yml`.

**Out:** the regular image, Helm and `make dev` (there the server's folders *are* the machine's; unchanged); more than one shared folder; picking a folder with the browser's own file dialog (a browser can't hand the server a path).

## Implementation details

### `run.sh`
| Change | Detail |
|---|---|
| `start [--output DIR]` / `ODB_OUTPUT_DIR` | Default `$HOME/omni-data-bus-output`. Resolved to an absolute path, created if missing. Adds `-v "$DIR:/data"` and `-e ODB_OUTPUT_HOST_DIR="$DIR"` |
| Existing container | `docker inspect` its `/data` mount; if it differs from the requested folder, stop, remove and recreate it (volume kept), printing *"Output folder changed to …; recreated the container (data kept)"* |
| Linux | `uid 1000 can write DIR?` checked with a throwaway container (`docker run --rm --user 1000 -v DIR:/d … touch /d/.odb-write-test`); fix as in Scope |
| `status` | Also prints *"JSON output folder: DIR"* |

### Entrypoint (`odb-appliance.sh`)
- `/data` is a mount when `/proc/self/mountinfo` lists it. Then a write probe as `couchbase` (create and delete `/data/.odb-write-test`).
- Mounted: `OC_DIRECTORY_SHARED=/data=${ODB_OUTPUT_HOST_DIR:-/data}`, and log *"JSON output folder on your computer: …"*; if the write probe fails, also the warning with the fix. Not mounted: `OC_DIRECTORY_CONTAINER_ONLY=true` (the picker offers the container home with its warning).

### Server
- `OC_DIRECTORY_SHARED` (`config.DirectoryConfig.Shared`, `container=host` pairs separated by `,`). The directory connector gets the shared areas; when any exists, its allowed roots are the shared ones only (the container home isn't one).
- `FolderEntry` and `FolderListing` gain `hostPath` (the path on the computer, empty when not shared) and areas gain `label` and `shared`. `GET /target-kinds/directory/folders` without a path lists areas with them; with a path, `hostPath` of the folder and of each child.
- `GET /target-kinds` adds `sharedFolder: {path, hostPath}` to the directory kind when one is shared (`target.SharedFolderer`; used by the form hint and summary). `api/openapi.yaml` (`FolderEntry`, `FolderWhere`, `SharedFolder`) and the TS types follow.
- As built: `directory.New(roots, directory.Options{Shared, ContainerOnly})` and `app.Registries(nil, opts)`; existing callers are unchanged.
- The directory Test connection's *path* step detail adds *"on your computer: …"* when the path is under a shared area.

### Web
- `FolderPicker`: areas as rows with an icon (computer vs. container), label and path; the description says where files go; the container-only warning when nothing is shared; the current folder shows its computer path under the breadcrumb.
- `DirectoryForm`: the path field's hint shows the computer path of the typed or picked path; the repository summary does too.

## Results (2026-10-01)
- `smoke.sh`: 34 checks pass, 7 of them new for OC-080 (log line, `sharedFolder`, picker area, folder made through the picker appears on the host, Test connection shows the computer path and leaves nothing behind, read-only share reported).
- `appliance.spec.ts` › `OC-080 AC1`: migrated `shop.customer` to `/data/<folder>`; 2,000 JSON files (one per row) on the host, plus the run manifest `_odb/run.json`, owned by the Mac user.
- `run_sh_test.sh`: 33 checks (8 new) pass on macOS bash, bash 3.2 and bash 5.2.
- Backend and web suites pass (web 467 tests).
- Not exercised on a real Linux host: the `setfacl` path (tested with a fake Linux in `run_sh_test.sh`).

## Acceptance criteria & unit tests

### AC1 — Files land on the computer
**Given** `./run.sh start` (default output folder), **when** a directory target is set to `/data/orders` and a migration runs, **then** the JSON files are in `~/omni-data-bus-output/orders` on the computer, one per document.

| Test | Location | Verifies |
|---|---|---|
| `smoke.sh` › OC-080 | `deploy/appliance/test/smoke.sh` | Mounted temp folder; a folder made through the picker API appears on the host |
| `appliance.spec.ts` › `OC-080 AC1: directory target writes to the computer` | `web/tests/e2e/appliance.spec.ts` | Full migration; file count on the host = rows |

### AC2 — The picker says what is shared
**Given** a shared `/data`, **then** the picker's only area is *"Your computer: <path>"*, and folders show their computer path. **Given** nothing shared, **then** the container home is offered with the warning and the `--output` hint.

| Test | Location | Verifies |
|---|---|---|
| `TestFolders_OC080_AC2_SharedAreaOnly` | `backend/internal/connectors/target/directory/shared_test.go` | Labels, `hostPath`, home hidden, Test connection detail |
| `TestFolders_OC080_AC2_ContainerOnlyWarning`, `TestHostPath_OC080_WindowsComputer` | same | |
| `TestDirectoryShared_OC080` | `backend/internal/app/wiring_test.go` | |
| `TestLoad_DirectoryShared_OC080` | `backend/internal/config/config_test.go` | Parsing `container=host` pairs |
| `FolderPicker.test.tsx` › `OC-080 AC2` (2 tests) | `web/src/features/targets/FolderPicker.test.tsx` | |
| `FolderPicker.test.tsx` › `hostPathOf` | `web/src/features/targets/FolderPicker.test.tsx` | Incl. a Windows computer |

### AC3 — `run.sh` shares, recreates and checks permissions
**Given** `--output DIR`, **then** DIR is created and mounted at `/data` with its host path passed in. **Given** a running container with another output folder, **then** it is recreated with the new one and the volume is kept. **Given** Linux and a folder uid 1000 can't write, **then** `run.sh` grants it with `setfacl` or stops with the command to run.

| Test | Location | Verifies |
|---|---|---|
| `run_sh_test.sh` › output folder (8 checks) | `deploy/appliance/test/run_sh_test.sh` | Fake docker: mount, env, recreate, Linux check |

### AC4 — Unwritable share is reported, not fatal
**Given** `/data` mounted read-only, **then** the entrypoint logs the warning with the fix and the app starts.

| Test | Location | Verifies |
|---|---|---|
| `smoke.sh` › OC-080 read-only mount | `deploy/appliance/test/smoke.sh` | |

### AC5 — Nothing changes elsewhere
**Given** the regular image or `make dev` (no `OC_DIRECTORY_SHARED`), **then** the picker lists the same areas as before, without labels changing what is allowed.

| Test | Location | Verifies |
|---|---|---|
| `TestFolders_OC080_AC5_RegularServerUnchanged`; existing `TestFolderPicker_OC038` and `FolderPicker.test.tsx` › `OC-038` | `…/directory/shared_test.go`, `backend/internal/connections/handlers_test.go`, `web/src/features/targets/FolderPicker.test.tsx` | Unchanged and passing |
