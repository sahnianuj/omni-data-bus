package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
)

// Resolver looks up a host's addresses (net.DefaultResolver in production).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DialFunc dials a network address.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Dialer enforces a Policy when connecting. It resolves the host once, refuses the whole dial
// if any resolved address is denied, and then dials only the addresses it checked, so a DNS
// answer that changes between the check and the connect (rebinding) can't get through.
type Dialer struct {
	Policy   Policy
	Resolver Resolver // nil = net.DefaultResolver
	Dial     DialFunc // nil = net.Dialer
}

// NewDialer returns a dialer with the system resolver and dialer.
func NewDialer(p Policy) *Dialer { return &Dialer{Policy: p} }

func (d *Dialer) resolver() Resolver {
	if d.Resolver != nil {
		return d.Resolver
	}
	return net.DefaultResolver
}

func (d *Dialer) dial() DialFunc {
	if d.Dial != nil {
		return d.Dial
	}
	return (&net.Dialer{}).DialContext
}

// Resolve returns the host's addresses when every one of them is allowed.
func (d *Dialer) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if !d.Policy.AllowsIP(ip) {
			return nil, ErrDenied
		}
		return []netip.Addr{ip}, nil
	}
	ips, err := d.resolver().LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
	}
	for i, ip := range ips {
		ips[i] = ip.Unmap()
		if !d.Policy.AllowsIP(ips[i]) {
			return nil, ErrDenied
		}
	}
	return ips, nil
}

// Check verifies a host:port without connecting (used for clients whose dialer can't be
// replaced, such as the Couchbase SDK's bootstrap hosts).
func (d *Dialer) Check(ctx context.Context, hostport string) error {
	host, port, err := splitPort(hostport)
	if err != nil {
		return err
	}
	if !d.Policy.AllowsPort(port) {
		return ErrDenied
	}
	_, err = d.Resolve(ctx, host)
	return err
}

// DialContext connects to address after checking every resolved IP; it tries the checked IPs
// in order and never resolves the host again.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network == "unix" || network == "unixgram" {
		if !d.Policy.AllowPrivateNetworks {
			return nil, ErrDenied // a local socket is as internal as it gets
		}
		return d.dial()(ctx, network, address)
	}
	host, port, err := splitPort(address)
	if err != nil {
		return nil, err
	}
	if !d.Policy.AllowsPort(port) {
		return nil, ErrDenied
	}
	ips, err := d.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, ip := range ips {
		c, err := d.dial()(ctx, network, net.JoinHostPort(ip.String(), strconv.Itoa(int(port))))
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}

// LookupStrings adapts Resolve to pgx's LookupFunc: the checked addresses as strings.
func (d *Dialer) LookupStrings(ctx context.Context, host string) ([]string, error) {
	ips, err := d.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out, nil
}

func splitPort(hostport string) (string, uint16, error) {
	host, p, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil {
		return "", 0, err
	}
	return host, uint16(port), nil
}

var def = NewDialer(Unrestricted())

// SetDefault installs the process-wide dialer built from configuration at startup.
func SetDefault(d *Dialer) { def = d }

// Default is the process-wide dialer (Unrestricted until SetDefault).
func Default() *Dialer { return def }
