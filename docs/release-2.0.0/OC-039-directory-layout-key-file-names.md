# OC-039 — Directory layout, key-to-file-name encoding & lookup by primary key

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E8 Directory Target | P0 | 5 | OC-038, OC-016 | Not started |

## User story
As an **application developer consuming the exported files**, I want each document stored at a predictable path derived from its primary key, for example `orders/1001.json`, so that I can open any document directly when I know its key, without scanning or an index service.

## Scope
**In:**
- Three directory layouts (`flat`, `sharded`, `key-path`) and the folder structure they produce.
- A reversible, cross-platform file-name encoding of document keys (`pathcodec`), in Go and TypeScript.
- Directory-friendly key template defaults: the primary key alone.
- An optional manifest (`key → path`) for tools that prefer an index.
- `odb doc path` CLI and an API to resolve a key to its path.

**Out:** secondary indexes on other fields (use the manifest or load the files into Couchbase).

## Implementation details

### Layouts
| Layout | Path for key `1001` in folder `orders` | When to use |
|---|---|---|
| `flat` (default) | `orders/1001.json` | Up to a few million files per folder |
| `sharded` | `orders/3b/7a/1001.json` (first 4 hex characters of `sha1("1001")`) | Tens of millions of files; keeps each folder under ~10k entries |
| `key-path` | Key `2026::10::1001` → `orders/2026/10/1001.json` | Keys that already carry a hierarchy (split on `::`, each segment encoded) |

The layout is shown as a live example in the Target step: "Document `1001` → `/data/odb-out/orders/1001.json`".

### Key defaults for directory targets (extends OC-016)
- The default key template is the primary key only: `{order_id}` for a single-column PK, `{order_id}::{line_no}` for a composite PK. With `key-path`, a composite PK becomes nested folders.
- The type prefix (`order::`) that 1.0 adds for Couchbase is not added, since the folder already says what the document is. The user can still edit the template.
- `{$uuid}` keys (no PK) still work and still raise `NON_IDEMPOTENT_KEY`. The UI adds that such files can't be found by primary key.

### `pathcodec` (`internal/pathcodec`, `web/src/lib/pathcodec`)
- `Encode(key) string` percent-encodes (`%XX`, uppercase hex) the bytes that are unsafe on any of Linux, macOS or Windows: `/ \ : * ? " < > |`, `%` itself, control characters (0x00–0x1F, 0x7F), and a leading `.` or `-`, and trailing `.` or space.
- Windows reserved names (`CON`, `PRN`, `AUX`, `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9`, case-insensitive, with or without extension) get their first character encoded.
- If the encoded name plus extension exceeds 255 bytes, it becomes `<first 200 bytes>~<sha1(key)[:16]>`. That is not reversible from the name alone, so such keys are listed in the manifest and flagged `KEY_NAME_HASHED` (a row warning).
- `Decode(name) (key, ok)` reverses it, with `ok=false` for hashed names.
- Non-ASCII UTF-8 is kept as-is (valid on all three platforms) and normalized to NFC, so macOS and Linux agree on the name.
- **Case-insensitive file systems** (detected in OC-038): if the key template uses string columns, validation warns with `KEY_CASE_COLLISION_RISK` ("`ABC` and `abc` would be the same file here"), and the writer detects a real collision at runtime (OC-040).

### Manifest (optional, default on)
- The writer appends `{"key","path","bytes","sha256","sourcePk"}` lines to `_odb/manifest/<mapping>-<writerId>.jsonl` (one file per writer, so there are no locks). At job completion the files are merged, deduplicated by key with the last write winning, into `_odb/manifest/<mapping>.jsonl` and sorted by key.
- `_odb/run.json` records the job ID, plan hash, layout, key template and pathcodec version, so a reader knows how paths were built.

### Lookup
- CLI: `odb doc path --layout sharded --folder orders 1001` prints `orders/3b/7a/1001.json`. `odb doc get --root /data/odb-out --folder orders 1001` prints the document. It reads `_odb/run.json` for the layout when present.
- API: `GET /mappings/{id}/documents/{key}` works for both target kinds: a KV get for Couchbase, a file read for a directory. It returns the document and, for directories, its path.
- A composite PK lookup takes the values in key-template order: `odb doc get orders 1001 3`.

## Acceptance criteria & unit tests

### AC1 — Deterministic paths per layout
**Given** key `1001` in folder `orders`, **then** `flat` → `orders/1001.json`, `sharded` → `orders/<sha1 prefix>/1001.json`, and `key-path` with key `2026::10::1001` → `orders/2026/10/1001.json`. **Given** `gzip`, **then** the extension is `.json.gz`.

| Test | Location | Verifies |
|---|---|---|
| `TestPath_AC1_Layouts` | `internal/connectors/target/directory/layout_test.go` | |

### AC2 — Encoding is safe and reversible
**Given** keys with `/`, `:`, `%`, a leading dot, `CON`, emoji, NFD accents and 300-byte strings, **then** every encoded name is valid on Linux, macOS and Windows rules, `Decode(Encode(k)) == NFC(k)` for all except hashed names, and long names are ≤ 255 bytes with `KEY_NAME_HASHED`.

| Test | Location | Verifies |
|---|---|---|
| `TestEncode_AC2_Fixtures` | `internal/pathcodec/pathcodec_test.go` | Shared `testdata/pathcodec.json` |
| `FuzzEncodeDecode` | same | Round trip, no separators in output |
| `pathcodec.test.ts` › `AC2: matches server fixtures` | `src/lib/pathcodec/pathcodec.test.ts` | |

### AC3 — Directory key defaults
**Given** a directory target and table `orders(order_id PK)`, **then** the default template is `{order_id}`. **Given** a Couchbase target, **then** it stays `order::{order_id}`.

| Test | Location | Verifies |
|---|---|---|
| `TestDefaultKeyTemplate_AC3_ByTargetKind` | `internal/mapping/keytemplate_test.go` | |

### AC4 — Manifest
**Given** 3 writers writing 10,000 docs with 5 keys written twice (retries), **then** the merged manifest has 10,000 unique sorted lines, and each line's `sha256` matches its file.

| Test | Location | Verifies |
|---|---|---|
| `TestManifest_AC4_MergeDedupeSort` | `internal/connectors/target/directory/manifest_test.go` | |

### AC5 — Lookup by primary key
**Given** a completed directory migration, **when** `odb doc get --folder orders 1001` runs or `GET /mappings/{id}/documents/1001` is called, **then** the document for `order_id=1001` is returned. **Given** a missing key, **then** exit code 1 or `404 DOCUMENT_NOT_FOUND`.

| Test | Location | Verifies |
|---|---|---|
| `TestDocGet_AC5_ByPrimaryKey` | `cmd/odb/doc_test.go` | |
| `TestGetDocument_AC5_DirectoryAndCouchbase` | `internal/httpapi/documents_test.go` | |

### AC6 — Case-collision warning
**Given** a case-insensitive target and a key template over a `varchar` column, **then** validation shows `KEY_CASE_COLLISION_RISK`. **Given** an integer PK, **then** no warning.

| Test | Location | Verifies |
|---|---|---|
| `TestValidate_AC6_CaseCollisionRisk` | `internal/mapping/validate_test.go` | |
