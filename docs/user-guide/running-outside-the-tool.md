# Running migrations outside the tool

Export a project and run the migration on another machine (a production server, an air-gapped site) without Omni-Data-Bus or access to it. An export is a folder with `config.json` saying **where** (the connections) and `mappings.json` saying **what** (the mappings). The `odb` command runs it: the same engine as the tool, so the documents are identical.

Nothing in the folder is specific to one operating system. Test the export on your Mac or Windows laptop, then copy the same folder to the Linux server, and run the same commands there.

## Exporting

Review → **Export…** → **New export**.

1. **Project**: the export uses the project exactly as configured, meaning its source, target, saved credentials and every ready mapping. They're shown read-only; to change one, follow its *Change in …* link, then export again.
2. **Options**: the run options (readers, writers, batch size, write mode, max errors). Also, only for a machine without internet access, **Add odb for a machine without internet access** and which machine: macOS (Intel or Apple silicon), Windows, Linux x86-64 or Linux ARM64.

**Export** downloads the zip at once: there's no passphrase and no password to confirm. Exporting needs the editor role. Every export is in the **Export history**, with who made it, its bundle hash, and whether the mappings changed since. A project on the all-in-one image's bundled Couchbase can't be exported: choose another target first.

## Running it

The zip unpacks into one folder named like it:

| File | What it is |
|---|---|
| `config.json` | **Where:** the source and target connection details (below) |
| `mappings.json` | **What:** the tables, the documents they become and how; each mapping's `outline` lists what fills each field |
| `bin/odb` (`bin/odb.exe`) | Only when added: odb for the chosen machine |
| `README.md` | Written for this export: the commands, the download links and checksums |

In the folder, run the same commands in Terminal, PowerShell, cmd or a Linux shell:

```
cd odb-export-<project>-<date>
odb preflight      # checks the source, credentials, schema and target; writes nothing
odb run            # checks everything again, then migrates
odb status         # summarizes the run
```

odb finds `config.json` and `mappings.json` itself: in the current directory, or beside odb when you run it from elsewhere (for example `/opt/export/bin/odb run`). Everything it writes (the checkpoint, `errors.jsonl`, `report.json`) goes to `odb-state` in the export folder.

| Command | Does |
|---|---|
| `odb preflight` | Checks everything; writes no documents |
| `odb run [--mapping NAME] [options]` | Migrates, for example `odb run --throttle 5000 --writers 32` (`odb run help` lists the options) |
| `odb resume` | Continues after Ctrl+C, a crash or a reboot, from the checkpoint |
| `odb status` | Summarizes the run in the state directory |
| `odb verify` | Counts each mapping's documents in the target |
| `odb inspect` | What `mappings.json` migrates, field by field (no secrets) |
| `odb help` | The commands, the files and the exit codes |

On a terminal, progress is printed as lines and the run ends with a sentence explaining its exit code. Piped, redirected or in a container, odb prints JSON lines (the 2.0 format) for pipelines and log collectors. `--log-format json|text` chooses explicitly.

### Installing odb

odb is one file with nothing else to install. An export needs odb from the release that exported it, or a later release with the same major version: a 2.5 export runs on odb 2.5.0 or a later 2.x release. preflight refuses any other odb and says which one to install.

The export's README lists the download for each machine with its SHA-256:

- **macOS**: one binary for Intel and Apple silicon Macs, signed and notarized, so it opens with no warning.
- **Windows**: `odb.exe` in a zip, signed.
- **Linux** x86-64 and ARM64: a static binary.

```bash
tar -xzf odb_<version>_<machine>.tar.gz odb && sudo mv odb /usr/local/bin/    # macOS, Linux
```

```powershell
Expand-Archive odb_<version>_windows_amd64.zip -DestinationPath $HOME\odb; $env:Path += ";$HOME\odb"
```

**On a machine without internet access**, export with **Add odb for a machine without internet access**, choose that machine, and run `bin/odb preflight` (`.\bin\odb.exe preflight` in PowerShell) in the folder.

An odb from a development build of the tool isn't signed. macOS then blocks it until you run `xattr -d com.apple.quarantine bin/odb`, and Windows SmartScreen asks you to confirm (*More info → Run anyway*). The dialog and the README say so for such builds.

### With Docker

On a machine with Docker and no odb, the release image runs the same commands. Mount the export folder and work in it:

```bash
docker run --rm -it --user "$(id -u):$(id -g)" -v "$PWD:$PWD" -w "$PWD" <image> preflight
```

In PowerShell: `docker run --rm -it -v "${PWD}:/work" -w /work <image> preflight`. Replace `preflight` with `run`, `resume`, `status` or `verify`; the README names the image for your release.

- **A database on this machine:** in a container, `localhost` is the container itself. Add `--network host` (Linux), or use `host.docker.internal` instead of `localhost` in `config.json` (Docker Desktop). When a connection to `localhost` fails in a container, odb says the same.
- **A directory target outside the folder** must be mounted too (`-v /data/out:/data/out`). Otherwise its files would vanish with the container, so odb stops at preflight and tells you which `-v` to add.

### Connection details: `config.json`

The source and target connection details (host, port, database, user, password…) are in `config.json`. To run the same migration against another environment, or after a password change, edit `config.json` and run again; there's no need to export again.

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

- **Passwords** (and TLS certificates and keys, when set) are kept base64-encoded with a `base64:` prefix. To change one, replace the whole value with the new password in plain text. The next `preflight`, `run`, `resume` or `verify` encodes it, saves `config.json` again (mode 0600) and says which value it encoded.
- **base64 isn't encryption.** It keeps passwords from being read at a glance, but anyone with `config.json` can use the credentials, so keep it like a password.
- **The `type` can't change.** A JSON-files target's section has its `path` (relative to `config.json`'s folder when it isn't absolute) and `layout`. `--target-path` or `ODB_TARGET_PATH` overrides the path for one run.
- **Per environment:** `odb run --config prod.json` (or `ODB_CONFIG`) uses another file, for example one per environment. Use a separate state directory per environment too (`--state-dir` or `ODB_STATE_DIR`): a checkpoint follows the migration, not the connection.

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

`odb inspect` prints the same. The file is checked against its sha256 when it runs, so export again from the tool to change it. `--mappings` (or `ODB_MAPPINGS`) runs another export's mappings.

### Settings

Each setting has a flag and an environment variable; the flag wins.

| Flag | Variable | Default |
|---|---|---|
| `--config` | `ODB_CONFIG` | `config.json` in the current directory, else beside odb |
| `--mappings` | `ODB_MAPPINGS` | `mappings.json` in the current directory, else beside odb |
| `--state-dir` | `ODB_STATE_DIR` | `odb-state` in the folder `mappings.json` was found in |
| `--target-path` | `ODB_TARGET_PATH` | a directory target's `path` in `config.json` |

Set a variable for one shell with `export ODB_STATE_DIR=/data/state` (macOS, Linux), `$env:ODB_STATE_DIR = "D:\state"` (PowerShell) or `set ODB_STATE_DIR=D:\state` (cmd).

**Folder names** in a directory target's mappings are checked against Windows' rules (no `con`, `aux.json` or names ending in `.`) on every OS. Document file names are encoded the same way everywhere (`orders::1` becomes `orders%3A%3A1.json`), so a run on any machine writes the same files.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Completed |
| 2 | Completed with errors below the threshold: see `errors.jsonl` |
| 3 | Failed: error threshold exceeded or a fatal error; see `report.json` |
| 4 | Not started: preflight or configuration problem (bad `mappings.json` or `config.json`, schema drift, wrong credentials, target not writable, an odb from another release, bad command line) |
| 5 | Cancelled after saving the checkpoint: `odb resume` continues |

If the source schema changed since the export, preflight stops with `SCHEMA_DRIFT` naming the columns; `--allow-drift` continues when every change is compatible.

## Importing the report

On the Export page, choose **Import report** and select the run's `report.json` (in `odb-state`). It appears in the project's jobs as an **External** run with its counts, errors and verification. It's read-only, since it ran elsewhere.

## Exports from 2.0

A 2.0 export holds a `migrate.sh`, which still works: it runs odb 2.0, as before. odb 2.5 also runs a 2.0 export folder directly: `odb preflight` and `odb run` in it, since a 2.0 export accepts any odb from 2.0 up to, but not including, 3.0.
