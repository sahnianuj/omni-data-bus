# OC-156 — Passwords in `config.json` are kept in base64; plain text is encoded on the next run

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E11 Portable Runner | P1 | 2 | OC-155 | Implemented |

## User story
As an **operator** editing `config.json`, I want passwords stored base64-encoded, and to be able to type a new one in plain text, so that the file doesn't show passwords at a glance and I don't have to encode them myself.

## Background (2026-10-16)
OC-155 puts the connection secrets in `config.json`. A plain base64 string can't be told apart from a plain-text password (`abcd1234` is valid base64), so encoded values carry a `base64:` prefix.

## Scope
**In:**
- Every secret in `config.json` (`password`, `caCertPem`, `clientCertPem`, `clientKeyPem`) is written by the export as `"base64:<standard base64>"`.
- A secret without the `base64:` prefix is plain text. When `migrate.sh` runs a command that reads `config.json` (`preflight`, `run`, `resume`, `verify`), it encodes each plain value, writes the file back (same path, mode 0600, replaced atomically) and notes which values it encoded (never the values). The run then goes on with the decoded values.
- A `base64:` value that isn't valid base64 is a configuration error (exit 4) naming the field. An empty value means no secret.
- `odb run --config` does the same when used without `migrate.sh`.

**Out:** encryption. base64 only keeps passwords from being read at a glance; anyone with the file can decode them. The README says so, next to the instructions for editing the file.

## Acceptance criteria & unit tests

### AC1 — Exported encoded
**Given** a project whose source password is `s3cret`, **when** I export, **then** `config.json` has `"password": "base64:czNjcmV0"`.

### AC2 — Plain text is encoded on the next run
**Given** `config.json` with `"password": "n3w-pass"`, **when** I run `./migrate.sh run`, **then** the run uses `n3w-pass`, `config.json` now holds `"base64:bjN3LXBhc3M="` with mode 0600, and stderr says the source password was encoded.

### AC3 — Encoded values are read, bad ones refused
**Given** `"password": "base64:bjN3LXBhc3M="`, **then** the run uses `n3w-pass` and leaves the file unchanged. **Given** `"password": "base64:***"`, **then** it stops with exit 4 naming `source.password`.

| Test | Location | Verifies |
|---|---|---|
| `TestExportsAPI_ConfigFileCarriesCredentials` | `backend/internal/export/handlers_test.go` | AC1 |
| `TestRunConfig_EncodesPlainSecrets` | `backend/internal/export/runconfig_test.go` | AC2, AC3 |
| `TestRunConfig_RoundTrip`, `TestRunConfig_ParseErrors` | same | AC1, AC3 (no secret echoed in errors) |
| `TestLoadRunConfig_WritesBackEncoded` | `backend/internal/headless/runconfig_test.go` | AC2 |
| `TestRunConfig_Errors` | same | AC3 |
