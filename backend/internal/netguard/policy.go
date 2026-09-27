// Package netguard decides which network addresses the server may connect to on a user's
// behalf, and enforces it when dialing (OC-033). Users type arbitrary hosts into connection
// forms, so without it the server could be used to probe internal networks or cloud metadata.
package netguard

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"omnidatabus/internal/problem"
)

// CodeEgressDenied is the problem code for a refused destination.
const CodeEgressDenied = "EGRESS_DENIED"

// ErrDenied is returned for a destination the policy refuses. Its message never includes the
// resolved IP, so resolution results don't leak to the user.
var ErrDenied = deniedError{}

type deniedError struct{}

func (deniedError) Error() string {
	return "Connections to private or internal addresses are disabled on this server."
}

// Problem implements problem.Problemer.
func (deniedError) Problem() problem.Problem {
	return problem.New(http.StatusUnprocessableEntity, CodeEgressDenied, "Destination not allowed", deniedError{}.Error())
}

func prefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}

var (
	// alwaysDenied can't be allowed by any setting: link-local (cloud metadata lives at
	// 169.254.169.254), AWS's IPv6 metadata address, "this network", multicast and broadcast.
	alwaysDenied = prefixes("169.254.0.0/16", "fe80::/10", "fd00:ec2::254/128", "0.0.0.0/8",
		"224.0.0.0/4", "ff00::/8", "255.255.255.255/32", "::/128")
	// private is allowed only with AllowPrivateNetworks: loopback, RFC 1918, CGNAT, ULA.
	private = prefixes("127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"100.64.0.0/10", "fc00::/7")
)

// PortRange is an inclusive range of TCP ports.
type PortRange struct{ From, To uint16 }

// DefaultPorts are PostgreSQL (5432–5439, PgBouncer 6432), Couchbase (8091–8097, 11207, 11210,
// 18091–18097) and HTTPS (443).
var DefaultPorts = []PortRange{
	{5432, 5439}, {6432, 6432}, {8091, 8097}, {11207, 11207}, {11210, 11210}, {18091, 18097}, {443, 443},
}

// Policy decides whether an (IP, port) may be dialed.
type Policy struct {
	// AllowPrivateNetworks permits loopback, RFC 1918, CGNAT and ULA addresses (self-hosted
	// default true; set false for SaaS).
	AllowPrivateNetworks bool
	// Allow lists extra permitted ranges (e.g. one private subnet with private networks off).
	Allow []netip.Prefix
	// Deny lists refused ranges; deny wins over everything except nothing.
	Deny []netip.Prefix
	// Ports are the permitted destination ports; empty means DefaultPorts.
	Ports []PortRange
}

// Unrestricted allows everything except the always-denied ranges on the default ports.
func Unrestricted() Policy { return Policy{AllowPrivateNetworks: true} }

// AllowsIP reports whether the address may be dialed (ports aside).
func (p Policy) AllowsIP(ip netip.Addr) bool {
	ip = ip.Unmap() // ::ffff:10.0.0.1 is 10.0.0.1
	if !ip.IsValid() {
		return false
	}
	ip = ip.WithZone("")
	for _, d := range alwaysDenied {
		if d.Contains(ip) {
			return false
		}
	}
	for _, d := range p.Deny {
		if d.Contains(ip) {
			return false
		}
	}
	for _, a := range p.Allow {
		if a.Contains(ip) {
			return true
		}
	}
	if p.AllowPrivateNetworks {
		return true
	}
	for _, d := range private {
		if d.Contains(ip) {
			return false
		}
	}
	return true
}

// AllowsPort reports whether the destination port is permitted.
func (p Policy) AllowsPort(port uint16) bool {
	ports := p.Ports
	if len(ports) == 0 {
		ports = DefaultPorts
	}
	for _, r := range ports {
		if port >= r.From && port <= r.To {
			return true
		}
	}
	return false
}

// Allows checks one resolved destination.
func (p Policy) Allows(ip netip.Addr, port uint16) bool { return p.AllowsIP(ip) && p.AllowsPort(port) }

// ParseCIDRs parses a comma-separated list; a bare address means a single host.
func ParseCIDRs(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.Contains(f, "/") {
			a, err := netip.ParseAddr(f)
			if err != nil {
				return nil, fmt.Errorf("invalid address %q", f)
			}
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			continue
		}
		pfx, err := netip.ParsePrefix(f)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q", f)
		}
		out = append(out, pfx.Masked())
	}
	return out, nil
}

// ParsePorts parses "5432-5439,6432,443"; empty means DefaultPorts.
func ParsePorts(s string) ([]PortRange, error) {
	var out []PortRange
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(f, "-")
		from, err := strconv.ParseUint(strings.TrimSpace(lo), 10, 16)
		if err != nil || from == 0 {
			return nil, fmt.Errorf("invalid port %q", f)
		}
		to := from
		if isRange {
			if to, err = strconv.ParseUint(strings.TrimSpace(hi), 10, 16); err != nil || to < from {
				return nil, fmt.Errorf("invalid port range %q", f)
			}
		}
		out = append(out, PortRange{uint16(from), uint16(to)})
	}
	return out, nil
}

// IsDenied reports whether err is (or wraps) a policy refusal.
func IsDenied(err error) bool { return errors.Is(err, ErrDenied) }
