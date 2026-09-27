package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"omnidatabus/internal/store"
)

// ErrDecrypt means a secret could not be opened: wrong AAD, tampering, or wrong key.
var ErrDecrypt = errors.New("crypto: decryption failed")

// Sealer encrypts and decrypts secrets. A KMS-backed implementation can replace LocalSealer.
type Sealer interface {
	Seal(plaintext, aad []byte) (store.Sealed, error)
	Open(s store.Sealed, aad []byte) ([]byte, error)
}

// LocalSealer wraps data keys with master keys held in process memory.
type LocalSealer struct{ kr *Keyring }

// NewLocalSealer returns a Sealer over kr.
func NewLocalSealer(kr *Keyring) *LocalSealer { return &LocalSealer{kr: kr} }

// ConnectionAAD binds a sealed secret to one connection of one project in one workspace,
// so a ciphertext copied to another record fails to open.
func ConnectionAAD(workspaceID, projectID, connectionID string) []byte {
	return []byte(workspaceID + "\x00" + projectID + "\x00" + connectionID + "\x00connection-secret-v1")
}

func dekAAD(aad []byte, keyID string) []byte {
	return append(append([]byte("dek\x00"+keyID+"\x00"), aad...), 0)
}

// Seal encrypts plaintext with a fresh DEK and wraps the DEK with the active master key.
func (s *LocalSealer) Seal(plaintext, aad []byte) (store.Sealed, error) {
	dek := make([]byte, KeySize)
	if _, err := rand.Read(dek); err != nil {
		return store.Sealed{}, err
	}
	defer zero(dek)
	ct, nonce, err := gcmSeal(dek, plaintext, aad)
	if err != nil {
		return store.Sealed{}, err
	}
	sealed := store.Sealed{Ciphertext: ct, Nonce: nonce}
	if err := s.wrap(&sealed, dek, aad, s.kr.Active()); err != nil {
		return store.Sealed{}, err
	}
	return sealed, nil
}

// Open unwraps the DEK and decrypts. Any mismatch returns ErrDecrypt (or ErrUnknownKey).
func (s *LocalSealer) Open(sealed store.Sealed, aad []byte) ([]byte, error) {
	dek, err := s.unwrap(sealed, aad)
	if err != nil {
		return nil, err
	}
	defer zero(dek)
	return gcmOpen(dek, sealed.Ciphertext, sealed.Nonce, aad)
}

// Rewrap re-wraps the DEK with the active key; the ciphertext is untouched.
// It reports false when the secret already uses the active key.
func (s *LocalSealer) Rewrap(sealed store.Sealed, aad []byte) (store.Sealed, bool, error) {
	if sealed.KeyID == s.kr.Active() {
		return sealed, false, nil
	}
	dek, err := s.unwrap(sealed, aad)
	if err != nil {
		return store.Sealed{}, false, err
	}
	defer zero(dek)
	out := sealed
	if err := s.wrap(&out, dek, aad, s.kr.Active()); err != nil {
		return store.Sealed{}, false, err
	}
	return out, true, nil
}

func (s *LocalSealer) wrap(sealed *store.Sealed, dek, aad []byte, keyID string) error {
	kek, err := s.kr.key(keyID)
	if err != nil {
		return err
	}
	wrapped, nonce, err := gcmSeal(kek, dek, dekAAD(aad, keyID))
	if err != nil {
		return err
	}
	sealed.KeyID, sealed.WrappedDEK, sealed.DEKNonce = keyID, wrapped, nonce
	return nil
}

func (s *LocalSealer) unwrap(sealed store.Sealed, aad []byte) ([]byte, error) {
	kek, err := s.kr.key(sealed.KeyID)
	if err != nil {
		return nil, err
	}
	return gcmOpen(kek, sealed.WrappedDEK, sealed.DEKNonce, dekAAD(aad, sealed.KeyID))
}

func gcmSeal(key, plaintext, aad []byte) (ct, nonce []byte, err error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return aead.Seal(nil, nonce, plaintext, aad), nonce, nil
}

func gcmOpen(key, ct, nonce, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, ErrDecrypt
	}
	pt, err := aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: %w", err)
	}
	return cipher.NewGCM(block)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
