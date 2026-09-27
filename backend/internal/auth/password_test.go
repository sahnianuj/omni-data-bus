package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePassword_AC1_Rules(t *testing.T) {
	cases := map[string]struct {
		pw, email string
		wantOK    bool
		contains  string
	}{
		"too short":       {"short1!", "", false, "at least 12"},
		"common":          {"unbelievable", "", false, "too common"},
		"common any case": {"UnBelievable", "", false, "too common"},
		"is email":        {"ada@example.com", "Ada@Example.com", false, "email"},
		"too long":        {strings.Repeat("x", 257), "", false, "at most"},
		"ok":              {goodPassword, "ada@example.com", true, ""},
		"unicode counted": {"пароль-пароль", "", true, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			msg := ValidatePassword(c.pw, c.email)
			if c.wantOK {
				require.Empty(t, msg)
				return
			}
			require.Contains(t, msg, c.contains)
		})
	}
	require.Greater(t, len(commonPasswords), 9000, "embedded list loaded")
}

func TestHash_AC2_PHCFormatAndParams(t *testing.T) {
	h, err := Argon2Hasher{Params: DefaultArgon2}.Hash(goodPassword)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$"), h)
	require.Len(t, strings.Split(h, "$"), 6)
	h2, err := Argon2Hasher{Params: DefaultArgon2}.Hash(goodPassword)
	require.NoError(t, err)
	require.NotEqual(t, h, h2, "random salt")
}

func TestVerify_AC2_RightAndWrong(t *testing.T) {
	h, err := fastHasher.Hash(goodPassword)
	require.NoError(t, err)
	ok, err := fastHasher.Verify(h, goodPassword)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = fastHasher.Verify(h, goodPassword+"x")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestVerify_AC2_ConstantTimeCompareUsed(t *testing.T) {
	calls := 0
	orig := compare
	compare = func(x, y []byte) int { calls++; return orig(x, y) }
	t.Cleanup(func() { compare = orig })
	h, err := fastHasher.Hash(goodPassword)
	require.NoError(t, err)
	_, _ = fastHasher.Verify(h, "nope")
	require.Equal(t, 1, calls)
}

func TestVerify_AC2_MalformedHash(t *testing.T) {
	for _, bad := range []string{"", "$bcrypt$x", "$argon2id$v=19$m=1$salt$hash", "$argon2id$v=99$m=64,t=1,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$m=64,t=1,p=1$!!$aGFzaA"} {
		_, err := fastHasher.Verify(bad, "x")
		require.ErrorIs(t, err, ErrBadHash, bad)
	}
}

func TestTokens_AC4_RandomAndHashed(t *testing.T) {
	tok, id := NewToken()
	tok2, _ := NewToken()
	require.NotEqual(t, tok, tok2)
	require.Equal(t, HashToken(tok), id)
	require.NotContains(t, id, tok)
	require.Len(t, id, 64)
	require.NotEqual(t, NewCSRFToken(), NewCSRFToken())
}
