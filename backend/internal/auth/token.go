package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// NewToken returns a random 32-byte session token (sent to the browser) and its SHA-256 hex
// digest (the session ID stored server-side). The raw token is never stored.
func NewToken() (token, id string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

// HashToken maps a cookie token to its session ID.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewCSRFToken returns a random per-session CSRF token.
func NewCSRFToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
