// Package auth implements sign-up, sign-in, sessions, CSRF protection and role checks (OC-005).
package auth

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2Params are the argon2id cost settings (OWASP-recommended: 64 MiB, 3 iterations, 2 lanes).
type Argon2Params struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
	SaltLen   int
	KeyLen    uint32
}

// DefaultArgon2 are the production parameters.
var DefaultArgon2 = Argon2Params{MemoryKiB: 64 * 1024, Time: 3, Threads: 2, SaltLen: 16, KeyLen: 32}

// compare is swapped by tests to prove verification is constant-time.
var compare = subtle.ConstantTimeCompare

// Hasher hashes and verifies passwords.
type Hasher interface {
	Hash(password string) (string, error)
	Verify(encoded, password string) (bool, error)
}

// Argon2Hasher encodes hashes in PHC string format:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
type Argon2Hasher struct{ Params Argon2Params }

// Hash implements Hasher.
func (h Argon2Hasher) Hash(password string) (string, error) {
	p := h.Params
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, p.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Time, p.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// ErrBadHash means a stored hash can't be parsed.
var ErrBadHash = errors.New("auth: malformed password hash")

// Verify implements Hasher. It uses the parameters recorded in the hash itself.
func (Argon2Hasher) Verify(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrBadHash
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, ErrBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false, ErrBadHash
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want))) //nolint:gosec // len(want) is a small hash length
	return compare(got, want) == 1, nil
}

//go:embed common-passwords.txt
var commonPasswordsRaw string

var commonPasswords = func() map[string]struct{} {
	set := map[string]struct{}{}
	sc := bufio.NewScanner(strings.NewReader(commonPasswordsRaw))
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			set[strings.ToLower(w)] = struct{}{}
		}
	}
	return set
}()

// Password policy.
const (
	MinPasswordLen = 12
	MaxPasswordLen = 256
)

// ValidatePassword returns a user-facing message, or "" when the password is acceptable.
func ValidatePassword(password, email string) string {
	n := utf8.RuneCountInString(password)
	switch {
	case n < MinPasswordLen:
		return fmt.Sprintf("must be at least %d characters", MinPasswordLen)
	case n > MaxPasswordLen:
		return fmt.Sprintf("must be at most %d characters", MaxPasswordLen)
	}
	lower := strings.ToLower(password)
	if _, common := commonPasswords[lower]; common {
		return "is too common; choose something less predictable"
	}
	if email != "" && lower == strings.ToLower(strings.TrimSpace(email)) {
		return "must not be your email address"
	}
	return ""
}
