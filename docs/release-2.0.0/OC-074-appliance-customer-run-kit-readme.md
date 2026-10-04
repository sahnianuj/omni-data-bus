# OC-074 — Customer run kit and README for the all-in-one image

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 3 | OC-072, OC-073 | Implemented |

## User story
As a **customer** who received the Omni-Data-Bus all-in-one image, I want a short guide and a ready-made run file that take me from "I have a file" to "the app is open in my browser" in a few copy-paste steps, including how to stop, upgrade, back up and remove it, so that I can use the tool without help from the vendor. As the **solutions engineer**, I want the same steps for building and sharing the image in the main README.

## Scope
**In:**
- `deploy/appliance/README.md`: the customer guide, copied into the shared folder by `make appliance-save` (OC-073).
- `deploy/appliance/compose.yml` and `deploy/appliance/run.sh`: two ways to start it, both with safe defaults (named volume, memory, stop timeout, restart policy, Couchbase console on localhost only).
- Main `README.md`: a new section **All-in-one image (app + Couchbase CE)** with the build and share steps, and a link to the customer guide. Contents, Make targets and Documentation map updated.
- Every command in both READMEs is run by a test, so the docs can't drift from the image.

**Out:** translated guides, a PDF version, and video walkthroughs.

## Implementation details

### `deploy/appliance/run.sh` (bash ≥ 3.2, `shellcheck`-clean)
| Command | Does |
|---|---|
| `./run.sh load [file]` | Verifies the `.sha256` if it is next to the file, then `docker load -i` (default: the only `omni-data-bus-appliance-*.tar.gz` in the folder) |
| `./run.sh start [--port 8080] [--memory 4g] [--name odb]` | `docker run -d` with `-p <port>:8080`, `-p 127.0.0.1:<ODB_CONSOLE_PORT, default 8091>:8091` (skipped, with a note, when that port is taken, e.g. by a local Couchbase), `-v odb-data:/opt/couchbase/var`, `--memory`, `--stop-timeout 90`, `--restart unless-stopped`, and every `OC_*` variable of the calling shell (e.g. `OC_SIGNUP_ENABLED=false ./run.sh start`); waits until healthy, then prints the URL |
| `./run.sh stop` / `status` / `logs [-f]` | `docker stop --time 90`, health and versions, `docker logs` |
| `./run.sh upgrade <new file>` | `load`, stop and remove the old container, start the new image on the same volume |
| `./run.sh backup [dir]` / `restore <file> --yes` | Stops the container, archives the `odb-data` volume with `tar` from the appliance image itself (so it works offline; no `busybox` pull), starts it again. Restore refuses unless the container is stopped and `--yes` is given, and keeps file owners (`--numeric-owner`) |
| `./run.sh remove [--delete-data]` | Removes the container; the volume only with `--delete-data` and a typed confirmation |
| `./run.sh credentials` | `docker exec … odb-appliance show-credentials` (the Couchbase admin password, for support) |

Pre-checks before anything else: Docker present and running, Docker memory ≥ 3 GiB (`docker info`), port free. Each failure prints one line saying how to fix it.

### `deploy/appliance/compose.yml`
For customers who prefer Compose: one service with the same settings as `run.sh start`, image from `ODB_APPLIANCE_IMAGE` (default `omni-data-bus-appliance:<version>`), named volume `odb-data`. `docker compose up -d --wait` / `down`.

### Customer guide (`deploy/appliance/README.md`) — required sections, in this order
1. **What you received**: the files and what each is for.
2. **Requirements**: Docker Desktop or Docker Engine ≥ 24; 4 GB of memory for Docker (3 GB minimum); 10 GB free disk plus room for migrated data; x86-64 or ARM64 (pick the matching file). How to raise Docker Desktop's memory limit.
3. **Load the image**: verify the checksum, `docker load` (and `./run.sh load`).
4. **Start it**: `./run.sh start` (and the plain `docker run` equivalent, and the Compose equivalent). What "healthy" looks like; first start takes 1–3 minutes.
5. **First login**: open `http://localhost:8080`, create the first account (it becomes the admin), and turn off sign-up afterwards (`OC_SIGNUP_ENABLED=false`).
6. **Migrate into the bundled Couchbase or your own**: the **Use the bundled Couchbase** button; reaching the source database from inside the container: `localhost` is the container, so a table of what to enter for a database installed on the computer, in another Docker container (`host.docker.internal` + its *published* port) or on another server. `run.sh` adds `--add-host host.docker.internal:host-gateway` on Linux so the same name works everywhere (added 2026-10-01 after a user tried `localhost:5434`).
7. **Where data lives**: the `odb-data` volume; directory-target output and how to copy it out (`docker cp`).
8. **Stop, start, upgrade**: commands; data is kept; downgrades are not supported. Plus a **`run.sh` settings** table: `ODB_OUTPUT_DIR`/`--output`, `ODB_PORT`, `ODB_PUBLIC_URL`, `ODB_MEMORY`, `ODB_CONSOLE_PORT`, `ODB_NAME`/`ODB_VOLUME`, `OC_*`.
9. **Back up and restore**.
10. **Remove it**.
11. **Troubleshooting**: not healthy, out of memory, port in use, `dial tcp [::1]:… connection refused` (used `localhost` for the source), JSON files not in the output folder, `docker logs`, how to collect a support bundle (logs + `odb version` + `docker info`), and the Couchbase console on `http://localhost:8091` (with `./run.sh credentials`).
12. **Security notes**: binds on all interfaces only for 8080; put HTTPS in front before exposing it beyond the machine; secrets live in the volume.

### Main `README.md` — new section **All-in-one image (app + Couchbase CE)** (under *Building and deploying*)
Short, numbered steps for whoever builds and ships it:
1. Prerequisites: Docker with buildx (and QEMU for a cross-arch build).
2. Build: `make appliance` (or `ARCH=amd64` for a typical customer server).
3. Try it locally: `./deploy/appliance/run.sh start` → `http://localhost:8080`.
4. Package: `make appliance-save ARCH=amd64` (or `ARCH=all`) → what is in `dist/appliance/`.
5. Share: send the folder (file link, or the GitHub release assets), or `make appliance-push REGISTRY=…` and send the image name.
6. What the customer does: link to `deploy/appliance/README.md`.
7. When to use it versus the regular image (Capella / your own cluster, Helm): a three-row table.
8. The Couchbase CE licensing note from OC-072.

### Docs tests
- `deploy/appliance/test/docs_commands.sh` runs every ```` ```bash ```` block marked `<!-- docs-test -->` in the customer guide, as written, in a copy of `dist/appliance/` (its own container name, port and volume), then the guide's data round trip (sign up, backup, `remove --delete-data`, restore, upgrade). The main README's marked build commands are checked with `make -n` (the builds themselves take too long for a docs test and are covered by OC-073).
- `deploy/appliance/test/run_sh_test.sh` tests `run.sh`'s safety checks with a fake `docker` on the `PATH`.

## Acceptance criteria & unit tests

### AC1 — From file to browser in the documented steps
**Given** only the files from `dist/appliance/` and no local image, **when** the customer guide's marked commands run as written, **then** the image loads, the app starts and reports its status, and stops; and the UI flow sign-up → project → **Use the bundled Couchbase** → migrate works.

| Test | Location | Verifies |
|---|---|---|
| `docs_commands.sh` › customer guide | `deploy/appliance/test/docs_commands.sh` | `./run.sh load`, `start`, `status`, `stop`: passed 2026-10-01 |
| `appliance.spec.ts` › `OC-072 AC6` | `web/tests/e2e/appliance.spec.ts` | The browser part |

### AC2 — `run.sh` is safe
**Given** `run.sh`, **then** it passes `shellcheck` and its tests on macOS bash, bash 3.2 and bash 5.2. **Given** Docker missing or not running, too little memory, the image not loaded or the port taken, **then** it prints a one-line fix and exits non-zero without starting anything. **Given** `remove` without `--delete-data`, **then** the volume is kept; **given** `--delete-data` without typing DELETE (including with no terminal), **then** nothing is deleted; **given** `restore` while running or without `--yes`, **then** it refuses.

| Test | Location | Verifies |
|---|---|---|
| `run_sh_test.sh` (20 checks) | `deploy/appliance/test/run_sh_test.sh` | Fake docker; passed on macOS bash, `bash:3.2`, `bash:5.2` |
| `shellcheck` | all scripts in `deploy/appliance/` and `scripts/appliance.sh` | Clean |

Found by these tests and fixed: `remove --delete-data` with no terminal exited silently at `read` instead of saying "not confirmed".

### AC3 — Upgrade and backup round-trip
**Given** a running appliance with a user, **when** `./run.sh backup`, then `remove --delete-data`, `restore <backup> --yes` + `start`, then `upgrade <archive>` run, **then** after each step the user can sign in.

| Test | Location | Verifies |
|---|---|---|
| `docs_commands.sh` › data round trip | `deploy/appliance/test/docs_commands.sh` | Passed 2026-10-01 (backup 5.4 MB) |

### AC4 — Main README tells the builder what to do
**Given** the main README, **then** *All-in-one image (app + Couchbase CE)* exists under *Building and deploying* with numbered steps (prerequisites, build, try, package, share, the customer's part, test) and a when-to-use table and licensing note; Contents, Make targets and the Documentation map list it; its marked commands are real make targets.

| Test | Location | Verifies |
|---|---|---|
| `docs_commands.sh` › main README | `deploy/appliance/test/docs_commands.sh` | `make -n` of each marked command |
