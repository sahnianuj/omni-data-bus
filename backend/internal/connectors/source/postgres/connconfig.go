package postgres

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/netguard"
	"omnidatabus/internal/observability"
)

// base is a fixed, trusted DSN. Every user-supplied value is assigned field by field below, so no
// user input is ever parsed as a connection string (no parameter injection, no PG* env leakage).
const base = "postgres://omni@127.0.0.1:5432/omni?sslmode=disable"

// buildConnConfig turns config and credentials into a pgx config.
func buildConnConfig(c Config, creds connectors.Credentials) (*pgx.ConnConfig, error) {
	cc, err := pgx.ParseConfig(base)
	if err != nil {
		return nil, err
	}
	cc.Host = c.Host
	cc.Port = uint16(c.Port) //nolint:gosec // validated 1..65535
	cc.Database = c.Database
	cc.User = c.Username
	cc.Password = creds.Password
	cc.ConnectTimeout = time.Duration(c.ConnectTimeoutSec) * time.Second
	cc.Fallbacks = nil
	cc.Tracer = observability.PgxTracer{} // latency metric; spans inside traced requests (OC-031)
	// Every address is checked against the egress policy at dial time (OC-033): the lookup
	// returns only allowed IPs and the dialer re-checks, so DNS rebinding can't get through.
	guard := netguard.Default()
	cc.LookupFunc = guard.LookupStrings
	cc.DialFunc = guard.DialContext
	cc.RuntimeParams = map[string]string{
		"application_name":              c.ApplicationName,
		"default_transaction_read_only": "on",
	}
	for k, v := range c.Options {
		if _, ok := allowedOptions[k]; !ok {
			return nil, fmt.Errorf("option %q is not allowed", k)
		}
		cc.RuntimeParams[k] = v
	}
	tlsCfg, err := tlsConfig(c, creds)
	if err != nil {
		return nil, err
	}
	cc.TLSConfig = tlsCfg
	if c.SSLMode == "prefer" {
		// Try TLS first, then plain text, like libpq's sslmode=prefer.
		cc.Fallbacks = []*pgconn.FallbackConfig{{Host: cc.Host, Port: cc.Port, TLSConfig: nil}}
	}
	return cc, nil
}

func tlsConfig(c Config, creds connectors.Credentials) (*tls.Config, error) {
	if c.SSLMode == "disable" {
		return nil, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.Host}
	if creds.ClientCertPEM != "" || creds.ClientKeyPEM != "" {
		pair, err := tls.X509KeyPair([]byte(creds.ClientCertPEM), []byte(creds.ClientKeyPEM))
		if err != nil {
			return nil, errors.New("client certificate and key don't form a valid pair")
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	switch c.SSLMode {
	case "prefer", "require":
		// Encrypt without verifying the server certificate (libpq semantics).
		cfg.InsecureSkipVerify = true //nolint:gosec // explicit user choice: sslmode=require/prefer
	case "verify-ca", "verify-full":
		pool, err := rootPool(creds.CACertPEM)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
		if c.SSLMode == "verify-ca" {
			// Verify the chain against the CA but not the host name. VerifyConnection (unlike
			// VerifyPeerCertificate) also runs on resumed sessions, so resumption can't skip it.
			cfg.InsecureSkipVerify = true //nolint:gosec // chain verified in VerifyConnection
			cfg.VerifyConnection = verifyChainOnly(pool)
		}
	default:
		return nil, fmt.Errorf("unknown sslMode %q", c.SSLMode)
	}
	return cfg, nil
}

func rootPool(caPEM string) (*x509.CertPool, error) {
	if caPEM == "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			return x509.NewCertPool(), nil
		}
		return pool, nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, errors.New("CA certificate is not valid PEM")
	}
	return pool, nil
}

func verifyChainOnly(pool *x509.CertPool) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		certs := cs.PeerCertificates
		if len(certs) == 0 {
			return errors.New("server presented no certificate")
		}
		inter := x509.NewCertPool()
		for _, c := range certs[1:] {
			inter.AddCert(c)
		}
		_, err := certs[0].Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter})
		return err
	}
}

// address is host:port for dialing checks.
func address(c Config) string {
	host := c.Host
	if len(host) > 0 && host[0] != '[' && containsColon(host) {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(c.Port)
}

func containsColon(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return true
		}
	}
	return false
}
