package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeResolver answers from a queue per host, so a test can change the answer between lookups.
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string][][]string
	lookups int
}

func (r *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookups++
	q := r.answers[host]
	if len(q) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	ans := q[0]
	if len(q) > 1 {
		r.answers[host] = q[1:]
	}
	out := make([]netip.Addr, len(ans))
	for i, a := range ans {
		out[i] = netip.MustParseAddr(a)
	}
	return out, nil
}

// recorder records dialed addresses and fails the ones listed in refuse.
type recorder struct {
	mu     sync.Mutex
	dialed []string
	refuse map[string]bool
}

func (d *recorder) dial(_ context.Context, _, address string) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dialed = append(d.dialed, address)
	if d.refuse[address] {
		return nil, errors.New("connection refused")
	}
	c1, c2 := net.Pipe()
	_ = c2.Close()
	return c1, nil
}

func TestDialer_AC2_RejectsIfAnyResolvedIPDenied(t *testing.T) {
	res := &fakeResolver{answers: map[string][][]string{"db.example.com": {{"203.0.113.5", "10.0.0.5"}}}}
	rec := &recorder{}
	d := &Dialer{Policy: Policy{}, Resolver: res, Dial: rec.dial}
	_, err := d.DialContext(context.Background(), "tcp", "db.example.com:5432")
	require.ErrorIs(t, err, ErrDenied)
	require.NotContains(t, err.Error(), "10.0.0.5", "the resolved IP isn't echoed")
	require.Empty(t, rec.dialed, "nothing is dialed")

	// Literal IPs, ports and local sockets are checked too.
	for _, addr := range []string{"169.254.169.254:443", "[::ffff:127.0.0.1]:5432", "8.8.8.8:22"} {
		_, err = d.DialContext(context.Background(), "tcp", addr)
		require.ErrorIs(t, err, ErrDenied, addr)
	}
	_, err = d.DialContext(context.Background(), "unix", "/var/run/postgresql/.s.PGSQL.5432")
	require.ErrorIs(t, err, ErrDenied)
	require.ErrorIs(t, d.Check(context.Background(), "db.example.com:5432"), ErrDenied)
}

func TestDialer_AC2_DialsCheckedIPOnly(t *testing.T) {
	// First answer is public (checked); a second lookup would return a private address.
	res := &fakeResolver{answers: map[string][][]string{"rebind.example.com": {{"203.0.113.7", "203.0.113.8"}, {"10.0.0.9"}}}}
	rec := &recorder{refuse: map[string]bool{"203.0.113.7:5432": true}}
	d := &Dialer{Policy: Policy{}, Resolver: res, Dial: rec.dial}
	c, err := d.DialContext(context.Background(), "tcp", "rebind.example.com:5432")
	require.NoError(t, err)
	_ = c.Close()
	require.Equal(t, []string{"203.0.113.7:5432", "203.0.113.8:5432"}, rec.dialed, "only checked IPs, falling through on failure")
	require.Equal(t, 1, res.lookups, "the host is resolved once")

	ips, err := d.LookupStrings(context.Background(), "8.8.4.4")
	require.NoError(t, err)
	require.Equal(t, []string{"8.8.4.4"}, ips)
}

func TestDialer_AllowsPrivateWhenConfigured(t *testing.T) {
	res := &fakeResolver{answers: map[string][][]string{"pg.internal": {{"10.0.0.5"}}}}
	rec := &recorder{}
	d := &Dialer{Policy: Unrestricted(), Resolver: res, Dial: rec.dial}
	c, err := d.DialContext(context.Background(), "tcp", "pg.internal:5432")
	require.NoError(t, err)
	_ = c.Close()
	require.Equal(t, []string{"10.0.0.5:5432"}, rec.dialed)
	_, err = d.DialContext(context.Background(), "tcp", "nowhere.example:5432")
	var dnsErr *net.DNSError
	require.ErrorAs(t, err, &dnsErr)
	_, err = d.DialContext(context.Background(), "tcp", "no-port")
	require.Error(t, err)
}
