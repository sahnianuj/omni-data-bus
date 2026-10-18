# Omni-Data-Bus — all-in-one image

Omni-Data-Bus moves PostgreSQL tables into Couchbase documents or JSON files. This package runs the whole tool on one machine with Docker: the app **and** its own Couchbase Server (Community Edition), which stores the app's data and can also be a migration target. You don't need a Couchbase cluster, a cloud account or anything else.

## 1. What you received

| File | What it is |
|---|---|
| `omni-data-bus-appliance-<version>-amd64.tar.gz` | The image for Intel/AMD machines (most servers, Intel Macs, Windows PCs) |
| `omni-data-bus-appliance-<version>-arm64.tar.gz` | The image for ARM machines (Apple-silicon Macs, AWS Graviton) |
| `*.tar.gz.sha256` | Checksums, to confirm the download is complete |
| `run.sh` | Starts, stops, upgrades and backs up the app (macOS and Linux) |
| `compose.yml` | The same setup for Docker Compose, if you prefer it (any OS, including Windows) |
| `README.md` | This guide |

You only need the archive that matches your machine's CPU; `run.sh` picks the right one.

## 2. Requirements

- **Docker**: Docker Desktop (macOS, Windows) or Docker Engine 24 or later (Linux).
- **Memory**: 4 GB for Docker (3 GB minimum). In Docker Desktop: *Settings → Resources → Memory*.
- **Disk**: 10 GB free, plus room for the data you migrate into the bundled Couchbase.
- **Network**: the machine must reach your PostgreSQL database. No internet access is needed.

## 3. Load the image

<!-- docs-test -->
```bash
./run.sh load
```

This checks the archive against its checksum, then loads it into Docker (about a minute). Without `run.sh`:

```bash
shasum -a 256 -c omni-data-bus-appliance-<version>-<arch>.tar.gz.sha256
docker load -i omni-data-bus-appliance-<version>-<arch>.tar.gz
```

## 4. Start it

<!-- docs-test -->
```bash
./run.sh start
```

The **first start takes 1–3 minutes**: it sets up the bundled Couchbase and generates its passwords. `run.sh` waits and prints `Ready: open http://localhost:8080`. Later starts take about 15 seconds.

- JSON files from *Directory of JSON files* targets go to **`~/omni-data-bus-output`** on this computer. Another folder: `./run.sh start --output /path/to/folder`.
- Another port: `./run.sh start --port 8081`. All settings: [`run.sh` settings](#runsh-settings).
- People open it from other computers: `ODB_PUBLIC_URL=http://<this-server>:8080 ./run.sh start`.

Without `run.sh` (all the same settings):

```bash
docker run -d --name omni-data-bus --restart unless-stopped \
  -p 8080:8080 -p 127.0.0.1:8091:8091 \
  -v odb-data:/opt/couchbase/var \
  -v "$HOME/omni-data-bus-output:/data" -e ODB_OUTPUT_HOST_DIR="$HOME/omni-data-bus-output" \
  --memory 4g --stop-timeout 90 \
  omni-data-bus-appliance:<version>
```

With Docker Compose: `ODB_IMAGE=omni-data-bus-appliance:<version> docker compose -f compose.yml up -d --wait`.

## 5. First sign-in

1. Open **http://localhost:8080**.
2. Choose **Create an account**. Each account has its own workspace with its own projects.
3. Once the people who need it have accounts, turn off open sign-up (data is kept):

   ```bash
   ./run.sh remove
   OC_SIGNUP_ENABLED=false ./run.sh start
   ```

   With `docker run`, add `-e OC_SIGNUP_ENABLED=false`; with Compose, add it under `environment:`.

## 6. Migrate

Create a project, then follow its steps (Source → Target → Tables → Design & map → Review & migrate).

- **Source**: your PostgreSQL. The app runs **inside a container**, so `localhost` there means the container itself, not this computer. Use:

  | Where your database runs | Host | Port |
  |---|---|---|
  | Installed on this computer | `host.docker.internal` | its port (usually `5432`) |
  | In another Docker container on this computer | `host.docker.internal` | the port it **publishes** on this computer: the left number in `docker ps`, e.g. `5434` for `0.0.0.0:5434->5432/tcp` |
  | On another server | its host name or IP address | its port |

  `./run.sh start` makes `host.docker.internal` work on Linux too; with a plain `docker run` on Linux, add `--add-host host.docker.internal:host-gateway`. Give the app a read-only database user where you can: it only reads the source.
- **Target**: click **Use the bundled Couchbase** to migrate into the Couchbase in this container (bucket `sandbox`), or enter your own Couchbase cluster, or pick *Directory of JSON files*. For a directory, **Choose folder…** shows one place, *Your computer: ~/omni-data-bus-output* (the app calls it `/data`); open it, create a folder for this migration, and use it. Each table gets its own sub-folder there, one JSON file per document.

Couchbase Community Edition doesn't support a *max expiry* on collections; leave that field empty when you create one in the bundled Couchbase.

## 7. Where your data lives

- **Projects, users, migrated Couchbase documents, generated passwords**: the Docker volume **`odb-data`**. Removing or upgrading the container keeps it.
- **JSON files from directory targets**: the output folder on this computer, `~/omni-data-bus-output` unless you chose another. Open it like any folder.

To use another output folder, start with `./run.sh start --output <folder>`. The folder is fixed when the container is created, so `run.sh` recreates the container for you (all data is kept); point existing directory targets at the new location (`/data/…`) afterwards. `./run.sh status` shows the current folder.

On **Linux**, the app writes as user id 1000. `run.sh` checks the folder and, if needed, grants that user access with `setfacl`, or tells you the one command to run. Docker Desktop (macOS, Windows) needs nothing.

If you started the app without an output folder (a plain `docker run` without `-v …:/data`), **Choose folder…** warns that its folders are inside the app's container. Files written there are kept in the volume; copy them out with:

```bash
docker cp omni-data-bus:/opt/couchbase/var/odb/home/<folder> ./<folder>
```

## 8. Stop, start and upgrade

<!-- docs-test -->
```bash
./run.sh status
./run.sh stop
```

`stop` lets running migrations save their progress first (up to 90 seconds); `./run.sh start` resumes them. To upgrade, put the new archive next to `run.sh` and run:

```bash
./run.sh upgrade omni-data-bus-appliance-<new-version>-<arch>.tar.gz
```

Your data is kept. Going back to an older version isn't supported; take a backup before upgrading.

### `run.sh` settings

Set these before `./run.sh start` (for example `ODB_PORT=8081 ./run.sh start`). Port, memory, output folder and `OC_*` settings take effect when the container is created; `run.sh` recreates it for a new output folder, and for the others run `./run.sh remove` first (data is kept).

| Setting | Default | What it does |
|---|---|---|
| `ODB_OUTPUT_DIR` (or `--output`) | `~/omni-data-bus-output` | Folder on this computer for JSON files from directory targets (`/data` in the app) |
| `ODB_PORT` (or `--port`) | `8080` | Port the app is opened on |
| `ODB_PUBLIC_URL` | `http://localhost:<port>` | The address people open, when it isn't `localhost` (another computer, a proxy) |
| `ODB_MEMORY` (or `--memory`) | `4g` | Memory for the container (3 GB minimum) |
| `ODB_CONSOLE_PORT` | `8091` | Couchbase web console, this computer only; skipped if the port is taken |
| `ODB_NAME`, `ODB_VOLUME` | `omni-data-bus`, `odb-data` | Container and data-volume names |
| `OC_*` | | Any app setting, passed into the container, e.g. `OC_SIGNUP_ENABLED=false` |

## 9. Back up and restore

```bash
./run.sh backup             # stops the app, writes odb-backup-<time>.tar.gz here, starts it again
./run.sh stop
./run.sh restore odb-backup-<time>.tar.gz --yes    # replaces ALL data with the backup
./run.sh start
```

## 10. Remove it

```bash
./run.sh remove                  # removes the container, keeps the data
./run.sh remove --delete-data    # also deletes all data (asks you to type DELETE)
docker image rm omni-data-bus-appliance:<version>
```

## 11. Troubleshooting

| Symptom | What to do |
|---|---|
| `the appliance needs at least 3 GiB of memory` | Give Docker more memory (section 2), then `./run.sh start` |
| `port 8080 is in use` | `./run.sh start --port 8081` |
| Not ready after 5 minutes | `./run.sh logs` and look at the last lines starting with `appliance:` |
| JSON files don't appear in the output folder | `./run.sh status` shows the folder in use; in the app, the target's folder must start with `/data`. On Linux, check the log for `isn't writable by the app` and run the command it gives |
| Source test fails with `dial tcp [::1]:5432` (or `127.0.0.1`): `connection refused` | You entered `localhost`: inside the container that's the container itself. Use `host.docker.internal` and the published port (section 6) |
| You can't sign in from another computer | Start with `ODB_PUBLIC_URL=http://<this-server>:8080`; check the firewall allows port 8080 |
| Couchbase questions from support | The Couchbase web console is at http://localhost:8091 on this machine (if 8091 was taken, start with `ODB_CONSOLE_PORT=8095`); `./run.sh credentials` shows its password |

**Collect a support bundle** (send all three files):

```bash
./run.sh logs > odb-logs.txt 2>&1
./run.sh status > odb-status.txt
docker info > docker-info.txt
```

## 12. Security notes

- Only port **8080** (the app) is open to other machines. The Couchbase console (8091) is bound to this machine only.
- The app speaks plain HTTP. Before people use it over a network you don't trust, put an HTTPS proxy in front (for example Caddy or nginx) and start with `ODB_PUBLIC_URL=https://<your-domain>`.
- The bundled Couchbase's password and the key that encrypts stored database passwords are generated on the first start and kept in the `odb-data` volume, readable only inside the container. Protect backups like the data itself.

---

**Licences.** This image combines two products, so it isn't licensed under Apache-2.0 as a whole (its OCI label is `Apache-2.0 AND LicenseRef-Couchbase-CE`):
- **Omni-Data-Bus** is licensed under the Apache License 2.0, Copyright 2026 Couchbase, Inc. Its `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.md` are in `/usr/share/doc/omni-data-bus/`.
- **Couchbase Server Community Edition**, unmodified, is under Couchbase's own license (`docker run --rm --entrypoint cat omni-data-bus-appliance:<version> /opt/couchbase/LICENSE.txt`).
