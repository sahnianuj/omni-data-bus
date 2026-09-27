package crypto

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"strings"
)

// ErrNotCertificate means the input isn't a PEM-encoded X.509 certificate.
var ErrNotCertificate = errors.New("crypto: not a PEM certificate")

// CertFingerprint returns "SHA256:ab:cd:…" of the first certificate in pemData, so the UI can
// show which CA is stored without revealing the certificate itself.
func CertFingerprint(pemData string) (string, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", ErrNotCertificate
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return "", ErrNotCertificate
	}
	sum := sha256.Sum256(block.Bytes)
	hexed := hex.EncodeToString(sum[:])
	parts := make([]string, 0, len(sum))
	for i := 0; i < len(hexed); i += 2 {
		parts = append(parts, hexed[i:i+2])
	}
	return "SHA256:" + strings.Join(parts, ":"), nil
}
