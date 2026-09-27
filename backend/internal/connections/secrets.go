// Package connections manages a project's source and target repositories (OC-009, OC-012):
// configuration, sealed credentials, testing and the API representation.
package connections

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"omnidatabus/internal/crypto"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Secrets is the plaintext credential payload that gets sealed. It only exists in memory
// while a connection is being saved or used, and never prints or logs.
type Secrets struct {
	Password      string `json:"password,omitempty"`
	CACertPEM     string `json:"caCertPem,omitempty"`
	ClientCertPEM string `json:"clientCertPem,omitempty"`
	ClientKeyPEM  string `json:"clientKeyPem,omitempty"`
}

// String and LogValue keep secrets out of logs and fmt output.
func (Secrets) String() string { return "Secrets(***)" }

// LogValue implements slog.LogValuer.
func (Secrets) LogValue() slog.Value { return slog.StringValue("***") }

// SecretsPatch is what the API accepts. Nil means "keep the stored value".
type SecretsPatch struct {
	Password        *string `json:"password,omitempty"`
	CACertPEM       *string `json:"caCertPem,omitempty"`
	ClearCACert     bool    `json:"clearCaCert,omitempty"`
	ClientCertPEM   *string `json:"clientCertPem,omitempty"`
	ClientKeyPEM    *string `json:"clientKeyPem,omitempty"`
	ClearClientCert bool    `json:"clearClientCert,omitempty"`
}

// ErrEmptyPassword rejects `password: ""` (omit the field to keep the stored one).
var ErrEmptyPassword = problem.Problem{
	Type: "https://omni-data-bus.dev/problems/validation-failed", Title: "Invalid request", Status: http.StatusUnprocessableEntity,
	Code: problem.CodeValidation, Detail: "Password can't be empty. Omit it to keep the stored password.",
	FieldErrors: []problem.FieldError{{Field: "secrets.password", Message: "must not be empty"}},
}

// ErrInvalidCert rejects a CA certificate that isn't PEM.
var ErrInvalidCert = problem.Problem{
	Type: "https://omni-data-bus.dev/problems/validation-failed", Title: "Invalid request", Status: http.StatusUnprocessableEntity,
	Code: problem.CodeValidation, Detail: "The CA certificate must be a PEM file starting with -----BEGIN CERTIFICATE-----.",
	FieldErrors: []problem.FieldError{{Field: "secrets.caCertPem", Message: "is not a PEM certificate"}},
}

// MergeSecrets applies an API patch to the stored secrets.
func MergeSecrets(cur Secrets, p SecretsPatch) (Secrets, error) {
	out := cur
	if p.Password != nil {
		if *p.Password == "" {
			return Secrets{}, ErrEmptyPassword
		}
		out.Password = *p.Password
	}
	switch {
	case p.ClearCACert:
		out.CACertPEM = ""
	case p.CACertPEM != nil:
		if _, err := crypto.CertFingerprint(*p.CACertPEM); err != nil {
			return Secrets{}, ErrInvalidCert
		}
		out.CACertPEM = *p.CACertPEM
	}
	switch {
	case p.ClearClientCert:
		out.ClientCertPEM, out.ClientKeyPEM = "", ""
	default:
		if p.ClientCertPEM != nil {
			out.ClientCertPEM = *p.ClientCertPEM
		}
		if p.ClientKeyPEM != nil {
			out.ClientKeyPEM = *p.ClientKeyPEM
		}
	}
	return out, nil
}

// Meta derives the non-secret description stored next to the sealed payload.
func Meta(s Secrets) store.SecretMeta {
	m := store.SecretMeta{PasswordSet: s.Password != "", CACertSet: s.CACertPEM != "", ClientCertSet: s.ClientCertPEM != ""}
	if m.CACertSet {
		m.CACertFingerprint, _ = crypto.CertFingerprint(s.CACertPEM)
	}
	return m
}

// Seal encrypts secrets bound to the connection's workspace, project and ID.
func Seal(sealer crypto.Sealer, ws store.WorkspaceID, projectID, connectionID string, s Secrets) (store.Sealed, error) {
	plain, err := json.Marshal(s) //nolint:gosec // G117: this plaintext is the input to Seal and is never stored or returned
	if err != nil {
		return store.Sealed{}, err
	}
	return sealer.Seal(plain, crypto.ConnectionAAD(string(ws), projectID, connectionID))
}

// Open decrypts a connection's secrets for immediate use (test, discovery, migration).
func Open(sealer crypto.Sealer, c store.Connection) (Secrets, error) {
	if len(c.Secret.Ciphertext) == 0 {
		return Secrets{}, nil
	}
	plain, err := sealer.Open(c.Secret, crypto.ConnectionAAD(string(c.WorkspaceID), c.ProjectID, c.ID))
	if err != nil {
		return Secrets{}, errors.Join(errors.New("stored credentials could not be decrypted (was the master key changed?)"), err)
	}
	var s Secrets
	if err := json.Unmarshal(plain, &s); err != nil {
		return Secrets{}, err
	}
	return s, nil
}
