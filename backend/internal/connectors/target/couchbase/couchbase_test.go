package couchbase

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/problem"
)

func cfgJSON(over map[string]any) json.RawMessage {
	base := map[string]any{"connectionString": "couchbase://cb1.example.com,cb2.example.com:11210", "username": "migrator"}
	for k, v := range over {
		if v == nil {
			delete(base, k)
		} else {
			base[k] = v
		}
	}
	b, _ := json.Marshal(base)
	return b
}

func TestCouchbaseConfig_AC1_Validation(t *testing.T) {
	cases := map[string]struct {
		over  map[string]any
		field string
	}{
		"http scheme":                {map[string]any{"connectionString": "http://host"}, "config.connectionString"},
		"empty username":             {map[string]any{"username": ""}, "config.username"},
		"capella needs couchbases":   {map[string]any{"deployment": "capella", "connectionString": "couchbase://cb.abc.cloud.couchbase.com"}, "config.connectionString"},
		"no hosts":                   {map[string]any{"connectionString": "couchbase://"}, "config.connectionString"},
		"bad deployment":             {map[string]any{"deployment": "moon"}, "config.deployment"},
		"host with credentials":      {map[string]any{"connectionString": "couchbase://user:pw@host"}, "config.connectionString"},
		"capella ok":                 {map[string]any{"deployment": "capella", "connectionString": "couchbases://cb.abc.cloud.couchbase.com"}, ""},
		"self-managed multi-host ok": {nil, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseConfig(cfgJSON(c.over))
			if c.field == "" {
				require.NoError(t, err)
				return
			}
			p := problem.From(err)
			require.Equal(t, 422, p.Status, err)
			require.Equal(t, c.field, p.FieldErrors[0].Field)
		})
	}
}

func TestParseConnString_AC1_HostsPortsParams(t *testing.T) {
	c, err := ParseConfig(cfgJSON(map[string]any{"connectionString": " couchbases://h1:11207,h2/?network=external&kv_timeout=1&bogus=x "}))
	require.NoError(t, err)
	require.Equal(t, "couchbases://h1:11207,h2?network=external", c.ConnectionString, "only allow-listed params kept")
	require.Equal(t, "h1:11207", c.FirstHost())
	require.Equal(t, SelfManaged, c.Deployment)
	require.Equal(t, 2500, c.KVTimeoutMs)
	require.Equal(t, 10000, c.ConnectTimeoutMs)
	require.True(t, c.VerifyTLS())
}

func caPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cb-ca"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func mustCfg(t *testing.T, over map[string]any) Config {
	t.Helper()
	c, err := ParseConfig(cfgJSON(over))
	require.NoError(t, err)
	return c
}

func TestBuildClusterOptions_AC2_Capella(t *testing.T) {
	c := mustCfg(t, map[string]any{"deployment": "capella", "connectionString": "couchbases://cb.abc.cloud.couchbase.com", "tlsVerify": false})
	opts, warnings, err := buildClusterOptions(c, connectors.Credentials{Password: "pw"})
	require.NoError(t, err)
	require.False(t, opts.SecurityConfig.TLSSkipVerify, "Capella always verifies TLS, even if asked not to")
	require.Empty(t, warnings)
	require.GreaterOrEqual(t, opts.TimeoutsConfig.ConnectTimeout, 10*time.Second, "WAN profile applied")
	auth := opts.Authenticator.(gocb.PasswordAuthenticator)
	require.Equal(t, "migrator", auth.Username)
	require.Equal(t, "pw", auth.Password)
}

func TestBuildClusterOptions_AC2_CustomCA(t *testing.T) {
	opts, _, err := buildClusterOptions(mustCfg(t, nil), connectors.Credentials{CACertPEM: caPEM(t)})
	require.NoError(t, err)
	require.NotNil(t, opts.SecurityConfig.TLSRootCAs)
	_, _, err = buildClusterOptions(mustCfg(t, nil), connectors.Credentials{CACertPEM: "junk"})
	require.ErrorContains(t, err, "not valid PEM")
}

func TestBuildClusterOptions_AC2_SkipVerifyWarns(t *testing.T) {
	opts, warnings, err := buildClusterOptions(mustCfg(t, map[string]any{"tlsVerify": false}), connectors.Credentials{})
	require.NoError(t, err)
	require.True(t, opts.SecurityConfig.TLSSkipVerify)
	require.Equal(t, []string{WarnTLSVerifyDisabled}, warnings)
}

// --- tester with a fake cluster ---

type fakeCluster struct {
	readyErr   error
	services   map[string]ServiceReport
	servErr    error
	version    string
	versionErr error
	roles      []Role
	rolesErr   error
	nodes      []string
	closed     bool
}

func (f *fakeCluster) WaitReady(context.Context, time.Duration) error { return f.readyErr }
func (f *fakeCluster) Services(context.Context) (map[string]ServiceReport, error) {
	return f.services, f.servErr
}
func (f *fakeCluster) Version(context.Context) (string, error) { return f.version, f.versionErr }
func (f *fakeCluster) MyRoles(context.Context) ([]Role, error) { return f.roles, f.rolesErr }
func (f *fakeCluster) Close() error                            { f.closed = true; return nil }
func (f *fakeCluster) Nodes(context.Context) ([]string, error) { return f.nodes, nil }

func healthy() *fakeCluster {
	return &fakeCluster{
		services: map[string]ServiceReport{"kv": {OK: true}, "query": {OK: true, Latency: 11}},
		version:  "8.0.3-1234-enterprise",
		roles:    []Role{{Role: "admin"}},
	}
}

func testWith(fc *fakeCluster, connectErr error) *Connector {
	return &Connector{
		connect: func(string, gocb.ClusterOptions) (cluster, error) {
			if connectErr != nil {
				return nil, connectErr
			}
			return fc, nil
		},
		now:  time.Now,
		pool: NewPool(time.Minute, time.Now),
	}
}

func steps(res connectors.TestResult) map[string]connectors.StepStatus {
	out := map[string]connectors.StepStatus{}
	for _, s := range res.Steps {
		out[s.ID] = s.Status
	}
	return out
}

func caps(t *testing.T, res connectors.TestResult) Capabilities {
	t.Helper()
	var c Capabilities
	require.NoError(t, json.Unmarshal(res.Capabilities, &c))
	return c
}

func TestTester_AC3_AllOK(t *testing.T) {
	fc := healthy()
	res := testWith(fc, nil).Test(context.Background(), cfgJSON(nil), connectors.Credentials{Password: "pw"})
	require.True(t, res.OK)
	require.Equal(t, map[string]connectors.StepStatus{"parse": "ok", "bootstrap": "ok", "services": "ok", "version": "ok", "permissions": "ok"}, steps(res))
	c := caps(t, res)
	require.True(t, c.CanCreateBucket)
	require.True(t, c.RangeScan)
	require.True(t, c.QueryService)
	require.True(t, fc.closed)
}

func TestTester_AC3_AuthFailure(t *testing.T) {
	fc := healthy()
	fc.readyErr = fmt.Errorf("bootstrap: %w", gocb.ErrAuthenticationFailure)
	res := testWith(fc, nil).Test(context.Background(), cfgJSON(nil), connectors.Credentials{Password: "bad"})
	require.False(t, res.OK)
	require.Equal(t, map[string]connectors.StepStatus{"parse": "ok", "bootstrap": "fail", "services": "skipped", "version": "skipped", "permissions": "skipped"}, steps(res))
	require.Equal(t, "TARGET_AUTH_FAILED", res.Steps[1].Code)
}

func TestTester_AC3_CapellaTimeoutHintsAllowedIPs(t *testing.T) {
	fc := healthy()
	fc.readyErr = gocb.ErrUnambiguousTimeout
	res := testWith(fc, nil).Test(context.Background(), cfgJSON(map[string]any{"deployment": "capella", "connectionString": "couchbases://cb.x.cloud.couchbase.com"}), connectors.Credentials{})
	require.Equal(t, "TARGET_UNREACHABLE", res.Steps[1].Code)
	require.Contains(t, res.Steps[1].Hint, "Allowed IP addresses")
}

func TestTester_AC3_QueryMissingWarn(t *testing.T) {
	fc := healthy()
	fc.services = map[string]ServiceReport{"kv": {OK: true}}
	res := testWith(fc, nil).Test(context.Background(), cfgJSON(nil), connectors.Credentials{})
	require.True(t, res.OK)
	require.Equal(t, connectors.StepWarn, steps(res)["services"])
	require.False(t, caps(t, res).QueryService)
}

func TestTester_AC3_KVMissingFails(t *testing.T) {
	fc := healthy()
	fc.services = map[string]ServiceReport{"query": {OK: true}}
	res := testWith(fc, nil).Test(context.Background(), cfgJSON(nil), connectors.Credentials{})
	require.False(t, res.OK)
	require.Equal(t, connectors.StepFail, steps(res)["services"])
}

func TestTester_AC3_OldVersionFails(t *testing.T) {
	fc := healthy()
	fc.version = "6.6.5-10080-enterprise"
	res := testWith(fc, nil).Test(context.Background(), cfgJSON(nil), connectors.Credentials{})
	require.False(t, res.OK)
	require.Equal(t, CodeVersionUnsupported, res.Steps[3].Code)
	fc = healthy()
	fc.version = "7.2.4-7070-enterprise"
	res = testWith(fc, nil).Test(context.Background(), cfgJSON(nil), connectors.Credentials{})
	require.True(t, res.OK)
	require.Equal(t, connectors.StepWarn, steps(res)["version"], "7.2 works but lacks range-scan sampling")
	require.False(t, caps(t, res).RangeScan)
}

func TestTester_AC3_InvalidConfigAndConnectError(t *testing.T) {
	res := testWith(healthy(), nil).Test(context.Background(), cfgJSON(map[string]any{"connectionString": "http://x"}), connectors.Credentials{})
	require.False(t, res.OK)
	require.Len(t, res.Steps, 1)
	res = testWith(nil, fmt.Errorf("connect: %w", gocb.ErrInvalidArgument)).Test(context.Background(), cfgJSON(nil), connectors.Credentials{})
	require.Equal(t, connectors.StepFail, steps(res)["bootstrap"])
}

func TestTester_AC3_NoWriteAccessWarns(t *testing.T) {
	fc := healthy()
	fc.roles = []Role{{Role: "data_reader", Bucket: "shop"}}
	res := testWith(fc, nil).Test(context.Background(), cfgJSON(nil), connectors.Credentials{})
	require.True(t, res.OK)
	require.Equal(t, connectors.StepWarn, steps(res)["permissions"])
	require.Contains(t, res.Steps[4].Detail, "can't write documents")
}

func TestClassifyGocbError_AC3_Table(t *testing.T) {
	cases := []struct {
		err  error
		kind connectors.Kind
	}{
		{gocb.ErrAuthenticationFailure, connectors.KindAuth},
		{gocb.ErrUnambiguousTimeout, connectors.KindUnreachable},
		{gocb.ErrTimeout, connectors.KindUnreachable},
		{context.DeadlineExceeded, connectors.KindUnreachable},
		{x509.UnknownAuthorityError{}, connectors.KindTLS},
		{errors.New("weird"), connectors.KindUnknown},
	}
	for _, c := range cases {
		require.Equal(t, c.kind, classify(c.err).Kind, c.err.Error())
	}
}

func TestCapabilitiesFromRoles_AC4_BucketAdmin(t *testing.T) {
	c := CapabilitiesFromRoles([]Role{
		{Role: "bucket_admin", Bucket: "app"},
		{Role: "data_writer", Bucket: "app", Scope: "*", Collection: "*"},
	}, SelfManaged)
	require.False(t, c.CanCreateBucket)
	require.True(t, c.CanManageScopes)
	require.True(t, c.CanWriteDocs)
	require.Equal(t, []string{"app"}, c.BucketsAllowed)
	require.False(t, c.AllBuckets)
}

func TestCapabilitiesFromRoles_AC4_FullAdmin(t *testing.T) {
	c := CapabilitiesFromRoles([]Role{{Role: "admin"}}, SelfManaged)
	require.True(t, c.CanCreateBucket && c.CanManageScopes && c.CanWriteDocs && c.CanListBuckets && c.AllBuckets)
}

func TestCapabilities_AC4_CapellaNoBucketCreate(t *testing.T) {
	c := CapabilitiesFromRoles([]Role{{Role: "admin"}, {Role: "bucket_full_access", Bucket: "*"}}, Capella)
	require.False(t, c.CanCreateBucket)
	require.Contains(t, c.Reasons, ReasonCapellaBuckets)
	require.True(t, c.AllBuckets)
}

func TestParseVersion(t *testing.T) {
	ma, mi := parseVersion("7.6.2-3721-enterprise")
	require.Equal(t, [2]int{7, 6}, [2]int{ma, mi})
	ma, _ = parseVersion("garbage")
	require.Zero(t, ma)
}

// --- pool ---

type fakeCloser struct{ closed bool }

func (f *fakeCloser) Close() error { f.closed = true; return nil }

func TestSessionPool_AC5_Reuses(t *testing.T) {
	now := time.Unix(0, 0)
	p := NewPool(10*time.Minute, func() time.Time { return now })
	opens := 0
	open := func() (Closer, error) { opens++; return &fakeCloser{}, nil }
	a, err := p.Get("conn-1", "v1", open)
	require.NoError(t, err)
	b, err := p.Get("conn-1", "v1", open)
	require.NoError(t, err)
	require.Same(t, a, b)
	require.Equal(t, 1, opens)
}

func TestSessionPool_AC5_InvalidatesOnConfigChange(t *testing.T) {
	now := time.Unix(0, 0)
	p := NewPool(10*time.Minute, func() time.Time { return now })
	first := &fakeCloser{}
	_, _ = p.Get("conn-1", "v1", func() (Closer, error) { return first, nil })
	second, _ := p.Get("conn-1", "v2", func() (Closer, error) { return &fakeCloser{}, nil })
	require.True(t, first.closed, "the edited connection's old cluster is closed")
	require.NotSame(t, first, second)
	require.Equal(t, 1, p.Len())
}

func TestSessionPool_AC5_IdleEviction(t *testing.T) {
	now := time.Unix(0, 0)
	p := NewPool(10*time.Minute, func() time.Time { return now })
	c := &fakeCloser{}
	_, _ = p.Get("conn-1", "v1", func() (Closer, error) { return c, nil })
	now = now.Add(11 * time.Minute)
	p.Evict()
	require.True(t, c.closed)
	require.Zero(t, p.Len())
	_, err := p.Get("x", "v", func() (Closer, error) { return nil, errors.New("down") })
	require.Error(t, err)
	d := &fakeCloser{}
	_, _ = p.Get("d", "v", func() (Closer, error) { return d, nil })
	p.CloseAll()
	require.True(t, d.closed)
}

func TestConnector_DescriptorNormalizeClose(t *testing.T) {
	c := New()
	require.Equal(t, "couchbase", c.Descriptor().Type)
	require.NoError(t, c.Validate(cfgJSON(nil)))
	norm, name, err := c.Normalize(cfgJSON(nil))
	require.NoError(t, err)
	require.Equal(t, "cb1.example.com", name)
	require.Contains(t, string(norm), `"deployment":"self-managed"`)
	require.NoError(t, c.Close(context.Background()))
}

func probeReturning(status int, srvTarget string, seen *string) authProber {
	return authProber{
		lookupSRV: func(context.Context, string, string, string) (string, []*net.SRV, error) {
			if srvTarget == "" {
				return "", nil, errors.New("no srv")
			}
			return "", []*net.SRV{{Target: srvTarget + "."}}, nil
		},
		do: func(req *http.Request, tlsCfg *tls.Config) (*http.Response, error) {
			*seen = req.URL.String() + " tlsVerify=" + fmt.Sprint(!tlsCfg.InsecureSkipVerify)
			u, p, _ := req.BasicAuth()
			if u == "" || p == "" {
				return nil, errors.New("no auth sent")
			}
			return &http.Response{StatusCode: status, Body: http.NoBody}, nil
		},
	}
}

func TestTester_AC3_TimeoutWithRejectedCredentialsIsAuthFailed(t *testing.T) {
	fc := healthy()
	fc.readyErr = gocb.ErrUnambiguousTimeout // what the SDK reports for a wrong password
	var seen string
	c := testWith(fc, nil)
	c.probe = probeReturning(http.StatusUnauthorized, "svc-node-1.cb.x.cloud.couchbase.com", &seen)
	res := c.Test(context.Background(), cfgJSON(map[string]any{"deployment": "capella", "connectionString": "couchbases://cb.x.cloud.couchbase.com"}), connectors.Credentials{Password: "wrong"})
	require.Equal(t, "TARGET_AUTH_FAILED", res.Steps[1].Code)
	require.Equal(t, "https://svc-node-1.cb.x.cloud.couchbase.com:18093/query/service tlsVerify=true", seen, "probes the SRV node over verified TLS")
}

func TestTester_AC3_TimeoutWithoutRejectionStaysUnreachable(t *testing.T) {
	fc := healthy()
	fc.readyErr = gocb.ErrUnambiguousTimeout
	var seen string
	c := testWith(fc, nil)
	c.probe = probeReturning(http.StatusOK, "", &seen)
	res := c.Test(context.Background(), cfgJSON(nil), connectors.Credentials{Password: "pw"})
	require.Equal(t, "TARGET_UNREACHABLE", res.Steps[1].Code)
	require.Equal(t, "http://cb1.example.com:8093/query/service tlsVerify=true", seen, "self-managed couchbase:// uses the first host")
}

func TestServiceName(t *testing.T) {
	require.Equal(t, "query", serviceName(gocb.ServiceTypeQuery))
	require.Equal(t, "management", serviceName(gocb.ServiceTypeManagement))
	require.Equal(t, "kv", serviceName(gocb.ServiceTypeKeyValue))
	require.NotEmpty(t, serviceName(gocb.ServiceTypeSearch))
}

func TestAuthProbe_TLSAlwaysVerifies(t *testing.T) {
	p := newAuthProber()
	cfg := mustCfg(t, map[string]any{"tlsVerify": false})
	tc := p.tlsConfig(cfg, connectors.Credentials{CACertPEM: caPEM(t)}, "node1")
	require.False(t, tc.InsecureSkipVerify, "the probe sends a password, so it never skips verification")
	require.Equal(t, "node1", tc.ServerName)
	require.NotNil(t, tc.RootCAs)
}

func TestAuthProbe_QueryNode(t *testing.T) {
	p := authProber{lookupSRV: func(context.Context, string, string, string) (string, []*net.SRV, error) {
		return "", nil, errors.New("none")
	}}
	require.Equal(t, "cb1.example.com", p.queryNode(context.Background(), mustCfg(t, map[string]any{"connectionString": "couchbase://cb1.example.com:11210"}), false))
	var service string
	p.lookupSRV = func(_ context.Context, svc, _, _ string) (string, []*net.SRV, error) {
		service = svc
		return "", []*net.SRV{{Target: "n1.cloud."}}, nil
	}
	require.Equal(t, "n1.cloud", p.queryNode(context.Background(), mustCfg(t, map[string]any{"connectionString": "couchbases://cb.x"}), true))
	require.Equal(t, "couchbases", service)
}

func TestAuthProbe_TransportErrorIsNotRejection(t *testing.T) {
	var seen string
	p := probeReturning(http.StatusUnauthorized, "", &seen)
	p.do = func(*http.Request, *tls.Config) (*http.Response, error) { return nil, errors.New("dial timeout") }
	require.False(t, p.credentialsRejected(context.Background(), mustCfg(t, nil), connectors.Credentials{Password: "x"}))
}

func TestRoleSummary(t *testing.T) {
	roles := []Role{{Role: "admin"}, {Role: "data_writer", Bucket: "b"}, {Role: "data_writer", Bucket: "b"}}
	require.Equal(t, "admin, data_writer[b]", roleSummary(roles))
	var many []Role
	for i := 0; i < 8; i++ {
		many = append(many, Role{Role: "data_reader", Bucket: fmt.Sprint(i)})
	}
	require.Contains(t, roleSummary(many), "+2 more")
}

// --- OC-190: an unreachable cluster is reported within ReachTimeout, in plain words ---

type waitRecorder struct {
	*fakeCluster
	timeout time.Duration
}

func (w *waitRecorder) WaitReady(_ context.Context, d time.Duration) error {
	w.timeout = d
	return w.readyErr
}

func TestCouchbaseTest_OC190_AC1_UnreachableFast(t *testing.T) {
	c := testWith(nil, nil)
	c.connect = func(string, gocb.ClusterOptions) (cluster, error) {
		panic("the SDK must not be called for an unreachable cluster")
	}
	var dialed []string
	var mu sync.Mutex
	c.dial = func(_ context.Context, _, addr string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, addr)
		mu.Unlock()
		return nil, context.DeadlineExceeded
	}
	res := c.Test(context.Background(), cfgJSON(nil), connectors.Credentials{})
	require.False(t, res.OK)
	require.Equal(t, map[string]connectors.StepStatus{"parse": "ok", "bootstrap": "fail", "services": "skipped", "version": "skipped", "permissions": "skipped"}, steps(res))
	b := res.Steps[1]
	require.Equal(t, "TARGET_TIMEOUT", b.Code)
	require.Equal(t, "No answer from cb1.example.com:11210 within 5s.", b.Detail)
	require.Contains(t, b.Hint, "firewall")
	require.ElementsMatch(t, []string{"cb1.example.com:11210", "cb2.example.com:11210"}, dialed, "every bootstrap host, on the default KV port")

	// Capella: the SRV record's nodes are probed, and the hint names the allowed IPs.
	c.probe = authProber{lookupSRV: func(context.Context, string, string, string) (string, []*net.SRV, error) {
		return "", []*net.SRV{{Target: "svc-node-1.cb.x.cloud.couchbase.com.", Port: 11207}}, nil
	}}
	dialed = nil
	res = c.Test(context.Background(), cfgJSON(map[string]any{"deployment": "capella", "connectionString": "couchbases://cb.x.cloud.couchbase.com"}), connectors.Credentials{})
	require.Equal(t, []string{"svc-node-1.cb.x.cloud.couchbase.com:11207"}, dialed)
	require.Contains(t, res.Steps[1].Hint, "Allowed IP addresses")
}

func TestCouchbaseTest_OC190_AC5_TestCapsBootstrapOnly(t *testing.T) {
	w := &waitRecorder{fakeCluster: healthy()}
	c := testWith(nil, nil)
	c.connect = func(string, gocb.ClusterOptions) (cluster, error) { return w, nil }
	c.dial = func(context.Context, string, string) (net.Conn, error) { return nopConn{}, nil }
	res := c.Test(context.Background(), cfgJSON(map[string]any{"connectTimeoutMs": 20000}), connectors.Credentials{})
	require.True(t, res.OK, "%+v", res.Steps)
	require.Equal(t, connectors.ReachTimeout, w.timeout, "the test waits at most ReachTimeout")

	// A bootstrap failure after the network is fine: plain detail, the SDK's text kept as the cause.
	w.readyErr = gocb.ErrUnambiguousTimeout
	res = c.Test(context.Background(), cfgJSON(nil), connectors.Credentials{})
	require.Equal(t, "Couchbase at cb1.example.com didn't finish connecting within 5s.", res.Steps[1].Detail)
	require.Contains(t, res.Steps[1].Cause, "unambiguous timeout")
}

type nopConn struct{ net.Conn }

func (nopConn) Close() error { return nil }
