# OC-050 — Portable migration bundle format

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 5 | OC-036, OC-037, OC-042 | Implemented |

## User story
As a **data engineer**, I want to export everything needed to run a migration (source and target settings, mappings, relationships and job options) as one versioned, self-describing file, so that the migration can run outside the tool and be reviewed, versioned in git and re-run the same way.

## Scope
**In:**
- The bundle format ([architecture §6.3](00-architecture.md#63-portable-bundle-odbjson-schema-apibundleschemajson)) and its JSON Schema, `api/bundle.schema.json`.
- Server-side generation from a project and a selection of mappings, with optional target and job-option overrides.
- A source schema fingerprint, used by the runner to detect drift.
- An integrity hash over canonical JSON.
- `odb bundle validate | inspect | diff` CLI commands.

- Refuse to export a project whose target is the all-in-one image's bundled Couchbase (`couchbase://127.0.0.1`, saved by *Use the bundled Couchbase*, OC-072): 422 `BUNDLED_TARGET_NOT_EXPORTABLE`, since that address means nothing outside the container. Test `TestExports_AC6_BundledTargetRefused` (deferred here from OC-072 AC6).

**Out:** credential sealing (OC-052; this story writes `credentials.mode: "external"` placeholders), script and kit generation (OC-053/054), and importing a bundle back into the tool as a project (post-2.0).

## Implementation details

### Contents (`internal/export/bundle.go`)
| Section | Source | Notes |
|---|---|---|
| `bundleVersion` | constant `1` | The runner refuses unknown major versions with exit 4 |
| `odbVersion` | server version | Semver range `>=<current minor> <next major>` |
| `project` | projects | id, name (informational only) |
| `source` | project source connection | Non-secret `config`, plus a `credentials` block (OC-052) |
| `target` | project target connection, or the export's override | A Couchbase project can export a directory target, for example to write JSON files in production |
| `mappings[]` | selected mappings | Spec upgraded to v2, `location`, `planHash`. Design-time data is stripped: `docSchema.reference` and field `example`s (OC-059) never go into a bundle |
| `relationships[]` | virtual relationships used by the selected mappings (OC-042) | |
| `jobOptions` | project defaults or export overrides | batchSize, readers, writers, write mode, error thresholds, throttle |
| `sourceFingerprint` | latest discovery snapshot | Per table: sha256 over `(column name, udtName, nullable)` for referenced columns only |
| `integrity` | computed | sha256 of the canonical JSON with `integrity` removed |

- **Canonical JSON:** keys sorted, UTF-8, no insignificant whitespace, numbers in shortest round-trip form (RFC 8785 JCS). The same bundle produces the same bytes, so `git diff` shows only real changes.
- Every mapping must pass validation (OC-049 AC6). Otherwise `422 MAPPING_INVALID`.
- Target overrides are validated with the target kind's `Validate`. For a directory override, the allowlist (OC-038) doesn't apply, because the path is on the runner's machine; the UI says so.

### Drift detection (used by OC-051)
- At preflight, the runner rediscovers only the referenced tables and compares fingerprints. A mismatch → exit 4 `SCHEMA_DRIFT` with a per-table diff (added, removed or retyped columns). `--allow-drift` continues only if every mapped column still exists with a compatible type.

### API & CLI
- `POST /projects/{projectId}/exports {mappingIds, targetOverride?, jobOptions?, credentialMode, runtime}` returns the artifact (OC-055 wraps it).
- `odb bundle validate <file>`: schema, integrity, version, and plan compilation. Exit 0 or 4.
- `odb bundle inspect <file>`: a human summary (source host and DB, target, mappings with row estimates, credential mode). Secrets are never printed.
- `odb bundle diff <a> <b>`: a semantic diff (mappings, relations, options).

## Results (2026-10-02)
- `internal/export`: bundle model, RFC 8785 canonical JSON (`canonical.go`, passes the RFC vectors), builder (`build.go`), fingerprint and drift (`fingerprint.go`), schema check and version gate (`validate.go`), shared secret scanner (`scan.go`). `api/bundle.schema.json` is embedded (a test keeps the copy identical). `odb bundle validate | inspect | diff` in `cmd/odb/bundle.go`.
- Exported twice, bundles differ only in `createdAt`/`integrity`; the file is canonical JSON indented, and re-indenting a bundle keeps it valid (integrity covers the canonical form).
- Verified through the running server: export → `migrate.sh` → 10,000 files (see OC-053).

### Changes from the plan
- `integrity` has two hashes: `sha256` (whole bundle) and `contentSha256` (without credentials). The sealed blocks' AAD is `contentSha256`; a hash that covered the ciphertext couldn't also be the ciphertext's AAD.
- `sourceFingerprint.tables` maps each table to `{sha256, columns[]}` rather than a bare hash, so `SCHEMA_DRIFT` can name the column offline.
- No *Use directory key defaults* option: keys are the same for every target kind (user decision 2026-09-29), so a directory override only relocates mappings (`connections.Relocated`, OC-068 rules).
- `odb bundle validate` checks each plan as far as possible without the source (spec decodes, relation tree, key template, location); the full compile happens at preflight against the live schema. `inspect` shows no row estimates (they would make bundles change with table statistics); preflight reports them.
- The bundled-target refusal applies to the bundle's target: a project on the bundled node can still export with a target override.
- `POST /projects/{id}/exports` lives in `internal/export/handlers.go` (not `internal/httpapi`), next to the service, like the other feature handlers.

### Change (user decision, 2026-10-02): the project as configured
An export is the project exactly as configured: its source, target, saved credentials and every ready mapping. There is no target override and no mapping selection — the dialog shows them read-only, each with a link to the step that changes them. `POST /projects/{id}/exports` takes only `passphrase`, `runtime`, `script` and `jobOptions` and rejects anything else (400). AC4 is now `TestBundle_AC4_TargetAsConfigured`. A directory target's path can still differ on the runner machine (`--target-path`, `ODB_TARGET_PATH`); a project on the bundled Couchbase must change its target in the Target step before exporting.

## Acceptance criteria & unit tests

### AC1 — Schema-valid & deterministic
**Given** a project with 2 mappings, **when** exported twice, **then** both bundles validate against `api/bundle.schema.json` and are byte-identical except `createdAt`, and `integrity` verifies.

| Test | Location | Verifies |
|---|---|---|
| `TestBundle_AC1_SchemaValid` | `internal/export/bundle_test.go` | |
| `TestBundle_AC1_Deterministic` | same | |
| `TestCanonicalJSON_AC1_JCSVectors` | `internal/export/canonical_test.go` | RFC 8785 test vectors |

### AC2 — Tamper detection
**Given** a bundle with one edited mapping field, **then** `odb bundle validate` exits 4 with `BUNDLE_INTEGRITY`.

| Test | Location | Verifies |
|---|---|---|
| `TestBundleValidate_AC2_Tampered` | `cmd/odb/bundle_test.go` | |

### AC3 — Contents are complete and secret-free
**Given** a project with a source password, **then** the bundle (with the `external` mode used in this story) contains no password, token or private key. This is checked by the secret scanner over the output. The bundle does include the virtual relationships used and excludes unused ones.

| Test | Location | Verifies |
|---|---|---|
| `TestBundle_AC3_NoSecretsScanner` | `internal/export/bundle_test.go` | Shared scanner used by OC-052/053/054 |
| `TestBundle_AC3_IncludesUsedRelationships` | same | |

### AC4 — Target override
**Given** a Couchbase project exported with target override `directory: /data/out`, **then** the bundle's target is the directory, each mapping's location is `{folder: <type value>}`, and key templates switch to directory defaults only when the user ticks *Use directory key defaults*.

| Test | Location | Verifies |
|---|---|---|
| `TestBundle_AC4_DirectoryOverride` | `internal/export/bundle_test.go` | |

### AC5 — Drift detection
**Given** a bundle fingerprint and a source where `order_items.quantity` changed from `int4` to `text`, **then** preflight reports `SCHEMA_DRIFT` naming the column. **With** `--allow-drift`, **then** it continues (text→integer coercion is compatible under OC-020) with a warning.

| Test | Location | Verifies |
|---|---|---|
| `TestFingerprint_AC5_Diff` | `internal/export/fingerprint_test.go` | |

### AC6 — Version gate
**Given** `bundleVersion: 2` or an `odbVersion` range that excludes the running binary, **then** exit 4 with `BUNDLE_VERSION_UNSUPPORTED`, naming the versions.

| Test | Location | Verifies |
|---|---|---|
| `TestBundleValidate_AC6_Versions` | `cmd/odb/bundle_test.go` | |
