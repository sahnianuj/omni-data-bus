# OC-041 — Directory verification & output document browser

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E8 Directory Target | P1 | 5 | OC-040, OC-030 | Not started |

## User story
As a **data engineer**, I want the post-migration report to verify the files written to a directory, and a browser in the app to find and view any output document by its primary key, so that I can prove the export is complete and check individual documents.

## Scope
**In:**
- OC-030 verification for directory targets: counts, spot checks, manifest integrity.
- An **Output** tab on a finished job: find a document by primary key, view it, see its path, copy the path.
- Download a single document, or a small selection, from the browser.

**Out:** downloading the whole output folder from the app (use the volume, `kubectl cp` or OC-054's `fetch` command), and full-text search.

## Implementation details

### Verification (`internal/pipeline/verify.go` via `DocReader`)
- **Count:** files under each mapping's folder, excluding `_odb/` and `.odb-tmp/`, compared with the job's written count and the source row count (1.0 semantics: skipped and failed rows are explained in the report). Counting uses the merged manifest when present (O(1) reading of one file) and falls back to a directory walk.
- **Spot check:** 1.0 samples N source rows and rebuilds their documents. Here, each rebuilt document is compared with the file at its computed path (OC-039), byte for byte after decompression.
- **Manifest integrity:** every manifest line's file exists and its sha256 matches, checked for a random sample of 1,000 lines, or all lines when `--full`.
- Report sections are named per target kind ("Files" instead of "Documents in collection").

### Output browser (UI, `src/features/jobs/OutputBrowser.tsx`)
- A search box whose inputs follow the key template's columns ("order_id"), or a raw key.
- The result shows the path (with a copy button), size, modified time and pretty-printed JSON with folding, plus a **Download** button.
- The folder listing is paginated (`limit`/`cursor`, sorted by name) for browsing without a key.
- Works for Couchbase targets too, through the same API (OC-039 `GET /mappings/{id}/documents/{key}`), so the tab isn't directory-only.

### API
- `GET /jobs/{id}/output?mapping=&cursor=&limit=`: a page of `{key, path, bytes}` from the manifest or folder listing.
- `GET /mappings/{id}/documents/{key}?download=1`: `Content-Disposition: attachment`.
- Reads are confined to the mapping's folder under the allowlisted root, with the same symlink checks as OC-038.

## Acceptance criteria & unit tests

### AC1 — Count verification
**Given** a completed job with 10,000 rows, 3 dead-lettered, **then** the report shows 9,997 files, 3 explained failures, status *verified*. **Given** a file deleted manually afterwards, **then** re-verification shows 1 missing with its key.

| Test | Location | Verifies |
|---|---|---|
| `TestVerifyDirectory_AC1_Counts` | `internal/pipeline/verify_directory_test.go` | |
| `TestVerifyDirectory_AC1_MissingFile` | same | |

### AC2 — Spot check by path
**Given** a file edited after the run, **when** it falls in the sample, **then** the report shows a mismatch with a JSON diff.

| Test | Location | Verifies |
|---|---|---|
| `TestVerifyDirectory_AC2_SpotCheckMismatch` | `internal/pipeline/verify_directory_test.go` | |

### AC3 — Manifest integrity
**Given** a manifest line whose sha256 doesn't match its file, **then** the report lists it under *Integrity*.

| Test | Location | Verifies |
|---|---|---|
| `TestVerifyDirectory_AC3_ManifestHash` | `internal/pipeline/verify_directory_test.go` | |

### AC4 — Find by primary key
**Given** the Output tab, **when** the user enters `order_id = 1001`, **then** the document is shown with path `orders/1001.json`. **Given** an unknown key, **then** the empty state "No document with key 1001".

| Test | Location | Verifies |
|---|---|---|
| `OutputBrowser.test.tsx` › `AC4: finds by key and shows path` | `src/features/jobs/OutputBrowser.test.tsx` | |
| `OutputBrowser.test.tsx` › `AC4: not found state` | same | |

### AC5 — Path confinement
**Given** a key that encodes to a name containing `..` after a crafted decode, or a request for another mapping's folder, **then** `404` and an audit event `output.path_rejected`.

| Test | Location | Verifies |
|---|---|---|
| `TestGetDocument_AC5_Confinement` | `internal/httpapi/documents_test.go` | |
