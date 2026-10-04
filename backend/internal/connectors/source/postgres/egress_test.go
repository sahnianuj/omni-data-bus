package postgres

import (
	"context"
	"net"
	"net/netip"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/netguard"
	"omnidatabus/internal/problem"
)

type oneIP string

func (r oneIP) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr(string(r))}, nil
}

// TestMain allows any port: testcontainers maps PostgreSQL to a random host port, outside
// DefaultPorts. Tests that check the guard install their own with withGuard.
func TestMain(m *testing.M) {
	p := netguard.Unrestricted()
	p.Ports = []netguard.PortRange{{From: 1, To: 65535}}
	netguard.SetDefault(netguard.NewDialer(p))
	os.Exit(m.Run())
}

// withGuard installs a restrictive process-wide guard for one test.
func withGuard(t *testing.T, d *netguard.Dialer) {
	t.Helper()
	prev := netguard.Default()
	netguard.SetDefault(d)
	t.Cleanup(func() { netguard.SetDefault(prev) })
}

func TestPgConfig_AC2_UsesGuardedDialAndLookup(t *testing.T) {
	var dialed []string
	withGuard(t, &netguard.Dialer{
		Policy:   netguard.Policy{}, // private networks off
		Resolver: oneIP("10.0.0.5"),
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			c, s := net.Pipe()
			_ = s.Close()
			return c, nil
		},
	})
	cc, err := buildConnConfig(mustConfig(t, map[string]any{"host": "db.internal.example"}), connectors.Credentials{Password: "pw"})
	require.NoError(t, err)
	_, err = cc.LookupFunc(context.Background(), "db.internal.example")
	require.ErrorIs(t, err, netguard.ErrDenied, "the lookup refuses a private answer")
	_, err = cc.DialFunc(context.Background(), "tcp", "169.254.169.254:5432")
	require.ErrorIs(t, err, netguard.ErrDenied)
	require.Empty(t, dialed)

	// pgx itself goes through them: connecting fails with the egress problem, not a network error.
	_, err = pgx.ConnectConfig(context.Background(), cc)
	require.ErrorIs(t, err, netguard.ErrDenied)
	p := problem.From(connectors.Classify(connectors.RoleSource, err))
	require.Equal(t, netguard.CodeEgressDenied, p.Code)
	require.Equal(t, 422, p.Status)
	require.NotContains(t, p.Detail, "10.0.0.5")

	// The read sessions' dialer copies the same config, so bulk reads are guarded too.
	require.NotNil(t, cc.Copy().DialFunc)
	require.NotNil(t, cc.Copy().LookupFunc)
}

func TestPgConfig_TLSServerNameKeptWithGuardedLookup(t *testing.T) {
	withGuard(t, &netguard.Dialer{Policy: netguard.Unrestricted(), Resolver: oneIP("203.0.113.20")})
	cc, err := buildConnConfig(mustConfig(t, map[string]any{"host": "db.example.com", "sslMode": "verify-full"}), connectors.Credentials{Password: "pw"})
	require.NoError(t, err)
	ips, err := cc.LookupFunc(context.Background(), "db.example.com")
	require.NoError(t, err)
	require.Equal(t, []string{"203.0.113.20"}, ips)
	require.Equal(t, "db.example.com", cc.TLSConfig.ServerName, "certificates are still checked against the host name")
}
