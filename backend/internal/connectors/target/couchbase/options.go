package couchbase

import (
	"crypto/x509"
	"errors"
	"time"

	"github.com/couchbase/gocb/v2"

	"omnidatabus/internal/connectors"
)

// WarnTLSVerifyDisabled is reported when certificate verification is turned off.
const WarnTLSVerifyDisabled = "TLS_VERIFY_DISABLED"

// buildClusterOptions maps config + credentials to gocb options. It returns warnings to show
// in the connection test.
func buildClusterOptions(c Config, creds connectors.Credentials) (gocb.ClusterOptions, []string, error) {
	opts := gocb.ClusterOptions{
		Authenticator: gocb.PasswordAuthenticator{Username: c.Username, Password: creds.Password},
		TimeoutsConfig: gocb.TimeoutsConfig{
			ConnectTimeout: time.Duration(c.ConnectTimeoutMs) * time.Millisecond,
			KVTimeout:      time.Duration(c.KVTimeoutMs) * time.Millisecond,
		},
	}
	var warnings []string
	if c.Deployment == Capella {
		if err := opts.ApplyProfile(gocb.ClusterConfigProfileWanDevelopment); err != nil {
			return opts, nil, err
		}
		// The WAN profile raises timeouts; keep the user's connect timeout if it's longer.
		if d := time.Duration(c.ConnectTimeoutMs) * time.Millisecond; d > opts.TimeoutsConfig.ConnectTimeout {
			opts.TimeoutsConfig.ConnectTimeout = d
		}
	}
	if creds.CACertPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(creds.CACertPEM)) {
			return opts, nil, errors.New("CA certificate is not valid PEM")
		}
		opts.SecurityConfig.TLSRootCAs = pool
	}
	if !c.VerifyTLS() {
		opts.SecurityConfig.TLSSkipVerify = true
		warnings = append(warnings, WarnTLSVerifyDisabled)
	}
	return opts, warnings, nil
}
