package connectors

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type nopConn struct{ net.Conn }

func (nopConn) Close() error { return nil }

// blackhole waits for the deadline, like a host behind a firewall that drops packets.
func blackhole(ctx context.Context, _, _ string) (net.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestReach_OC190_AC1_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond) // stands in for ReachTimeout
	defer cancel()
	start := time.Now()
	err := Reach(ctx, blackhole, []string{"cb.example.com:11210"})
	require.Less(t, time.Since(start), time.Second, "decided by the deadline, not a driver timeout")
	var re *ReachError
	require.ErrorAs(t, err, &re)
	require.Equal(t, ReasonTimeout, re.Reason)
	require.Equal(t, "No answer from cb.example.com:11210 within 5s.", re.Message())
	require.Contains(t, re.Hint(), "firewall")
	ce := Classify(RoleTarget, err)
	require.Equal(t, "TARGET_TIMEOUT", ce.Code())
	require.Equal(t, re.Hint(), ce.Hint(), "the probe's hint wins over the kind's")
	require.LessOrEqual(t, ReachTimeout, 5*time.Second, "the story's budget")
}

func TestReach_OC190_AC2_Causes(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	noRoute := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}
	for _, tc := range []struct {
		name    string
		err     error
		reason  Reason
		code    string
		message string
	}{
		{"dns", &net.DNSError{Err: "no such host", Name: "db.internal", IsNotFound: true}, ReasonDNS, "SOURCE_DNS", "The host name db.internal doesn't resolve."},
		{"refused", refused, ReasonRefused, "SOURCE_UNREACHABLE", "db.internal refused the connection on port 5432: nothing is listening there."},
		{"no route", noRoute, ReasonNoRoute, "SOURCE_UNREACHABLE", "This machine has no network route to db.internal."},
		{"timeout", context.DeadlineExceeded, ReasonTimeout, "SOURCE_TIMEOUT", "No answer from db.internal:5432 within 5s."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dial := func(context.Context, string, string) (net.Conn, error) { return nil, tc.err }
			err := Reach(context.Background(), dial, []string{"db.internal:5432"})
			var re *ReachError
			require.ErrorAs(t, err, &re)
			require.Equal(t, tc.reason, re.Reason)
			require.Equal(t, tc.message, re.Message())
			require.NotEmpty(t, re.Hint())
			require.Equal(t, tc.code, Classify(RoleSource, err).Code())
		})
	}
}

func TestReach_OC190_OneReachableNodeIsEnough(t *testing.T) {
	dial := func(_ context.Context, _, addr string) (net.Conn, error) {
		if addr == "cb2:11210" {
			return nopConn{}, nil
		}
		return nil, errors.New("connection refused")
	}
	require.NoError(t, Reach(context.Background(), dial, []string{"cb1:11210", "cb2:11210"}))
	require.NoError(t, Reach(context.Background(), dial, nil), "nothing to probe")
}
