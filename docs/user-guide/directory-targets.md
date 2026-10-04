# Directory targets

A project's target can be a **folder of JSON files** instead of a Couchbase cluster: one file per document, named after its key. Mappings build exactly the same keys and documents whichever kind of target you choose, so you can switch later (Target step → *Move mappings*).

## Choosing the folder

On the Target step pick **Directory of JSON files**, then **Browse** for a folder. The server only offers folders inside its user's home folder and `/data` (hidden folders such as `.ssh` are never offered). **Test connection** checks the folder is writable and reports the free space.

- **All-in-one image:** the folder you shared with `run.sh --output` (default `~/omni-data-bus-output`) appears as *On your computer*; files written there show up on your machine.
- **Kubernetes / Docker:** mount a volume at `/data/odb-out` (Helm: `directoryTarget.enabled=true`; use a ReadWriteMany claim when several workers run).

## Settings

| Setting | Values | Notes |
|---|---|---|
| Layout | `flat` (default), `sharded`, `key-path` | See below |
| Compression | `none`, `gzip` | gzip writes `.json.gz` |
| Durability | `none` (default), `file`, `file+dir` | `file` fsyncs each file; on macOS that is a full device flush (~220 files/s), so leave it off unless you need power-loss safety |
| Pretty | on / off | Indented JSON |
| Max document size | 1 KiB – 1 GiB | Larger documents fail with `DOC_TOO_LARGE` |

Writes are atomic (a temporary file, then a rename), so a reader never sees half a document.

## Where a document lands

A file's path is a pure function of its key, so whoever knows the primary key can find the file:

```
<folder>/<encoded key>.json                       flat:     orders::1001 → orders/orders%3A%3A1001.json
<folder>/<h0h1>/<h2h3>/<encoded key>.json         sharded:  spreads millions of files over subfolders
<folder>/<seg1>/<seg2>/…/<encoded last>.json      key-path: orders::1001 → orders/orders/1001.json
```

Characters that aren't safe on Linux, macOS or Windows are percent-encoded; very long names get a hash suffix. From a terminal:

```bash
odb doc path --folder orders --layout flat 'orders::1001'     # where it is
odb doc get --root /data/out --folder orders 1001             # print it, by primary key
```

Each run also writes `_odb/run.json` and a manifest per mapping (`_odb/manifest/<mapping>.jsonl`) listing every key and file, which verification and the **Output** tab use.

## After the run

The job's **Output** tab lists the files and finds a document by key; verification compares counts and sampled documents just as for Couchbase.
