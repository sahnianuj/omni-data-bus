package export

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// fastKDF keeps unit tests quick; the default is checked separately.
var fastKDF = KDF{Alg: "argon2id", T: 1, M: 1024, P: 1}

func TestSeal_AC1_RoundTrip(t *testing.T) {
	in := Secrets{"password": "pg-pw", "caCertPem": "-----BEGIN CERTIFICATE-----"}
	c, err := Seal(in, passphrase, "aad", fastKDF)
	require.NoError(t, err)
	require.Equal(t, ModeSealed, c.Mode)
	require.NotContains(t, c.Ciphertext, "pg-pw")
	require.NotEmpty(t, c.KDF.Salt)
	got, err := Unseal(c, passphrase, "aad")
	require.NoError(t, err)
	require.Equal(t, in, got)

	// The default KDF is the documented one.
	require.Equal(t, KDF{Alg: "argon2id", T: 3, M: 65536, P: 1}, DefaultKDF)
	// And a full bundle opens with it.
	f := newFixture(t)
	b := f.build(t, Request{CredentialMode: ModeSealed, Passphrase: passphrase}).Bundle
	aad, err := b.ContentHash()
	require.NoError(t, err)
	require.Equal(t, b.Integrity.ContentSHA256, aad)
	src, err := Unseal(b.Source.Credentials, passphrase, aad)
	require.NoError(t, err)
	require.Equal(t, pgPassword, src["password"])
}

func TestSeal_AC1_WrongPassphrase(t *testing.T) {
	c, err := Seal(Secrets{"password": "x"}, passphrase, "aad", fastKDF)
	require.NoError(t, err)
	_, err = Unseal(c, "not-the-passphrase", "aad")
	require.ErrorIs(t, err, ErrPassphrase)

	// A corrupt ciphertext fails the same way, after the same key derivation.
	bad := *c
	bad.Ciphertext = "AAAA" + c.Ciphertext[4:]
	_, err = Unseal(&bad, passphrase, "aad")
	require.ErrorIs(t, err, ErrPassphrase)
	bad.Nonce = "!!not base64"
	_, err = Unseal(&bad, passphrase, "aad")
	require.ErrorIs(t, err, ErrPassphrase)

	// No timing difference beyond the KDF: every failure derives the key exactly once, as a
	// right passphrase does, so a wrong passphrase can't be told from a corrupt block by its time.
	// (Counted, not timed: a timing ratio of two 16 MB derivations is noise on CI runners.)
	derived := 0
	prev := deriveKey
	deriveKey = func(p string, salt []byte, k KDF) []byte { derived++; return prev(p, salt, k) }
	t.Cleanup(func() { deriveKey = prev })
	corrupt := *c
	corrupt.Ciphertext = "AAAA"
	notB64 := *c
	notB64.Ciphertext = "!!not base64"
	badNonce := *c
	badNonce.Nonce = "!!not base64"
	shortNonce := *c
	shortNonce.Nonce = "AAAA"
	badSalt, saltKDF := *c, *c.KDF
	saltKDF.Salt = "!!not base64"
	badSalt.KDF = &saltKDF
	for name, tc := range map[string]struct {
		cr   *Credentials
		pass string
	}{
		"right": {c, passphrase}, "wrong passphrase": {c, "wrong"}, "corrupt ciphertext": {&corrupt, passphrase},
		"ciphertext not base64": {&notB64, passphrase},
		"nonce not base64":      {&badNonce, passphrase}, "short nonce": {&shortNonce, passphrase}, "salt not base64": {&badSalt, passphrase},
	} {
		derived = 0
		_, _ = Unseal(tc.cr, tc.pass, "aad")
		require.Equal(t, 1, derived, name)
	}

	// Unsupported or abusive KDF settings are refused before deriving.
	huge := *c
	huge.KDF = &KDF{Alg: "argon2id", T: 3, M: 1 << 30, P: 1, Salt: c.KDF.Salt}
	derived = 0
	_, err = Unseal(&huge, passphrase, "aad")
	require.ErrorContains(t, err, CodeInvalid)
	require.Zero(t, derived, "refused before deriving")
}

func TestSeal_AC2_AADBinding(t *testing.T) {
	f := newFixture(t)
	a := f.build(t, Request{CredentialMode: ModeSealed, Passphrase: passphrase}).Bundle
	f.draft(t, f.items)
	b := f.build(t, Request{CredentialMode: ModeSealed, Passphrase: passphrase}).Bundle
	// Move a's sealed source block into b: b's content hash is a different AAD.
	b.Source.Credentials = a.Source.Credentials
	aad, err := b.ContentHash()
	require.NoError(t, err)
	_, err = Unseal(b.Source.Credentials, passphrase, aad)
	require.ErrorIs(t, err, ErrPassphrase)
	// And b's integrity no longer verifies either.
	require.Error(t, b.Verify())
}
