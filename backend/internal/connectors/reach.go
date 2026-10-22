package connectors

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"omnidatabus/internal/netguard"
)

// ReachTimeout bounds how long a connection test takes to decide that a repository can't be
// reached (OC-190). A run still uses the connection's own connect timeout.
const ReachTimeout = 5 * time.Second

// DialFunc opens a network connection (the egress guard's DialContext in production).
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Reason says why an address couldn't be reached.
type Reason string

// Reach failure reasons.
const (
	ReasonDNS     Reason = "dns"     // the host name doesn't resolve
	ReasonRefused Reason = "refused" // the host answered; nothing listens on the port
	ReasonTimeout Reason = "timeout" // no answer: packets dropped, or the host is down
	ReasonNoRoute Reason = "noroute" // this machine has no route to the host
	ReasonOther   Reason = "other"
)

// ReachError is a host:port that couldn't be reached, with the reason in plain words.
type ReachError struct {
	Addr   string // host:port
	Reason Reason
	Cause  error
}

func (e *ReachError) Error() string { return e.Message() }
func (e *ReachError) Unwrap() error { return e.Cause }

// Message is one sentence naming the address and what happened.
func (e *ReachError) Message() string {
	host, port, err := net.SplitHostPort(e.Addr)
	if err != nil {
		host, port = e.Addr, ""
	}
	switch e.Reason {
	case ReasonDNS:
		return fmt.Sprintf("The host name %s doesn't resolve.", host)
	case ReasonRefused:
		return fmt.Sprintf("%s refused the connection on port %s: nothing is listening there.", host, port)
	case ReasonTimeout:
		return fmt.Sprintf("No answer from %s within %s.", e.Addr, ReachTimeout)
	case ReasonNoRoute:
		return fmt.Sprintf("This machine has no network route to %s.", host)
	}
	return fmt.Sprintf("Couldn't connect to %s: %v.", e.Addr, e.Cause)
}

// Hint says what to check for this reason.
func (e *ReachError) Hint() string {
	switch e.Reason {
	case ReasonDNS:
		return "Check the host name for typos. A private name only resolves inside its own network: run from there, or over its VPN."
	case ReasonRefused:
		return "Check the port, and that the database is running on that host."
	case ReasonTimeout:
		return "Check the host and port. If they're right, a firewall, security group or IP allow list is dropping the connection: allow this machine to reach that port."
	case ReasonNoRoute:
		return "Check the host, and that this machine is on the database's network (VPN, peering)."
	}
	return "Check the host and port, and that this machine can reach the database."
}

// kind is the connection error kind for the reason, so problem codes stay stable.
func (e *ReachError) kind() Kind {
	switch e.Reason {
	case ReasonDNS:
		return KindDNS
	case ReasonTimeout:
		return KindTimeout
	}
	return KindUnreachable
}

// NewReachError classifies a dial or lookup error for addr; an egress-policy refusal is returned
// as is (it has its own code and message).
func NewReachError(addr string, err error) error {
	if netguard.IsDenied(err) {
		return err
	}
	var dnsErr *net.DNSError
	var netErr net.Error
	reason := ReasonOther
	switch {
	case errors.As(err, &dnsErr) && !dnsErr.IsTimeout:
		reason = ReasonDNS
	case errors.Is(err, syscall.ECONNREFUSED), strings.Contains(err.Error(), "connection refused"), strings.Contains(err.Error(), "actively refused"):
		reason = ReasonRefused
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH),
		strings.Contains(err.Error(), "no route to host"), strings.Contains(err.Error(), "network is unreachable"):
		reason = ReasonNoRoute
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout(), errors.As(err, &dnsErr):
		reason = ReasonTimeout
	}
	return &ReachError{Addr: addr, Reason: reason, Cause: err}
}

// Reach opens a TCP connection to each address at once, within ReachTimeout, and returns nil as
// soon as one connects. Otherwise it returns the first address's error (a *ReachError, or the
// egress guard's refusal).
func Reach(ctx context.Context, dial DialFunc, addrs []string) error {
	if len(addrs) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, ReachTimeout)
	defer cancel()
	type result struct {
		i   int
		err error
	}
	results := make(chan result, len(addrs))
	for i, a := range addrs {
		go func() {
			c, err := dial(ctx, "tcp", a)
			if err == nil {
				_ = c.Close()
			}
			results <- result{i, err}
		}()
	}
	errs := make([]error, len(addrs))
	for range addrs {
		r := <-results
		if r.err == nil {
			return nil
		}
		errs[r.i] = r.err
	}
	return NewReachError(addrs[0], errs[0])
}
