package export

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"

	"golang.org/x/crypto/argon2"
)

// DefaultKDF is the sealed mode's key derivation (architecture D4): argon2id, 3 passes over
// 64 MiB, one lane.
var DefaultKDF = KDF{Alg: "argon2id", T: 3, M: 64 * 1024, P: 1}

const keyLen = 32 // AES-256

// Secrets are a connection's credentials in clear text; they live only in memory.
type Secrets map[string]string

// Seal encrypts secrets under a key derived from passphrase, bound to the bundle content hash
// aad (OC-052).
func Seal(secrets Secrets, passphrase, aad string, kdf KDF) (*Credentials, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	kdf.Salt = base64.StdEncoding.EncodeToString(salt)
	key := deriveKey(passphrase, salt, kdf)
	defer zero(key)
	plain, err := json.Marshal(secrets)
	if err != nil {
		return nil, err
	}
	defer zero(plain)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, plain, []byte(aad))
	return &Credentials{
		Mode: ModeSealed, KDF: &kdf,
		Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: base64.StdEncoding.EncodeToString(ct),
	}, nil
}

// ErrPassphrase is any failure to open a sealed block: a wrong passphrase, a corrupted
// ciphertext and a block moved from another bundle all look the same.
var ErrPassphrase = &Error{Code: CodePassphraseInvalid, Message: "The passphrase doesn't open this bundle's credentials."}

// Unseal decrypts a sealed block. Every failure after the key derivation returns ErrPassphrase,
// so timing and errors don't tell a wrong passphrase from a damaged block.
func Unseal(c *Credentials, passphrase, aad string) (Secrets, error) {
	if c == nil || c.Mode != ModeSealed || c.KDF == nil {
		return nil, &Error{Code: CodeInvalid, Message: "The credentials aren't sealed."}
	}
	if c.KDF.Alg != "argon2id" || c.KDF.T == 0 || c.KDF.M == 0 || c.KDF.P == 0 || c.KDF.M > 4<<20 || c.KDF.T > 64 {
		return nil, &Error{Code: CodeInvalid, Message: "The sealed credentials use unsupported key derivation settings."}
	}
	salt, err1 := base64.StdEncoding.DecodeString(c.KDF.Salt)
	nonce, err2 := base64.StdEncoding.DecodeString(c.Nonce)
	ct, err3 := base64.StdEncoding.DecodeString(c.Ciphertext)
	key := deriveKey(passphrase, salt, *c.KDF) // always derived, even for a malformed block
	defer zero(key)
	if err := errors.Join(err1, err2, err3); err != nil {
		return nil, ErrPassphrase
	}
	gcm, err := newGCM(key)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, ErrPassphrase
	}
	plain, err := gcm.Open(nil, nonce, ct, []byte(aad))
	if err != nil {
		return nil, ErrPassphrase
	}
	defer zero(plain)
	var s Secrets
	if err := json.Unmarshal(plain, &s); err != nil {
		return nil, ErrPassphrase
	}
	return s, nil
}

func deriveKey(passphrase string, salt []byte, k KDF) []byte {
	return argon2.IDKey([]byte(passphrase), salt, k.T, k.M, k.P, keyLen)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
