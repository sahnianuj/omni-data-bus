# Migration: Shop → docs

Exported 2026-10-01T10:00:00Z by user@example.com with Omni-Data-Bus 2.0.0.
mappings.json sha256: `<sha256>`

This folder runs the migration without the Omni-Data-Bus tool, on macOS, Windows or Linux, from
any machine that can reach the source database.
Nothing in it is specific to one operating system: test it on your laptop, then copy the same
folder to the server.

| File | What it is |
|---|---|
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

`odb inspect` prints each mapping's outline: every document field and what fills it.

## Run it

Unzip the export and run `odb` inside its folder: the same commands in Terminal, PowerShell,
cmd or a Linux shell. ([Install odb](#install-odb) first if `odb version` doesn't print 2.0.0.)

```
cd odb-export-shop-docs-20261001
odb preflight      # checks the source, credentials, schema and target; writes nothing
odb run            # checks everything again, then migrates
odb status         # summarizes the run
```

The JSON files go to `/data/out` (`target.path` in `config.json`; a relative path is
relative to this folder). `--target-path DIR` or `ODB_TARGET_PATH` writes elsewhere for one run.

- If the run stops (Ctrl+C, a reboot, a failure), `odb resume` continues from the last
  checkpoint in `odb-state` in this folder.
- `odb run` also continues from that checkpoint, so after a completed run it writes nothing, even
  if you deleted the migrated files. To migrate everything again, run
  `odb run --restart`: it moves the old checkpoint, errors and report to `archive/<time>/` in the
  state directory and starts over.
- `odb verify` counts the documents in the target; `odb run help` lists the run options
  (`--throttle`, `--writers`, `--mapping`, …).
- On a terminal, progress is printed as lines; piped or in a container, as JSON lines
  (`--log-format json|text` to choose).

## Install odb

odb is one file with nothing else to install. This export needs odb 2.0.0 or a later 2.x release.

| Machine | Download | SHA-256 |
|---|---|---|
| Linux x86-64 | [odb_2.0.0_linux_amd64.tar.gz](https://example.com/releases/v2.0.0/odb_2.0.0_linux_amd64.tar.gz) | `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` |
| macOS (Apple silicon) | [odb_2.0.0_darwin_arm64.tar.gz](https://example.com/releases/v2.0.0/odb_2.0.0_darwin_arm64.tar.gz) | `bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb` |

**macOS and Linux:**

```bash
tar -xzf odb_2.0.0_<machine>.tar.gz odb
sudo mv odb /usr/local/bin/     # or any folder on your PATH
odb version
```

**With Docker instead** (the same commands, nothing to install):

```bash
docker run --rm -it --user "$(id -u):$(id -g)" -v "$PWD:$PWD" -w "$PWD" ghcr.io/example/omni-data-bus@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc preflight
```

In PowerShell: `docker run --rm -it -v "${PWD}:/work" -w /work ghcr.io/example/omni-data-bus@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc preflight`.
Replace `preflight` with `run`, `resume`, `status` or `verify`. If the database runs on this
machine, use `--network host` (Linux) or `host.docker.internal` instead of `localhost` in
`config.json`.

## Run it elsewhere, or run other migrations

- **Another environment, same mappings:** edit `config.json`, or keep one file per environment
  and pick it with `odb run --config prod.json` (or `ODB_CONFIG`). Use another state directory
  for each environment (`--state-dir` or `ODB_STATE_DIR`): a checkpoint follows the migration,
  not the connection.
- **Other mappings:** run odb in another export's folder, or pass `--mappings` (or `ODB_MAPPINGS`).
- In `config.json`, each section (`source`, `target`) lists the connection's `type`, its
  settings (host, port, database, user…) and its `password`. The `type` can't change.
  Passwords are kept as `"base64:…"`: to change one, replace the whole value, prefix included,
  with the new password in plain text, and the next run encodes it and saves the file again.
- `mappings.json` is checked against its sha256 when it runs: export it again from the tool to
  change it, rather than editing it.

### Settings

Each has a flag and an environment variable; the flag wins.

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--config` | `ODB_CONFIG` | `config.json` here | Where: source and target connection details |
| `--mappings` | `ODB_MAPPINGS` | `mappings.json` here | What: the mappings |
| `--state-dir` | `ODB_STATE_DIR` | `odb-state` here | Checkpoint, `errors.jsonl` and `report.json` |
| `--target-path` | `ODB_TARGET_PATH` | `target.path` in `config.json` | Folder the JSON files are written to |

"Here" is the current directory, or the folder odb is in when you run it from elsewhere. To set a
variable for one shell: `export ODB_STATE_DIR=/data/state` (macOS, Linux),
`$env:ODB_STATE_DIR = "D:\state"` (PowerShell) or `set ODB_STATE_DIR=D:\state` (cmd).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Completed |
| 2 | Completed with errors, below the error threshold: see `errors.jsonl` |
| 3 | Failed: error threshold exceeded or a fatal error; see `report.json` |
| 4 | Not started: preflight or configuration problem (`config.json` or `mappings.json`, schema drift, wrong credentials, target not writable, or an odb from another release) |
| 5 | Cancelled after saving the checkpoint: run `odb resume` |

After the run, import `report.json` from the state directory into the Omni-Data-Bus project
(Review → Export… → Import report) to see the production results next to your test runs.
