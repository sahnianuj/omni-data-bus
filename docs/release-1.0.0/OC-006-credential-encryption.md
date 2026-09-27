# OC-006 — Credential encryption & secret handling

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E1 Foundation | P0 | 5 | OC-003 | Implemented |

## User story
As a **security-conscious user**, I want the database passwords and certificates I enter to be encrypted at rest, never shown back to me, and never logged, so that a leak of the metadata bucket or a log scrape doesn't expose my production credentials.

## Implementation details

### Envelope encryption (`internal/crypto`)
- **Key encryption key (KEK):** `OC_MASTER_KEY` is a base64-encoded 32-byte key. It supports several keys for rotation: `OC_MASTER_KEYS="k2:<b64>,k1:<b64>"` with `OC_MASTER_KEY_ACTIVE=k2`. A single `OC_MASTER_KEY` is treated as ID `k1`.
- **Per-secret data key:** a random 32-byte DEK is generated per secret. The secret JSON is encrypted with the DEK using AES-256-GCM (12-byte nonce), and the DEK is then encrypted with the active KEK using AES-256-GCM.
- **Stored columns:** `secret_ciphertext` (the DEK-encrypted payload), `secret_nonce`, `secret_wrapped_dek` (the KEK-encrypted DEK plus its nonce), and `secret_key_id`.
- **AAD** = `workspace_id || project_id || connection_id || "connection-secret-v1"`. This prevents copying a ciphertext between rows, projects, or tenants.
- The credentials are sealed **once, when the project's source or target is saved**. Every later use in the project (discovery, preview, keyspace admin, every job) opens the same sealed secret server-side, and the user never re-enters it unless they choose *Change*.
- **Interface:**
  ```go
  type Sealer interface {
      Seal(plaintext []byte, aad []byte) (Sealed, error)
      Open(s Sealed, aad []byte) ([]byte, error)
  }
  type Sealed struct { KeyID string; WrappedDEK, DEKNonce, Ciphertext, Nonce []byte }
  ```
  The interface is shaped so a later `KMSSealer` (AWS KMS / GCP KMS / Vault Transit) can replace the local KEK.
- **Key rotation:** `odb admin rotate-secrets` re-wraps every DEK with the active KEK in batches of 500 inside transactions. It is idempotent: rows already on the active key are skipped.
- Plaintext buffers are zeroed after use where practical, and decrypted secrets live only for the duration of a connector call.

### Secret payloads
- PostgreSQL: `{password, sslClientKey?}`. The client cert and CA are also stored in the sealed payload, since they may be sensitive.
- Couchbase: `{password, caCertPem?}`.

### API rules
- Request DTOs accept secrets in fields such as `password`.
- Response DTOs **never** contain secrets. Instead they return `passwordSet: bool`, `caCertSet: bool`, and `caCertFingerprint: "SHA256:ab:cd…"`, so the user can confirm which certificate is stored.
- On `PATCH`: an omitted `password` keeps the stored value, `password: ""` is rejected with 422, and `clearCaCert: true` removes the CA.
- A JSON marshal guard type makes accidental serialization fail:
  ```go
  type Secret string
  func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"***"`), nil }
  func (Secret) LogValue() slog.Value          { return slog.StringValue("***") }
  func (s Secret) Reveal() string               { return string(s) }
  ```

### Frontend
- Secret inputs are write-only. On edit they show a placeholder `••••••••  (stored)` with a *Change* button that turns them into an empty input. `autocomplete="new-password"` is set.
- Certificate upload is a drag-and-drop area for `.pem` / `.crt` files, read client-side, validated to start with `-----BEGIN CERTIFICATE-----`, with the fingerprint shown after save.

## Acceptance criteria & unit tests

### AC1 — Round-trip encryption with AAD binding
**Given** a secret sealed with AAD `A`, **then** opening with `A` returns the plaintext, and opening with a different AAD, a tampered ciphertext, or a tampered wrapped DEK fails with `ErrDecrypt`.

| Test | Location | Verifies |
|---|---|---|
| `TestSealer_AC1_RoundTrip` | `internal/crypto/sealer_test.go` | |
| `TestSealer_AC1_WrongAADFails` | same | |
| `TestSealer_AC1_TamperedCiphertextFails` | same | Flip one bit |
| `TestSealer_AC1_UniqueNoncesPerSeal` | same | 10k seals → no nonce reuse |

### AC2 — Key rotation
**Given** secrets sealed with `k1`, **when** `k2` becomes active and rotation runs, **then** all rows have `secret_key_id = k2`, they still decrypt to the same plaintext, and a second run changes 0 rows. **Given** a row with an unknown key ID, **then** open fails with `ErrUnknownKey` and rotation reports it without aborting the batch.

| Test | Location | Verifies |
|---|---|---|
| `TestKeyring_AC2_ParseMultipleKeys` | `internal/crypto/keyring_test.go` | |
| `TestKeyring_AC2_RejectsBadLength` | same | A key not 32 bytes → startup error |
| `TestRotate_AC2_RewrapsAndIsIdempotent` | `internal/crypto/rotate_test.go` | In-memory repo fake |
| `TestRotate_AC2_ReportsUnknownKeyAndContinues` | same | |

### AC3 — Secrets never leave via API
**Given** a stored connection with password and CA, **when** it is fetched via `GET /connections/{id}` or listed with `GET /projects/{projectId}/connections`, **then** the JSON contains `passwordSet:true`, `caCertSet:true`, and `caCertFingerprint`, and contains neither the password string nor the PEM body.

| Test | Location | Verifies |
|---|---|---|
| `TestConnectionDTO_AC3_NoSecretFields` | `internal/connections/dto_test.go` | Reflection over the response struct: no field of type `Secret`; JSON string search for the plaintext |
| `TestFingerprint_AC3_SHA256Colon` | `internal/crypto/fingerprint_test.go` | Known PEM → expected fingerprint |

### AC4 — Secrets never logged
**Given** a `Secret` value is logged via slog or marshaled to JSON, **then** the output contains `***`.

| Test | Location | Verifies |
|---|---|---|
| `TestSecret_AC4_JSONMasked` | `internal/crypto/secret_test.go` | |
| `TestSecret_AC4_SlogMasked` | same | |
| `TestSecret_AC4_FmtVerbsMasked` | same | `%v`, `%s`, `%+v` via a `String()` method |

### AC5 — Update semantics
**Given** a PATCH without `password`, **then** the stored secret is unchanged. **Given** `password:""`, **then** a 422 is returned. **Given** `clearCaCert:true`, **then** the CA is removed and `caCertSet:false` is returned.

| Test | Location | Verifies |
|---|---|---|
| `TestMergeSecrets_AC5_OmittedKeepsExisting` | `internal/connections/secrets_test.go` | |
| `TestMergeSecrets_AC5_EmptyRejected` | same | |
| `TestMergeSecrets_AC5_ClearCA` | same | |
| `SecretInput.test.tsx` › `AC5: shows stored placeholder and change toggles input` | `src/components/SecretInput.test.tsx` | |
| `CertDropzone.test.tsx` › `AC5: rejects non-PEM file` | `src/components/CertDropzone.test.tsx` | |

## Implementation notes (as built)
- Secret flags (`passwordSet`, `caCertSet`, `caCertFingerprint`, `clientCertSet`) are computed when a connection is saved and stored as non-secret `secretMeta` on the connection document, so the API never needs to decrypt anything to describe a connection.
- `store.Sealed` fields are base64 in the Couchbase document (`[]byte` JSON encoding).
- Admin commands: `odb admin gen-key` and `odb admin rotate-secrets` (reads `OC_MASTER_KEYS` + `OC_MASTER_KEY_ACTIVE`).
