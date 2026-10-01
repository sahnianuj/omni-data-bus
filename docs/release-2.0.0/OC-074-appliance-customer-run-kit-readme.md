# OC-074 — Customer run kit and README for the all-in-one image

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 3 | OC-072, OC-073 | Not started |

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
| `./run.sh start [--port 8080] [--memory 4g] [--name odb]` | `docker run -d` with `-p <port>:8080`, `-p 127.0.0.1:8091:8091`, `-v odb-data:/opt/couchbase/var`, `--memory`, `--stop-timeout 90`, `--restart unless-stopped`; waits until healthy, then prints the URL |
| `./run.sh stop` / `status` / `logs [-f]` | `docker stop --time 90`, health and versions, `docker logs` |
| `./run.sh upgrade <new file>` | `load`, stop and remove the old container, start the new image on the same volume |
| `./run.sh backup [dir]` / `restore <file>` | Stops the container, archives the `odb-data` volume with a throwaway `busybox` container (`tar czf`), starts it again. Restore refuses unless the container is stopped and `--yes` is given |
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
6. **Migrate into the bundled Couchbase or your own**: the **Use the bundled Couchbase** button; how to reach a PostgreSQL running on the same machine (`host.docker.internal`, or `--add-host` on Linux).
7. **Where data lives**: the `odb-data` volume; directory-target output and how to copy it out (`docker cp`).
8. **Stop, start, upgrade**: commands; data is kept; downgrades are not supported.
9. **Back up and restore**.
10. **Remove it**.
11. **Troubleshooting**: not healthy, out of memory, port in use, `docker logs`, how to collect a support bundle (logs + `odb version` + `docker info`), and the Couchbase console on `http://localhost:8091` (with `./run.sh credentials`).
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
- `docs_commands.bats` pulls every fenced `bash` block from both READMEs that is marked `<!-- docs-test -->` and runs it in order against a freshly built image, in a scratch Docker context. Blocks that need human input (opening a browser) are marked `<!-- docs-test: skip -->` and are covered by the E2E test instead.
- `markdown-link-check` on both files.

## Acceptance criteria & unit tests

### AC1 — From file to browser in the documented steps
**Given** only the files from `dist/appliance/` on a clean Docker host, **when** the customer guide's load and start steps are run as written, **then** the app answers on `http://localhost:8080` and the E2E sign-up → create project → **Use the bundled Couchbase** → migrate flow passes.

| Test | Location | Verifies |
|---|---|---|
| `docs_commands.bats` › `AC1: customer guide load + start` | `deploy/appliance/test/docs_commands.bats` | Commands copied from the README, not re-typed |
| `appliance.spec.ts` › `AC1: first login and bundled migration` | `web/tests/e2e/appliance.spec.ts` | |

### AC2 — `run.sh` is safe
**Given** `run.sh`, **then** it passes `shellcheck` and its bats suite on bash 3.2 and 5. **Given** Docker is not running, too little memory, or the port is taken, **then** it prints a one-line fix and exits non-zero without starting anything. **Given** `remove` without `--delete-data`, **then** the volume is kept; **given** `restore` while running, **then** it refuses.

| Test | Location | Verifies |
|---|---|---|
| `run_sh.bats` › `AC2: prechecks`, `AC2: remove keeps data`, `AC2: restore refuses while running` | `deploy/appliance/test/run_sh.bats` | Docker stubbed |
| `shellcheck deploy/appliance/run.sh` | CI `lint` | |

### AC3 — Upgrade and backup round-trip
**Given** a running appliance with data, **when** `./run.sh backup`, `./run.sh upgrade <newer file>`, then `./run.sh remove --delete-data` and `./run.sh restore <backup>` + `start` run, **then** after each step the projects, users and migrated documents are present.

| Test | Location | Verifies |
|---|---|---|
| `run_sh.bats` › `AC3: backup, upgrade, restore` *(docker)* | `deploy/appliance/test/run_sh.bats` | CI job `appliance` |

### AC4 — Main README tells the builder what to do
**Given** the main README, **then** the new section exists under *Building and deploying* with the numbered steps above, Contents, Make targets and the Documentation map list it, and its build/save commands are run by `docs_commands.bats`.

| Test | Location | Verifies |
|---|---|---|
| `docs_commands.bats` › `AC4: main README build + save` | `deploy/appliance/test/docs_commands.bats` | |
| `markdown-link-check README.md deploy/appliance/README.md` | CI `lint` | |
