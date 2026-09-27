package couchbase

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/netguard"
)

// authProber checks credentials directly against the Query service. The SDK reports a rejected
// password during bootstrap only as a timeout ("CONNECTION_ERROR"), which is indistinguishable from
// a network problem; one authenticated HTTP request tells them apart (401 = bad credentials).
type authProber struct {
	lookupSRV func(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	do        func(req *http.Request, tlsCfg *tls.Config) (*http.Response, error)
}

func newAuthProber() authProber {
	return authProber{
		lookupSRV: net.DefaultResolver.LookupSRV,
		do: func(req *http.Request, tlsCfg *tls.Config) (*http.Response, error) {
			// The probe carries the password: it dials through the egress guard (OC-033).
			client := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{
				TLSClientConfig: tlsCfg, Proxy: http.ProxyFromEnvironment, DialContext: netguard.Default().DialContext,
			}}
			return client.Do(req)
		},
	}
}

// credentialsRejected reports true only when the Query service definitively answered 401.
func (p authProber) credentialsRejected(ctx context.Context, cfg Config, creds connectors.Credentials) bool {
	secure := strings.HasPrefix(cfg.ConnectionString, "couchbases://")
	host := p.queryNode(ctx, cfg, secure)
	if host == "" {
		return false
	}
	scheme, port := "http", "8093"
	if secure {
		scheme, port = "https", "18093"
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port), Path: "/query/service"}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(url.Values{"statement": {"SELECT 1"}}.Encode()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(cfg.Username, creds.Password)
	resp, err := p.do(req, p.tlsConfig(cfg, creds, host))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusUnauthorized
}

// queryNode picks a node to probe: the DNS SRV target for a single-host couchbases:// string
// (how Capella publishes its nodes), otherwise the first host.
func (p authProber) queryNode(ctx context.Context, cfg Config, secure bool) string {
	first := cfg.FirstHost()
	host, _, err := net.SplitHostPort(first)
	if err != nil {
		host = first
	}
	service := "couchbase"
	if secure {
		service = "couchbases"
	}
	if _, srvs, err := p.lookupSRV(ctx, service, "tcp", host); err == nil && len(srvs) > 0 {
		return strings.TrimSuffix(srvs[0].Target, ".")
	}
	return host
}

// tlsConfig always verifies the server (system roots + Capella root + the user's CA) because the
// probe sends the password; it never honors tlsVerify=false.
func (p authProber) tlsConfig(_ Config, creds connectors.Credentials, host string) *tls.Config {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	pool.AppendCertsFromPEM(capellaRootCA)
	if creds.CACertPEM != "" {
		pool.AppendCertsFromPEM([]byte(creds.CACertPEM))
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: host}
}
