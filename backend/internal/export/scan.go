package export

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strings"
)

var (
	privateKey = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	// secretMember is a password, token or secret JSON member; its value is checked in code.
	secretMember = regexp.MustCompile(`(?i)"(password|token|secret|apiKey|clientKeyPem)"\s*:\s*"([^"]*)"`)
)

// ScanForSecrets is the secret scanner shared by the export tests (OC-050 AC3, OC-052 AC3,
// OC-053): it reports each known secret found in data (as is, or base64-encoded), a private
// key, and a password, token or secret member with a value. It returns descriptions, never the
// secrets themselves.
func ScanForSecrets(data []byte, known ...string) []string {
	var found []string
	for i, s := range known {
		if s == "" {
			continue
		}
		for _, form := range []string{s, base64.StdEncoding.EncodeToString([]byte(s))} {
			if bytes.Contains(data, []byte(form)) {
				found = append(found, "known secret #"+string(rune('1'+i)))
				break
			}
		}
	}
	if privateKey.Match(data) {
		found = append(found, "a private key")
	}
	for _, m := range secretMember.FindAllSubmatch(data, -1) {
		// Masked values and external references ("env:PG_PASSWORD") aren't secrets.
		v := string(m[2])
		if v != "" && !strings.HasPrefix(v, "***") && !strings.HasPrefix(v, "env:") && !strings.HasPrefix(v, "file:") {
			found = append(found, "a "+string(m[1])+" member")
			break
		}
	}
	return found
}
