# Running migrations outside the tool

Export a project as a script and run the migration on another machine — a production server, an air-gapped site — without Omni-Data-Bus or access to it. The script runs `odb run`, the same engine as the tool: the documents are identical.

## Exporting

Review → **Export…** → **New export**.

1. **Project** — the export uses the project exactly as configured: its source, target, saved credentials and every ready mapping. They are shown read-only; to change one, follow its *Change in …* link, then export again.
2. **Passphrase** — the saved source and target credentials go into the export **sealed** with this passphrase (argon2id + AES-256-GCM). Use **Generate** for six random words. It is shown once after the download: store it in your secret manager. Nothing in the export is readable without it.
3. **Runtime & options** — a `migrate.sh` script (a zip with the script and a README written for this export) or the bundle file alone, how the script finds `odb`, and run options.

Exporting needs the editor role and your password again (valid 5 minutes). Every export is in the **Export history** with who made it, its bundle hash, and whether the mappings changed since. A project on the all-in-one image's bundled Couchbase can't be exported: choose another target first.

## Running the script

The zip unpacks into one folder named like it, and the script keeps everything it writes (the state directory and a downloaded `odb`) in that folder.

```bash
unzip odb-export-<project>-<date>.zip       # unpacks into its own odb-export-<project>-<date>/ folder
cd odb-export-<project>-<date>
./migrate.sh run                     # asks for the passphrase, checks everything, then migrates
```

The target folder of a JSON-files target is the one set in the project (`ODB_TARGET_PATH` overrides it). `./migrate.sh preflight` runs only the checks. With *How it runs odb: Automatic*, the script uses an installed `odb`, else Docker, else downloads the release; a server with no release downloads puts its own `odb` in the script instead, and the export dialog names the machine it is for.

| Command | Does |
|---|---|
| `preflight` | Checks everything; writes no documents |
| `run [--mapping NAME] [-- odb options]` | Migrates, e.g. `./migrate.sh run -- --throttle 5000 --writers 32` |
| `resume` | Continues after Ctrl+C, a crash or a reboot, from the checkpoint |
| `status` | Summarizes the run in the state directory |
| `verify` | Counts each mapping's documents in the target |
| `inspect` | What the script will do (no secrets) |
| `extract [DIR]` | Writes `bundle.odb.json` (and the embedded `odb`, if any) |

Settings: `ODB_STATE_DIR` (default `odb-state` next to `migrate.sh`: `checkpoint.json`, `errors.jsonl`, `report.json`), `ODB_CACHE_DIR` (default `odb-cache` next to `migrate.sh`), `ODB_TARGET_PATH` (a directory target's folder on this machine), `ODB_RUNTIME` (`auto`, `binary`, `docker`, `embedded`), `ODB_BIN`, `ODB_DOCKER_ARGS`.

With `auto` the script uses `ODB_BIN`, then an `odb` embedded in the script, then `odb` on the `PATH`, then Docker, then downloads `odb` and checks it against the checksum written into the script (and its cosign signature when `cosign` is installed). It needs bash 3.2 or later (macOS or Linux).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Completed |
| 2 | Completed with errors below the threshold — see `errors.jsonl` |
| 3 | Failed: error threshold exceeded or a fatal error — see `report.json` |
| 4 | Not started: preflight or configuration problem (bad bundle, schema drift, wrong passphrase, target not writable, bad command line) |
| 5 | Cancelled after saving the checkpoint — `./migrate.sh resume` continues |

If the source schema changed since the export, preflight stops with `SCHEMA_DRIFT` naming the columns; `--allow-drift` continues when every change is compatible.

## Importing the report

On the Export page, **Import report** and choose the run's `report.json`. It appears in the project's jobs as an **External** run with its counts, errors and verification — read-only, since it ran elsewhere.
