# Migration: Shop → docs

Exported 2026-10-01T10:00:00Z by user@example.com with Omni-Data-Bus 2.0.0.
Bundle sha256: `<sha256>`

`migrate.sh` runs this migration on any Linux or macOS machine that can reach the source database. It doesn't need the Omni-Data-Bus tool: it carries the mappings and runs the `odb` command-line runner, the same engine as the tool.

## What it migrates

| Mapping | Source table | Target |
|---|---|---|
| orders | `public.orders` | `orders/` |
| order_items | `public.order_items` | `order_items/` |

- **Source:** PostgreSQL `migrator@db.internal:5432/shop`
- **Target:** JSON files in `/data/out` (layout flat)
- **Credentials:** external (read from the environment or files at run time)
  - source password from `env:ODB_SOURCE_PASSWORD`

## Run it

Unzip the export and work inside its folder. Everything the script writes (checkpoint, errors,
report, a downloaded `odb`) stays in that folder, wherever you run it from.

```bash
unzip odb-export-shop-docs-20261001.zip
cd odb-export-shop-docs-20261001
export ODB_SOURCE_PASSWORD='…'
./migrate.sh run         # checks everything first, then migrates; progress is printed as JSON lines
```

The JSON files go to `/data/out`, as set in the project; set `ODB_TARGET_PATH` to write elsewhere.

- If the run stops (Ctrl+C, a reboot, a failure), `./migrate.sh resume` continues from the last
  checkpoint in `$ODB_STATE_DIR` (default `odb-state` in this folder).
- `./migrate.sh run` also continues from that checkpoint, so after a completed run it writes
  nothing, even if you deleted the migrated files. To migrate everything
  again, run `./migrate.sh run --restart`: it moves the old checkpoint, errors and report to
  `archive/<time>/` in the state directory and starts over.
- `./migrate.sh preflight` runs only the checks, writing nothing.
- `./migrate.sh status` summarizes the run; `./migrate.sh verify` counts the documents in the target.
- `./migrate.sh inspect` shows what the script will do; `./migrate.sh extract` writes
  `bundle.odb.json` for use with `odb run --bundle` directly.
- Pass `odb run` options after `--`, for example `./migrate.sh run -- --throttle 5000`.

### Settings

| Variable | Default | Meaning |
|---|---|---|
| `ODB_STATE_DIR` | `odb-state` in this folder | Checkpoint, `errors.jsonl` and `report.json` |
| `ODB_TARGET_PATH` | `/data/out` | Folder the JSON files are written to |
| `ODB_RUNTIME` | `auto` | `auto`, `binary`, `docker` or `embedded` |
| `ODB_BIN` | | An `odb` 2.0.0 binary to use |
| `ODB_DOCKER_ARGS` | | Extra `docker run` arguments, e.g. `--network host` |
| `ODB_CACHE_DIR` | `odb-cache` in this folder | Where a downloaded `odb` is kept |

With `auto`, the script uses `ODB_BIN`, then `odb` 2.0.0 on the `PATH`, then Docker (`ghcr.io/example/omni-data-bus@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc`), and finally downloads `odb` from `https://example.com/releases/v2.0.0`, checking it against the checksum written into the script.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Completed |
| 2 | Completed with errors, below the error threshold: see `errors.jsonl` |
| 3 | Failed: error threshold exceeded or a fatal error; see `report.json` |
| 4 | Not started: preflight or configuration problem (bad bundle, schema drift, wrong credentials, target not writable) |
| 5 | Cancelled after saving the checkpoint: run `./migrate.sh resume` |

After the run, import `report.json` from the state directory into the Omni-Data-Bus project
(Review → Export… → Import report) to see the production results next to your test runs.
