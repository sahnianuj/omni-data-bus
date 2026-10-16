package export

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunConfig_RoundTrip(t *testing.T) {
	src, err := NewRunConnection(Connection{Type: "postgres", Config: json.RawMessage(`{"host":"db","port":5432,"username":"u"}`)}, Secrets{"password": "s3cret"})
	require.NoError(t, err)
	raw, err := EncodeRunConfig(RunConfig{Source: src})
	require.NoError(t, err)
	require.Equal(t, `{
  "source": {
    "type": "postgres",
    "host": "db",
    "port": 5432,
    "username": "u",
    "password": "base64:czNjcmV0"
  }
}
`, string(raw), "type first, settings sorted, secrets last and encoded")
	rc, plain, err := ParseRunConfig(raw)
	require.NoError(t, err)
	require.Empty(t, plain)
	require.Nil(t, rc.Target)
	require.Equal(t, src, rc.Source)
}

// TestRunConfig_EncodesPlainSecrets: a secret without "base64:" is plain text, reported so the
// runner saves it encoded; an encoded one is decoded; an empty one is no secret (OC-156).
func TestRunConfig_EncodesPlainSecrets(t *testing.T) {
	rc, plain, err := ParseRunConfig([]byte(`{
		"source": {"type": "postgres", "host": "db.prod", "password": "n3w-pass", "caCertPem": ""},
		"target": {"type": "couchbase", "password": "base64:bjN3LXBhc3M=", "clientKeyPem": "-----BEGIN KEY-----\nabc"}
	}`))
	require.NoError(t, err)
	require.Equal(t, []string{"source.password", "target.clientKeyPem"}, plain)
	require.Equal(t, Secrets{"password": "n3w-pass"}, rc.Source.Secrets)
	require.Equal(t, Secrets{"password": "n3w-pass", "clientKeyPem": "-----BEGIN KEY-----\nabc"}, rc.Target.Secrets)

	raw, err := EncodeRunConfig(rc)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"password": "base64:bjN3LXBhc3M="`)
	require.NotContains(t, string(raw), "n3w-pass")
	again, plain, err := ParseRunConfig(raw)
	require.NoError(t, err)
	require.Empty(t, plain, "nothing left in plain text")
	require.Equal(t, rc, again)
}

func TestRunConfig_ParseErrors(t *testing.T) {
	for in, want := range map[string]string{
		`not json`:                               "not valid JSON",
		`{"sources": {}}`:                        `unknown section "sources"`,
		`{"source": "db"}`:                       "source must be an object",
		`{"source": {"type": 5}}`:                "source.type must be a string",
		`{"target": {"password": 5}}`:            "target.password must be a string",
		`{"source": {"password": "base64:***"}}`: `source.password starts with "base64:"`,
	} {
		_, _, err := ParseRunConfig([]byte(in))
		var be *Error
		require.True(t, errors.As(err, &be), in)
		require.Equal(t, CodeRunConfigInvalid, be.Code, in)
		require.Contains(t, be.Message, want, in)
		require.NotContains(t, be.Message, "***", "never echoes a secret")
	}
}
