# OC-052 — Credentials in exported bundles: sealed, plaintext or external

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P0 | 5 | OC-050, OC-006, OC-033 | Not started |

## User story
As a **security-conscious operator**, I want the source and target credentials baked into an exported migration so that it runs without extra setup, but protected by a passphrase by default. I also want the option to reference my own secret store instead, or knowingly accept plain text, so that exported scripts don't leak database passwords.

## Scope
**In:**
- Three credential modes (`sealed` default, `plaintext`, `external`), described in [architecture D4](00-architecture.md#d4--credentials-in-exported-bundles-are-sealed-by-default).
- Server-side unsealing of project credentials (OC-006) and re-sealing for export, without the plaintext ever reaching the browser.
- Runner-side unlock: prompt, env var, passphrase file, or Kubernetes Secret.
- Guard rails: role, step-up re-authentication, audit, passphrase strength, and warnings.

**Out:** HSM or KMS-bound bundles (post-2.0), and credential rotation inside a bundle (re-export instead).

## Implementation details

### Modes
| Mode | Bundle `credentials` block | Runner behavior |
|---|---|---|
| `sealed` | `{mode, kdf:{alg:"argon2id", salt, t:3, m:65536, p:1}, nonce, ciphertext}`. AES-256-GCM over the JSON `{password, …}`, with AAD = the bundle's `integrity` hash, so a sealed block can't be moved into another bundle | Passphrase from `--passphrase-file`, `ODB_BUNDLE_PASSPHRASE`, or a TTY prompt (in that order). Wrong passphrase → exit 4 `BUNDLE_PASSPHRASE_INVALID` |
| `plaintext` | `{mode, values:{…}}` | Used directly. The runner logs a warning at startup |
| `external` | `{mode, refs:{password:"env:PG_PASSWORD"}}` or `"file:/var/run/secrets/pg/password"` | Resolved at start; missing → exit 4 `CREDENTIAL_REF_UNRESOLVED` naming the ref |

- Each connection (source, target) has its own block. A directory target has none.
- Passphrase rules: at least 12 characters, and a zxcvbn score ≥ 3 in the UI with the same check on the server. The UI offers **Generate** (6-word diceware) and shows it once with a copy button.
- The passphrase is never stored or logged, and it isn't included in the export history (OC-055).

### Server flow (`internal/export/seal.go`)
1. Authorize: the project `editor` role **and** a step-up password confirmation within the last 5 minutes (`POST /auth/step-up`), otherwise `403 STEP_UP_REQUIRED`.
2. Unseal the connection secrets in memory (OC-006), build the credentials JSON, then seal it (or embed or reference it, per mode). Zero the buffers after use.
3. Rate limit: 10 exports per user per hour (the 1.0 limiter).
4. Audit event `bundle.exported` with `{projectId, mappingIds, credentialMode, runtime, bundleSha256}`, **no** passphrase and no secret.

### Plaintext guard rails
- The UI requires typing the project name to confirm, and shows "Anyone with this file can connect to shop@db.internal as migrator".
- Generated scripts start with a banner comment `# WARNING: contains plaintext database credentials`, and the runner prints the same to stderr.
- Workspace policy `exports.allowPlaintext` (default **true**, because the customer requires it; admins can switch it off) → `403 PLAINTEXT_EXPORT_DISABLED`.

### Kubernetes
- For the Kubernetes kit (OC-054), `sealed` puts the passphrase, and `plaintext` the credentials, into a `Secret` manifest that is generated **separately** from the ConfigMap holding the bundle, so GitOps users can manage it in their own secret store (for example Sealed Secrets or External Secrets). `external` refs of the form `file:` map naturally to Secret volume mounts.

## Acceptance criteria & unit tests

### AC1 — Seal & unseal round trip
**Given** a sealed bundle and the right passphrase, **then** the runner obtains the original credentials. **Given** the wrong one, **then** exit 4 `BUNDLE_PASSPHRASE_INVALID` with no timing difference between wrong-passphrase and corrupt-ciphertext cases beyond the KDF.

| Test | Location | Verifies |
|---|---|---|
| `TestSeal_AC1_RoundTrip` | `internal/export/seal_test.go` | |
| `TestSeal_AC1_WrongPassphrase` | same | |

### AC2 — Sealed block bound to its bundle
**Given** a sealed block copied into another bundle, **then** unseal fails (AAD mismatch).

| Test | Location | Verifies |
|---|---|---|
| `TestSeal_AC2_AADBinding` | `internal/export/seal_test.go` | |

### AC3 — No plaintext leak in sealed and external modes
**Given** exports in `sealed` and `external` modes, **then** the secret scanner finds no credential in the bundle, script, manifests, audit event, server logs or HTTP responses.

| Test | Location | Verifies |
|---|---|---|
| `TestExport_AC3_NoLeakScanner` | `internal/export/leak_test.go` | Scans every generated artifact and captured logs |

### AC4 — Guard rails
**Given** no step-up in the last 5 minutes, **then** `403 STEP_UP_REQUIRED`. **Given** a `viewer`, **then** `403`. **Given** plaintext with the workspace policy off, **then** `403 PLAINTEXT_EXPORT_DISABLED`. **Given** a weak passphrase, **then** `422 PASSPHRASE_WEAK`.

| Test | Location | Verifies |
|---|---|---|
| `TestExportsAPI_AC4_Authorization` | `internal/httpapi/exports_test.go` | |
| `TestExportsAPI_AC4_PassphraseStrength` | same | |

### AC5 — External refs
**Given** `password: env:PG_PASSWORD` and that env var set, **then** the runner connects. **Given** it unset, **then** exit 4 `CREDENTIAL_REF_UNRESOLVED` naming `PG_PASSWORD`.

| Test | Location | Verifies |
|---|---|---|
| `TestResolveRefs_AC5_EnvAndFile` | `internal/headless/creds_test.go` | |

### AC6 — Audit
**Given** an export, **then** one `bundle.exported` audit event is recorded with the credential mode and bundle hash and no secret material.

| Test | Location | Verifies |
|---|---|---|
| `TestExportsAPI_AC6_AuditEvent` | `internal/httpapi/exports_test.go` | |
