// Package crypto seals connection credentials with envelope encryption (OC-006):
// each secret gets a random 256-bit data key (DEK) used with AES-256-GCM, and the DEK is
// wrapped by the active master key (KEK). Ciphertexts are bound to their record with AAD.
package crypto

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// KeySize is the length of every key (AES-256).
const KeySize = 32

// Keyring holds the master keys by ID and which one seals new secrets.
type Keyring struct {
	keys   map[string][]byte
	active string
}

// ErrUnknownKey means a sealed secret references a key ID the keyring doesn't have.
var ErrUnknownKey = errors.New("crypto: unknown key id")

// ParseKeyring builds a keyring from configuration:
//   - single: OC_MASTER_KEY (treated as key id "k1"), or
//   - multi:  OC_MASTER_KEYS="k2:<b64>,k1:<b64>" plus OC_MASTER_KEY_ACTIVE=k2.
func ParseKeyring(single, multi, active string) (*Keyring, error) {
	kr := &Keyring{keys: map[string][]byte{}}
	if multi != "" {
		for _, part := range strings.Split(multi, ",") {
			id, b64, ok := strings.Cut(strings.TrimSpace(part), ":")
			if !ok || id == "" {
				return nil, fmt.Errorf("crypto: OC_MASTER_KEYS entry %q must be id:base64", part)
			}
			key, err := decodeKey(b64)
			if err != nil {
				return nil, fmt.Errorf("crypto: key %q: %w", id, err)
			}
			if _, dup := kr.keys[id]; dup {
				return nil, fmt.Errorf("crypto: duplicate key id %q", id)
			}
			kr.keys[id] = key
		}
		if active == "" {
			return nil, errors.New("crypto: OC_MASTER_KEY_ACTIVE is required with OC_MASTER_KEYS")
		}
		if _, ok := kr.keys[active]; !ok {
			return nil, fmt.Errorf("crypto: active key %q is not in OC_MASTER_KEYS", active)
		}
		kr.active = active
		return kr, nil
	}
	key, err := decodeKey(single)
	if err != nil {
		return nil, fmt.Errorf("crypto: OC_MASTER_KEY: %w", err)
	}
	kr.keys["k1"] = key
	kr.active = "k1"
	return kr, nil
}

func decodeKey(b64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, errors.New("not valid base64")
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("must decode to %d bytes, got %d", KeySize, len(key))
	}
	return key, nil
}

// Active returns the ID of the key that seals new secrets.
func (k *Keyring) Active() string { return k.active }

// IDs lists the key IDs, sorted.
func (k *Keyring) IDs() []string {
	ids := make([]string, 0, len(k.keys))
	for id := range k.keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (k *Keyring) key(id string) ([]byte, error) {
	key, ok := k.keys[id]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownKey, id)
	}
	return key, nil
}
