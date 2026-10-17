# Running migrations outside the tool

Export a project and run the migration on another machine — a production server, an air-gapped site — without Omni-Data-Bus or access to it. An export is a folder of three files: `migrate.sh` runs `odb run`, the same engine as the tool (the documents are identical), with `config.json` saying **where** (the connections) and `mappings.json` saying **what** (the mappings).

## Exporting

Review → **Export…** → **New export**.

1. **Project** — the export uses the project exactly as configured: its source, target, saved credentials and every ready mapping. They are shown read-only; to change one, follow its *Change in …* link, then export again.
2. **Options** — whether to include `odb` for a machine without internet access (and which platform), and the batch size. Every export is the same folder otherwise.

**Export** downloads the zip at once: there's no passphrase and no password to confirm. Exporting needs the editor role. Every export is in the **Export history** with who made it, its bundle hash, and whether the mappings changed since. A project on the all-in-one image's bundled Couchbase can't be exported: choose another target first.

## Running the script

The zip unpacks into one folder named like it:

| File | What it is |
|---|---|
| `migrate.sh` | The runner. The same script for every export from one odb release: install it once, and give it other files for other migrations |
| `config.json` | **Where:** the source and target connection details (below) |
| `mappings.json` | **What:** the tables, the documents they become and how; each mapping's `outline` lists what fills each field |
| `odb` | Only when included: odb for the chosen platform |
| `README.md` | Written for this export |

The script keeps everything it writes (the state directory and a downloaded `odb`) in that folder.

```bash
unzip odb-export-<project>-<date>.zip       # unpacks into its own odb-export-<project>-<date>/ folder
cd odb-export-<project>-<date>
./migrate.sh run                     # checks everything, then migrates
```

### Connection details: `config.json`

The source and target connection details (host, port, database, user, password…) are in `config.json` beside `migrate.sh`, not in the script. To run the same migration against another environment, or after a password change, edit `config.json` and run again; there's no need to export again.

```json
{
  "source": {
    "type": "postgres",
    "database": "shop",
    "host": "db.internal",
    "port": 5432,
    "sslMode": "require",
    "username": "migrator",
    "password": "base64:cGctU2VjcjN0"
  },
  "target": {
    "type": "couchbase",
    "connectionString": "couchbases://cb.example.com",
    "username": "cbuser",
    "password": "base64:Y2ItVGFyZ2V0"
  }
}
```

- Passwords (and TLS certificates and keys, when set) are kept base64-encoded with a `base64:` prefix. To change one, replace the whole value with the new password in plain text: the next `preflight`, `run`, `resume` or `verify` encodes it, saves `config.json` again (mode 0600) and says which value it encoded.
- base64 keeps passwords from being read at a glance; it isn't encryption. Anyone with `config.json` can use the credentials: keep it like a password.
- The `type` can't change. A JSON-files target's section has its `path` (relative to `config.json`'s folder when it isn't absolute) and `layout`; `ODB_TARGET_PATH` overrides the path for one run.
- `ODB_CONFIG=/path/to/prod.json ./migrate.sh run` uses another file, e.g. one per environment. Use a separate `ODB_STATE_DIR` per environment too: a checkpoint follows the migration, not the connection.
- Without the script: `odb run --mappings mappings.json --config config.json`.

### What it migrates: `mappings.json`

`mappings.json` holds the mappings and the run options, and no connection details or credentials. Each mapping has an `outline`, one line per document field, so the logic reads at a glance:

```json
"outline": [
  "key = order::{order_id}",
  "orderId ← orders.order_id",
  "customer ← customers (object, customer_id = customer_id)",
  "customer.name ← customers.name",
  "items[] ← order_items (array, order_id = order_id, at most 1000)",
  "items[].quantity ← order_items.quantity | toInteger",
  "type = \"order\""
]
```

`./migrate.sh inspect` prints the same. The file is checked against its sha256 when it runs, so export again from the tool to change it. `ODB_MAPPINGS=/path/to/other.json` runs another export's mappings with the same script.

`./migrate.sh preflight` runs only the checks. The script uses `ODB_BIN`, then an `odb` beside it, then `odb` on the `PATH`, then Docker, then downloads the release; a server with no release downloads includes its own `odb` in every export, and the export dialog names the machine it is for.

| Command | Does |
|---|---|
| `preflight` | Checks everything; writes no documents |
| `run [--mapping NAME] [-- odb options]` | Migrates, e.g. `./migrate.sh run -- --throttle 5000 --writers 32` |
| `resume` | Continues after Ctrl+C, a crash or a reboot, from the checkpoint |
| `status` | Summarizes the run in the state directory |
| `verify` | Counts each mapping's documents in the target |
| `inspect` | What `mappings.json` migrates, field by field (no secrets) |

Settings: `ODB_CONFIG` (default `config.json` next to `migrate.sh`), `ODB_MAPPINGS` (default `mappings.json` next to it), `ODB_STATE_DIR` (default `odb-state` next to it: `checkpoint.json`, `errors.jsonl`, `report.json`), `ODB_CACHE_DIR` (default `odb-cache` next to it), `ODB_TARGET_PATH` (a directory target's folder, instead of `config.json`'s), `ODB_RUNTIME` (`auto`, `binary` never uses Docker, `docker`), `ODB_BIN`, `ODB_DOCKER_ARGS`.

With `auto` the script uses `ODB_BIN`, then `odb` beside it, then `odb` on the `PATH`, then Docker (with the folder mounted at the same path, so paths in `config.json` mean the same), then downloads `odb` and checks it against the checksum written into the script (and its cosign signature when `cosign` is installed). Only odb of the script's version is used. It needs bash 3.2 or later (macOS or Linux).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Completed |
| 2 | Completed with errors below the threshold — see `errors.jsonl` |
| 3 | Failed: error threshold exceeded or a fatal error — see `report.json` |
| 4 | Not started: preflight or configuration problem (bad `mappings.json` or `config.json`, schema drift, wrong credentials, target not writable, bad command line) |
| 5 | Cancelled after saving the checkpoint — `./migrate.sh resume` continues |

If the source schema changed since the export, preflight stops with `SCHEMA_DRIFT` naming the columns; `--allow-drift` continues when every change is compatible.

## Importing the report

On the Export page, **Import report** and choose the run's `report.json`. It appears in the project's jobs as an **External** run with its counts, errors and verification — read-only, since it ran elsewhere.
