package connections

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"omnidatabus/internal/crypto"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"

	"github.com/stretchr/testify/require"
)

func certPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "db-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func sealer(t *testing.T) crypto.Sealer {
	t.Helper()
	kr, err := crypto.ParseKeyring(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "", "")
	require.NoError(t, err)
	return crypto.NewLocalSealer(kr)
}

func ptr(s string) *string { return &s }

func TestMergeSecrets_AC5_OmittedKeepsExisting(t *testing.T) {
	cur := Secrets{Password: "old", CACertPEM: "ca"}
	out, err := MergeSecrets(cur, SecretsPatch{})
	require.NoError(t, err)
	require.Equal(t, cur, out)
	out, err = MergeSecrets(cur, SecretsPatch{Password: ptr("new")})
	require.NoError(t, err)
	require.Equal(t, "new", out.Password)
	require.Equal(t, "ca", out.CACertPEM)
}

func TestMergeSecrets_AC5_EmptyRejected(t *testing.T) {
	_, err := MergeSecrets(Secrets{Password: "old"}, SecretsPatch{Password: ptr("")})
	p := problem.From(err)
	require.Equal(t, 422, p.Status)
	require.Equal(t, "secrets.password", p.FieldErrors[0].Field)
}

func TestMergeSecrets_AC5_ClearCA(t *testing.T) {
	out, err := MergeSecrets(Secrets{Password: "p", CACertPEM: "ca"}, SecretsPatch{ClearCACert: true})
	require.NoError(t, err)
	require.Empty(t, out.CACertPEM)
	require.False(t, Meta(out).CACertSet)
}

func TestMergeSecrets_AC5_ValidatesCACert(t *testing.T) {
	_, err := MergeSecrets(Secrets{}, SecretsPatch{CACertPEM: ptr("not a cert")})
	require.Equal(t, 422, problem.From(err).Status)
	pemCA := certPEM(t)
	out, err := MergeSecrets(Secrets{}, SecretsPatch{CACertPEM: &pemCA})
	require.NoError(t, err)
	m := Meta(out)
	require.True(t, m.CACertSet)
	require.True(t, strings.HasPrefix(m.CACertFingerprint, "SHA256:"))
}

func TestMergeSecrets_AC5_ClientCertSetAndClear(t *testing.T) {
	out, err := MergeSecrets(Secrets{}, SecretsPatch{ClientCertPEM: ptr("cert"), ClientKeyPEM: ptr("key")})
	require.NoError(t, err)
	require.True(t, Meta(out).ClientCertSet)
	out, err = MergeSecrets(out, SecretsPatch{ClearClientCert: true})
	require.NoError(t, err)
	require.Empty(t, out.ClientKeyPEM)
}

func TestSealOpen_AC1_BoundToConnection(t *testing.T) {
	s := sealer(t)
	sec := Secrets{Password: "hunter2-very-secret"}
	sealed, err := Seal(s, "ws", "p1", "p1::source", sec)
	require.NoError(t, err)
	c := store.Connection{ID: "p1::source", WorkspaceID: "ws", ProjectID: "p1", Secret: sealed}
	got, err := Open(s, c)
	require.NoError(t, err)
	require.Equal(t, sec, got)

	c.ID = "p1::target" // copied onto another connection
	_, err = Open(s, c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not be decrypted")

	empty, err := Open(s, store.Connection{})
	require.NoError(t, err)
	require.Equal(t, Secrets{}, empty)
}

func TestConnectionDTO_AC3_NoSecretFields(t *testing.T) {
	s := sealer(t)
	pemCA := certPEM(t)
	sec := Secrets{Password: "hunter2-very-secret", CACertPEM: pemCA}
	sealed, err := Seal(s, "ws", "p1", "p1::source", sec)
	require.NoError(t, err)
	c := store.Connection{
		ID: "p1::source", WorkspaceID: "ws", ProjectID: "p1", Role: store.RoleSource, Type: "postgres",
		Config: json.RawMessage(`{"host":"db"}`), Secret: sealed, SecretMeta: Meta(sec),
	}
	dto := ToDTO(c)

	// Structural guard: no field of the DTO can hold sealed data or plaintext secrets.
	typ := reflect.TypeOf(dto)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		require.NotEqual(t, reflect.TypeOf(store.Sealed{}), f.Type, f.Name)
		require.NotEqual(t, reflect.TypeOf(Secrets{}), f.Type, f.Name)
		require.NotEqual(t, reflect.TypeOf(crypto.Secret("")), f.Type, f.Name)
	}

	out, err := json.Marshal(dto)
	require.NoError(t, err)
	body := string(out)
	require.NotContains(t, body, "hunter2")
	require.NotContains(t, body, "BEGIN CERTIFICATE")
	require.NotContains(t, body, "ciphertext")
	require.Contains(t, body, `"passwordSet":true`)
	require.Contains(t, body, `"caCertSet":true`)
	require.Contains(t, body, `"caCertFingerprint":"SHA256:`)
	require.Contains(t, body, `"lastTestStatus":"untested"`)
}

func TestSecrets_AC4_NeverPrinted(t *testing.T) {
	sec := Secrets{Password: "hunter2"}
	require.NotContains(t, fmt.Sprintf("%v %s %+v", sec, sec, sec), "hunter2")
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "secrets", sec)
	require.NotContains(t, buf.String(), "hunter2")
}
