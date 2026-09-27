package transform

import (
	"crypto/sha1" //nolint:gosec // test helper
	"crypto/sha256"
	"encoding/hex"
)

func sha256Hex(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func sha1Hex(s string) string   { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) } //nolint:gosec // test helper
