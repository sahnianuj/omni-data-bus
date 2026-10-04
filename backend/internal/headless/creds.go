package headless

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"omnidatabus/internal/export"
)

// PassphraseEnv holds a sealed bundle's passphrase when no file is given.
const PassphraseEnv = "ODB_BUNDLE_PASSPHRASE" //nolint:gosec // G101: the variable's name, not a credential

// Unlock is where the runner finds what it needs to open credentials (OC-052).
type Unlock struct {
	// PassphraseFile is --passphrase-file (e.g. a mounted secret); tried first.
	PassphraseFile string
	// Getenv reads the environment (os.Getenv by default); PassphraseEnv is tried second.
	Getenv func(string) string
	// Prompt asks on the terminal; nil when stdin isn't one. Tried last.
	Prompt func() (string, error)
	// ReadFile reads file: references (os.ReadFile by default).
	ReadFile func(string) ([]byte, error)
}

func (u Unlock) getenv(k string) string {
	if u.Getenv != nil {
		return u.Getenv(k)
	}
	return os.Getenv(k)
}

func (u Unlock) readFile(p string) ([]byte, error) {
	if u.ReadFile != nil {
		return u.ReadFile(p)
	}
	return os.ReadFile(p) //nolint:gosec // G304: the bundle's author names the secret file
}

// passphrase finds the sealed bundle's passphrase: the file, the environment, then a prompt.
func (u Unlock) passphrase() (string, error) {
	if u.PassphraseFile != "" {
		raw, err := u.readFile(u.PassphraseFile)
		if err != nil {
			return "", &ConfigError{Code: export.CodePassphraseInvalid, Message: "Can't read the passphrase file: " + err.Error()}
		}
		return strings.TrimRight(string(raw), "\r\n"), nil
	}
	if p := u.getenv(PassphraseEnv); p != "" {
		return p, nil
	}
	if u.Prompt != nil {
		return u.Prompt()
	}
	return "", &ConfigError{Code: export.CodePassphraseInvalid, Message: "This bundle's credentials are sealed. Give the passphrase with --passphrase-file or " + PassphraseEnv + "."}
}

// Credentials opens the bundle's source and target credentials. The passphrase is asked for
// once, when either is sealed; warn reports plaintext credentials.
func Credentials(b *export.Bundle, u Unlock, warn func(string)) (src, tgt export.Secrets, err error) {
	aad := b.Integrity.ContentSHA256
	var pass *string
	open := func(role string, c *export.Credentials) (export.Secrets, error) {
		if c == nil {
			return export.Secrets{}, nil
		}
		switch c.Mode {
		case export.ModePlaintext:
			warn("WARNING: this bundle contains plaintext " + role + " credentials; anyone with the file can use them")
			return c.Values, nil
		case export.ModeExternal:
			return ResolveRefs(c.Refs, u)
		case export.ModeSealed:
			if pass == nil {
				p, err := u.passphrase()
				if err != nil {
					return nil, err
				}
				pass = &p
			}
			s, err := export.Unseal(c, *pass, aad)
			var be *export.Error
			if errors.As(err, &be) {
				return nil, &ConfigError{Code: be.Code, Message: be.Message}
			}
			return s, err
		}
		return nil, &ConfigError{Code: export.CodeInvalid, Message: "Unknown credential mode " + c.Mode}
	}
	if src, err = open("source", b.Source.Credentials); err != nil {
		return nil, nil, err
	}
	if tgt, err = open("target", b.Target.Credentials); err != nil {
		return nil, nil, err
	}
	return src, tgt, nil
}

// ResolveRefs reads external references: env:NAME or file:/path (trailing newline trimmed).
// A missing one is CREDENTIAL_REF_UNRESOLVED naming the variable or file, never a value.
func ResolveRefs(refs map[string]string, u Unlock) (export.Secrets, error) {
	out := export.Secrets{}
	for k, ref := range refs {
		kind, name, _ := strings.Cut(ref, ":")
		switch kind {
		case "env":
			v := u.getenv(name)
			if v == "" {
				return nil, &ConfigError{Code: export.CodeRefUnresolved, Message: fmt.Sprintf("The %s comes from the environment variable %s, which isn't set.", k, name)}
			}
			out[k] = v
		case "file":
			raw, err := u.readFile(name)
			if err != nil {
				return nil, &ConfigError{Code: export.CodeRefUnresolved, Message: fmt.Sprintf("The %s comes from the file %s, which can't be read.", k, name)}
			}
			out[k] = strings.TrimRight(string(raw), "\r\n")
		default:
			return nil, &ConfigError{Code: export.CodeRefUnresolved, Message: fmt.Sprintf("The %s reference %q isn't env:NAME or file:/path.", k, ref)}
		}
	}
	return out, nil
}
