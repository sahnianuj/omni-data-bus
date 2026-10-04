package couchbase

import (
	"context"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/netguard"
	"omnidatabus/internal/problem"
)

// staticResolver answers example hosts offline (known ones from the map, others with a public
// IP) and leaves real names, such as the integration tests' localhost, to the system resolver.
type staticResolver map[string]string

func (r staticResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if !strings.HasSuffix(host, ".example") && !strings.HasSuffix(host, ".example.com") {
		return net.DefaultResolver.LookupNetIP(ctx, network, host)
	}
	if ip, ok := r[host]; ok {
		if ip == "" {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	}
	return []netip.Addr{netip.MustParseAddr("203.0.113.10")}, nil
}

var exampleHosts = staticResolver{"metadata.evil.example": "169.254.169.254", "internal.example": "10.0.0.8", "gone.example": ""}

// Unit tests never resolve example hosts for real; everything else (localhost in the
// integration tests) behaves as in production with private networks allowed. Any port is
// allowed: testcontainers maps Couchbase to a random host port, outside DefaultPorts.
func TestMain(m *testing.M) {
	p := netguard.Unrestricted()
	p.Ports = []netguard.PortRange{{From: 1, To: 65535}}
	netguard.SetDefault(&netguard.Dialer{Policy: p, Resolver: exampleHosts})
	os.Exit(m.Run())
}

// restricted installs a policy without private networks for one test.
func restricted(t *testing.T) {
	t.Helper()
	prev := netguard.Default()
	netguard.SetDefault(&netguard.Dialer{Policy: netguard.Policy{}, Resolver: exampleHosts})
	t.Cleanup(func() { netguard.SetDefault(prev) })
}

func TestCouchbaseEgress_AC3_NodeListChecked(t *testing.T) {
	restricted(t)
	fc := healthy()
	fc.nodes = []string{"203.0.113.10:11210", "169.254.169.254:11210"}
	c := testWith(fc, nil)
	_, err := c.open(context.Background(), Config{ConnectionString: "couchbase://cb.example.com"}, gocb.ClusterOptions{}, time.Second)
	require.ErrorIs(t, err, netguard.ErrDenied)
	require.True(t, fc.closed, "the session is closed")

	// Through the connector: the test step and the keyspace browser both report EGRESS_DENIED.
	fc = healthy()
	fc.nodes = []string{"metadata.evil.example:11210"}
	res := testWith(fc, nil).Test(context.Background(), cfgJSON(nil), connectors.Credentials{Password: "pw"})
	require.Equal(t, connectors.StepFail, steps(res)["bootstrap"])
	require.Equal(t, netguard.CodeEgressDenied, stepByID(res, "bootstrap").Code)
	require.NotContains(t, stepByID(res, "bootstrap").Hint+stepByID(res, "bootstrap").Detail, "169.254")

	fc = healthy()
	fc.nodes = []string{"internal.example:11210"}
	_, err = testWith(fc, nil).Keyspaces(context.Background(), "c1", "v1", cfgJSON(nil), connectors.Credentials{Password: "pw"})
	require.Equal(t, netguard.CodeEgressDenied, problem.From(err).Code)
	require.Equal(t, 422, problem.From(err).Status)
}

func TestCouchbaseEgress_BootstrapHostsCheckedFirst(t *testing.T) {
	restricted(t)
	connected := false
	c := testWith(healthy(), nil)
	c.connect = func(string, gocb.ClusterOptions) (cluster, error) { connected = true; return healthy(), nil }
	for _, cs := range []string{"couchbase://169.254.169.254", "couchbases://cb.example.com,metadata.evil.example", "couchbase://10.1.1.1:11210", "couchbase://cb.example.com:22"} {
		_, err := c.open(context.Background(), Config{ConnectionString: cs}, gocb.ClusterOptions{}, time.Second)
		require.ErrorIs(t, err, netguard.ErrDenied, cs)
	}
	require.False(t, connected, "the SDK never bootstraps to a denied host")

	// An unresolvable host is left to the SDK, and public hosts pass.
	for _, cs := range []string{"couchbase://gone.example", "couchbases://cb.example.com", "http://cb.example.com:8091", "couchbase://[2606:4700::1111]"} {
		connected = false
		_, err := c.open(context.Background(), Config{ConnectionString: cs}, gocb.ClusterOptions{}, time.Second)
		require.NoError(t, err, cs)
		require.True(t, connected, cs)
	}
}

func stepByID(res connectors.TestResult, id string) connectors.TestStep {
	for _, s := range res.Steps {
		if s.ID == id {
			return s
		}
	}
	return connectors.TestStep{}
}
