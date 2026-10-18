# OC-183 — Run the export with Docker in one command

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 3 | OC-181 | Implemented |

## User story
As an **operator on a machine with Docker but no odb binary for it**, I want one copy-paste command that runs the export with the odb image, so that I don't need the `migrate.sh` Docker wrapper.

## Background (2026-10-17)
`migrate.sh`'s Docker mode does three things:
- it mounts the export folder and any state or directory-target folder outside it, at the same paths;
- it runs as the caller's UID so files aren't owned by root;
- it asks the image for the directory target's path first.

Without the script, the README has to give a command that does the same. The image's entrypoint is already `odb`, so the OC-181 commands work as `docker run … IMAGE preflight`.

## Scope
**In:**
- **The command, in the README with the image pinned to the export's version:**
  ```
  docker run --rm -it --user "$(id -u):$(id -g)" -v "$PWD:$PWD" -w "$PWD" ghcr.io/…/omni-data-bus:2.5.0 preflight
  ```
  Replace `preflight` with `run`, `resume`, `status` or `verify`.
- **Inside the container, odb uses the working directory** for `config.json`, `mappings.json` and `odb-state` (OC-180). Nothing else is mounted for the common case.
- **A directory target outside the folder:**
  - if `config.json`'s target path isn't under the working directory and isn't mounted, preflight fails with exit 4;
  - the message gives the extra `-v` to add: "the directory target /data/out isn't visible in this container: add -v /data/out:/data/out".
- **Networking**: when the source or target is `localhost` and odb detects it's in a container (`/.dockerenv`), preflight's connection error suggests `--network host` (Linux) or `host.docker.internal`.

**Out:** a Docker Compose file; Podman (it works with the same command but isn't tested).

## Acceptance criteria & unit tests

### AC1 — The README command works
**Given** an export without a binary and the release image, **when** the README's command runs with `preflight` and then `run`, **then** the migration completes, and `odb-state` is owned by the caller.

| Test | Location | Verifies |
|---|---|---|
| `TestDocker_OC183_AC1_ReadmeCommand` (integration, needs Docker) | `backend/internal/export/docker_integration_test.go` | |

### AC2 — An unmounted directory target is explained
**Given** a directory target at `/data/out` and no mount for it, **then** preflight exits with 4 and the message gives the `-v` to add.

| Test | Location | Verifies |
|---|---|---|
| `TestRun_OC183_AC2_TargetNotMounted` | `backend/cmd/odb/run_test.go` | |

### AC3 — localhost inside a container
**Given** `/.dockerenv` and a source at `localhost:5432` that refuses connections, **then** the error suggests `--network host` or `host.docker.internal`.

| Test | Location | Verifies |
|---|---|---|
| `TestPreflight_OC183_AC3_LocalhostHint` | `backend/internal/headless/preflight_test.go` | |

## Implementation notes (2026-10-17)
- **The README's Docker section** (written in OC-182) is pinned to the server's image (`OC_EXPORT_IMAGE`). It gives:
  - `docker run --rm -it --user "$(id -u):$(id -g)" -v "$PWD:$PWD" -w "$PWD" <image> preflight`;
  - the PowerShell form (`-v "${PWD}:/work" -w /work`);
  - the `--network host` / `host.docker.internal` note.

  Inside the container, odb finds `config.json`, `mappings.json` and `odb-state` in the working directory (OC-180), so nothing else is mounted for the common case.
- **`backend/cmd/odb/container.go`**:
  - `inContainer()` checks for `/.dockerenv` or `/run/.containerenv` (Podman).
  - `mounted()` finds the longest mount point over a path in `/proc/self/mountinfo`. A path whose only mount is `/` is in the container's own filesystem.
  - `unmountedTarget()` stops `odb run`, `preflight` and `verify` with exit 4 when a directory target (from `config.json`, or `--target-path` / `ODB_TARGET_PATH`) isn't on a mounted folder: "the directory target /data/out isn't visible outside this container, so its files would be lost: add -v /data/out:/data/out to docker run, or use a target.path inside the export folder."
  - `localhostHint()` adds a `hint:` line after a failed preflight or run when, in a container, `config.json` points the source or target at `localhost` or `127.0.0.1`.
- **AC3's test** is in `cmd/odb/run_test.go` (`TestPreflight_OC183_AC3_LocalhostHint`), not `headless`: the hint is about how odb was started, not about the run.
- **Tests**:
  - `TestRun_OC183_AC2_TargetNotMounted`, `TestPreflight_OC183_AC3_LocalhostHint` and `TestMounted_OC183`, with a faked container and mountinfo;
  - `TestDocker_OC183_AC1_ReadmeCommand` (`cmd/odb/docker_integration_test.go`, `-tags integration`, needs `ODB_IT_IMAGE` and the dev stack). It runs the README's command exactly (preflight, then run), and checks all 10,000 orders and that `odb-state`, `report.json` and `out/orders` belong to the caller. **It passed locally** against `make image` of this tree (`omni-data-bus:oc183-it`) and the dev stack's PostgreSQL, through `host.docker.internal` on Docker Desktop.
