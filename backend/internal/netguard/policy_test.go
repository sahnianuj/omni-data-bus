package netguard

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

func TestPolicy_AC1_Matrix(t *testing.T) {
	allow := func(cidrs string) []netip.Prefix { p, err := ParseCIDRs(cidrs); require.NoError(t, err); return p }
	cases := []struct {
		ip                  string
		private, restricted bool // expected with AllowPrivateNetworks true / false
	}{
		{"169.254.169.254", false, false}, // cloud metadata: always denied
		{"169.254.10.1", false, false},
		{"fe80::1", false, false},
		{"fd00:ec2::254", false, false},
		{"0.1.2.3", false, false},
		{"224.0.0.1", false, false},
		{"255.255.255.255", false, false},
		{"127.0.0.1", true, false},
		{"::1", true, false},
		{"10.1.2.3", true, false},
		{"172.16.0.1", true, false},
		{"172.31.255.255", true, false},
		{"172.32.0.1", true, true}, // just outside 172.16/12: public
		{"192.168.1.10", true, false},
		{"100.64.0.1", true, false},
		{"fc00::1", true, false},
		{"fd12:3456::1", true, false},
		{"203.0.113.5", true, true},
		{"8.8.8.8", true, true},
		{"2606:4700::1111", true, true},
	}
	for _, c := range cases {
		ip := netip.MustParseAddr(c.ip)
		require.Equal(t, c.private, Policy{AllowPrivateNetworks: true}.AllowsIP(ip), "%s with private networks", c.ip)
		require.Equal(t, c.restricted, Policy{}.AllowsIP(ip), "%s without private networks", c.ip)
	}

	// Allow opens one private range; deny wins over allow and over private networks.
	p := Policy{Allow: allow("10.20.0.0/16"), Deny: allow("10.20.5.0/24, 203.0.113.9")}
	require.True(t, p.AllowsIP(netip.MustParseAddr("10.20.1.1")))
	require.False(t, p.AllowsIP(netip.MustParseAddr("10.20.5.1")), "deny wins")
	require.False(t, p.AllowsIP(netip.MustParseAddr("10.30.0.1")))
	require.False(t, p.AllowsIP(netip.MustParseAddr("203.0.113.9")))
	require.True(t, p.AllowsIP(netip.MustParseAddr("203.0.113.10")))
	require.False(t, Policy{AllowPrivateNetworks: true, Deny: allow("192.168.0.0/16")}.AllowsIP(netip.MustParseAddr("192.168.1.1")))
	// Nothing re-allows the metadata range.
	require.False(t, Policy{AllowPrivateNetworks: true, Allow: allow("169.254.0.0/16")}.AllowsIP(netip.MustParseAddr("169.254.169.254")))
	require.False(t, Policy{}.AllowsIP(netip.Addr{}), "invalid address")
}

func TestPolicy_AC1_IPv4MappedIPv6Normalized(t *testing.T) {
	require.False(t, Policy{}.AllowsIP(netip.MustParseAddr("::ffff:10.0.0.1")))
	require.False(t, Policy{AllowPrivateNetworks: true}.AllowsIP(netip.MustParseAddr("::ffff:169.254.169.254")))
	require.True(t, Policy{}.AllowsIP(netip.MustParseAddr("::ffff:8.8.8.8")))
	require.False(t, Policy{}.AllowsIP(netip.MustParseAddr("fe80::1%eth0")), "zones are ignored")
	deny, err := ParseCIDRs("::ffff:203.0.113.5")
	require.NoError(t, err)
	require.False(t, Policy{Deny: deny}.AllowsIP(netip.MustParseAddr("203.0.113.5")), "a mapped deny entry matches the IPv4 address")
}

func TestPolicy_AC1_PortAllowList(t *testing.T) {
	p := Policy{}
	for _, port := range []uint16{5432, 5439, 6432, 8091, 8097, 11207, 11210, 18091, 18097, 443} {
		require.True(t, p.AllowsPort(port), port)
	}
	for _, port := range []uint16{22, 80, 3306, 5440, 6379, 8098, 11211, 18098} {
		require.False(t, p.AllowsPort(port), port)
	}
	ports, err := ParsePorts("5432, 9000-9002")
	require.NoError(t, err)
	custom := Policy{Ports: ports}
	require.True(t, custom.AllowsPort(9001))
	require.False(t, custom.AllowsPort(443), "a custom list replaces the defaults")
	require.False(t, custom.Allows(netip.MustParseAddr("8.8.8.8"), 22))
	require.True(t, custom.Allows(netip.MustParseAddr("8.8.8.8"), 5432))

	for _, bad := range []string{"0", "70000", "9-3", "x", "1-"} {
		_, err := ParsePorts(bad)
		require.Error(t, err, bad)
	}
	for _, bad := range []string{"10.0.0.0/33", "not-an-ip"} {
		_, err := ParseCIDRs(bad)
		require.Error(t, err, bad)
	}
}

func TestDenied_IsAProblemWithoutTheIP(t *testing.T) {
	require.True(t, IsDenied(fmt.Errorf("open: %w", ErrDenied)))
	pr := problem.From(fmt.Errorf("connect: %w", ErrDenied))
	require.Equal(t, 422, pr.Status)
	require.Equal(t, CodeEgressDenied, pr.Code)
	require.Equal(t, "Connections to private or internal addresses are disabled on this server.", pr.Detail)
}
