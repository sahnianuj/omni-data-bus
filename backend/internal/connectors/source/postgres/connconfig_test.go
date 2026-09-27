package postgres

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
)

func selfSigned(t *testing.T) (certPEM, keyPEM string, der []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "db-ca"}, IsCA: true, BasicConstraintsValid: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	kb, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})), der
}

func mustConfig(t *testing.T, over map[string]any) Config {
	t.Helper()
	c, err := ParseConfig(cfgJSON(over))
	require.NoError(t, err)
	return c
}

func TestBuildConnConfig_AC3_FieldsCopied(t *testing.T) {
	cc, err := buildConnConfig(mustConfig(t, map[string]any{"port": 6543, "sslMode": "disable"}), connectors.Credentials{Password: "s3cr@t"})
	require.NoError(t, err)
	require.Equal(t, "db.example.com", cc.Host)
	require.EqualValues(t, 6543, cc.Port)
	require.Equal(t, "shop", cc.Database)
	require.Equal(t, "omni_ro", cc.User)
	require.Equal(t, "s3cr@t", cc.Password)
	require.Equal(t, 10*time.Second, cc.ConnectTimeout)
	require.Empty(t, cc.Fallbacks)
}

func TestBuildConnConfig_AC3_TLSModes(t *testing.T) {
	ca, _, _ := selfSigned(t)
	creds := connectors.Credentials{Password: "p", CACertPEM: ca}
	for _, mode := range []string{"disable", "prefer", "require", "verify-ca", "verify-full"} {
		t.Run(mode, func(t *testing.T) {
			cc, err := buildConnConfig(mustConfig(t, map[string]any{"sslMode": mode}), creds)
			require.NoError(t, err)
			switch mode {
			case "disable":
				require.Nil(t, cc.TLSConfig)
			case "prefer":
				require.True(t, cc.TLSConfig.InsecureSkipVerify)
				require.Len(t, cc.Fallbacks, 1, "falls back to plain text")
				require.Nil(t, cc.Fallbacks[0].TLSConfig)
			case "require":
				require.True(t, cc.TLSConfig.InsecureSkipVerify)
				require.Nil(t, cc.TLSConfig.RootCAs)
			case "verify-ca":
				require.NotNil(t, cc.TLSConfig.RootCAs)
				require.True(t, cc.TLSConfig.InsecureSkipVerify, "host name not checked")
				require.NotNil(t, cc.TLSConfig.VerifyConnection, "chain still verified, including resumed sessions")
				require.Nil(t, cc.TLSConfig.VerifyPeerCertificate)
			case "verify-full":
				require.NotNil(t, cc.TLSConfig.RootCAs)
				require.False(t, cc.TLSConfig.InsecureSkipVerify)
				require.Equal(t, "db.example.com", cc.TLSConfig.ServerName)
			}
		})
	}
}

func TestVerifyChainOnly_AC3(t *testing.T) {
	ca, _, der := selfSigned(t)
	pool, err := rootPool(ca)
	require.NoError(t, err)
	parse := func(der []byte) tls.ConnectionState {
		c, err := x509.ParseCertificate(der)
		require.NoError(t, err)
		return tls.ConnectionState{PeerCertificates: []*x509.Certificate{c}, DidResume: true}
	}
	require.NoError(t, verifyChainOnly(pool)(parse(der)), "trusted CA passes without a host name check (also on resumed sessions)")
	_, _, other := selfSigned(t)
	require.Error(t, verifyChainOnly(pool)(parse(other)), "untrusted certificate fails")
	require.Error(t, verifyChainOnly(pool)(tls.ConnectionState{}))
	_, err = rootPool("not pem")
	require.Error(t, err)
}

func TestBuildConnConfig_AC3_ClientCert(t *testing.T) {
	cert, key, _ := selfSigned(t)
	cc, err := buildConnConfig(mustConfig(t, map[string]any{"sslMode": "require"}), connectors.Credentials{ClientCertPEM: cert, ClientKeyPEM: key})
	require.NoError(t, err)
	require.Len(t, cc.TLSConfig.Certificates, 1)
	_, err = buildConnConfig(mustConfig(t, map[string]any{"sslMode": "require"}), connectors.Credentials{ClientCertPEM: cert, ClientKeyPEM: "junk"})
	require.ErrorContains(t, err, "valid pair")
}

func TestBuildConnConfig_AC3_RejectsNonAllowlistedOption(t *testing.T) {
	c := mustConfig(t, nil)
	c.Options = map[string]string{"host": "evil"} // bypassing ParseConfig must still fail
	_, err := buildConnConfig(c, connectors.Credentials{})
	require.ErrorContains(t, err, "not allowed")
}

func TestBuildConnConfig_AC3_ReadOnlyDefault(t *testing.T) {
	cc, err := buildConnConfig(mustConfig(t, map[string]any{"options": map[string]string{"statement_timeout": "30s"}}), connectors.Credentials{})
	require.NoError(t, err)
	require.Equal(t, "on", cc.RuntimeParams["default_transaction_read_only"])
	require.Equal(t, "omni-data-bus", cc.RuntimeParams["application_name"])
	require.Equal(t, "30s", cc.RuntimeParams["statement_timeout"])
}

func TestAddress_IPv6(t *testing.T) {
	require.Equal(t, "[::1]:5432", address(Config{Host: "::1", Port: 5432}))
	require.Equal(t, "db:5432", address(Config{Host: "db", Port: 5432}))
}
