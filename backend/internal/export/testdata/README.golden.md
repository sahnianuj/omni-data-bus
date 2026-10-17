# Migration: Shop → docs

Exported 2026-10-01T10:00:00Z by user@example.com with Omni-Data-Bus 2.0.0.
mappings.json sha256: `<sha256>`

This folder runs the migration on any Linux or macOS machine that can reach the source database, without the Omni-Data-Bus tool:

| File | What it is |
|---|---|
| `migrate.sh` | The runner. The same script for every export of odb 2.0.0: it reads the two files below and runs `odb`, the tool's engine |
| `config.json` | **Where:** the source and target connection details. Edit it for another environment |
| `mappings.json` | **What:** the tables, the documents they become, and how. Each mapping has an `outline` listing what fills each field |
| `README.md` | This file |

> **Warning:** `config.json` holds the database credentials. They are base64-encoded, which
> keeps them from being read at a glance but is not encryption: anyone with the file can
> connect to the source. Keep it like a password, and delete it after use.

## What it migrates

| Mapping | Source table | Target |
|---|---|---|
| orders | `public.orders` | `orders/` |
| order_items | `public.order_items` | `order_items/` |

- **Source:** PostgreSQL `migrator@db.internal:5432/shop`
- **Target:** JSON files in `/data/out` (layout flat)

`./migrate.sh inspect` prints each mapping's outline: every document field and what fills it.

## Run it

Unzip the export and work inside its folder. Everything the script writes (checkpoint, errors,
report, a downloaded `odb`) stays in that folder, wherever you run it from.

```bash
unzip odb-export-shop-docs-20261001.zip
cd odb-export-shop-docs-20261001
./migrate.sh run         # checks everything first, then migrates; progress is printed as JSON lines
```

The JSON files go to `/data/out` (`target.path` in `config.json`; a relative path is
relative to this folder). `ODB_TARGET_PATH` writes elsewhere for one run.

- If the run stops (Ctrl+C, a reboot, a failure), `./migrate.sh resume` continues from the last
  checkpoint in `$ODB_STATE_DIR` (default `odb-state` in this folder).
- `./migrate.sh run` also continues from that checkpoint, so after a completed run it writes
  nothing, even if you deleted the migrated files. To migrate everything
  again, run `./migrate.sh run --restart`: it moves the old checkpoint, errors and report to
  `archive/<time>/` in the state directory and starts over.
- `./migrate.sh preflight` runs only the checks, writing nothing.
- `./migrate.sh status` summarizes the run; `./migrate.sh verify` counts the documents in the target.
- Pass `odb run` options after `--`, for example `./migrate.sh run -- --throttle 5000`.
- Without the script: `odb run --mappings mappings.json --config config.json`.

## Run it elsewhere, or run other migrations

`migrate.sh` holds nothing about this migration, so the same script runs any export of odb
2.0.0:

- **Another environment, same mappings:** edit `config.json`, or keep one file per environment
  and pick it with `ODB_CONFIG=/path/to/prod.json ./migrate.sh run`. Use another
  `ODB_STATE_DIR` for each environment: a checkpoint follows the migration, not the connection.
- **Other mappings:** put another export's `mappings.json` (and its `config.json`) beside the
  script, or point `ODB_MAPPINGS` at it.
- In `config.json`, each section (`source`, `target`) lists the connection's `type`, its
  settings (host, port, database, user…) and its `password`. The `type` can't change.
  Passwords are kept as `"base64:…"`: to change one, replace the whole value, prefix included,
  with the new password in plain text, and the next run encodes it and saves the file again.
- `mappings.json` is checked against its sha256 when it runs: export it again from the tool to
  change it, rather than editing it.

### Settings

| Variable | Default | Meaning |
|---|---|---|
| `ODB_CONFIG` | `config.json` in this folder | Where: source and target connection details |
| `ODB_MAPPINGS` | `mappings.json` in this folder | What: the mappings |
| `ODB_STATE_DIR` | `odb-state` in this folder | Checkpoint, `errors.jsonl` and `report.json` |
| `ODB_TARGET_PATH` | `target.path` in `config.json` | Folder the JSON files are written to |
| `ODB_RUNTIME` | `auto` | `auto`, `binary` (never Docker) or `docker` |
| `ODB_BIN` | | An `odb` 2.0.0 binary to use |
| `ODB_DOCKER_ARGS` | | Extra `docker run` arguments, e.g. `--network host` |
| `ODB_CACHE_DIR` | `odb-cache` in this folder | Where a downloaded `odb` is kept |

The script uses `ODB_BIN`, then `odb` beside it, then `odb` 2.0.0 on the `PATH`, then
Docker (`ghcr.io/example/omni-data-bus@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc`, with this folder mounted at the same path), and finally downloads `odb`
from `https://example.com/releases/v2.0.0`, checking it against the checksum written in the script.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Completed |
| 2 | Completed with errors, below the error threshold: see `errors.jsonl` |
| 3 | Failed: error threshold exceeded or a fatal error; see `report.json` |
| 4 | Not started: preflight or configuration problem (`config.json` or `mappings.json`, schema drift, wrong credentials, target not writable) |
| 5 | Cancelled after saving the checkpoint: run `./migrate.sh resume` |

After the run, import `report.json` from the state directory into the Omni-Data-Bus project
(Review → Export… → Import report) to see the production results next to your test runs.
