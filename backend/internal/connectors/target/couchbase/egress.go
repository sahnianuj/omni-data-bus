package couchbase

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/couchbaselabs/gocbconnstr/v2"

	"omnidatabus/internal/netguard"
)

// The SDK can't take a custom dialer for every service, so the egress policy (OC-033) is
// enforced around it: the bootstrap hosts are checked before connecting, and every node the
// cluster reports is checked once it's ready. A violation closes the connection.

// checkBootstrap resolves and checks each host in the connection string.
func checkBootstrap(ctx context.Context, guard *netguard.Dialer, connStr string) error {
	spec, err := gocbconnstr.Parse(connStr)
	if err != nil {
		return nil // ParseConfig already validated it; the SDK reports anything else
	}
	def := 11210
	switch strings.ToLower(spec.Scheme) {
	case "couchbases":
		def = 11207
	case "http":
		def = 8091
	case "https":
		def = 18091
	}
	for _, a := range spec.Addresses {
		port := a.Port
		if port <= 0 {
			port = def
		}
		host := strings.Trim(a.Host, "[]")
		err := guard.Check(ctx, net.JoinHostPort(host, strconv.Itoa(port)))
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			// Not resolvable now: the SDK reports it as it can't connect either, and whatever it
			// does reach is checked after bootstrap (verifyNodes).
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// verifyNodes checks the addresses of the nodes the cluster reported after bootstrap.
func verifyNodes(ctx context.Context, guard *netguard.Dialer, nodes []string) error {
	for _, n := range nodes {
		if err := guard.Check(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// open bootstraps a guarded cluster connection: bootstrap hosts checked, ready within timeout,
// and every reported node checked.
func (c *Connector) open(ctx context.Context, cfg Config, opts gocb.ClusterOptions, timeout time.Duration) (cluster, error) {
	guard := netguard.Default()
	if err := checkBootstrap(ctx, guard, cfg.ConnectionString); err != nil {
		return nil, err
	}
	cl, err := c.connect(cfg.ConnectionString, opts)
	if err != nil {
		return nil, err
	}
	if err := cl.WaitReady(ctx, timeout); err != nil {
		_ = cl.Close()
		return nil, err
	}
	nodes, err := cl.Nodes(ctx)
	if err == nil {
		err = verifyNodes(ctx, guard, nodes)
	}
	if err != nil {
		_ = cl.Close()
		return nil, err
	}
	return cl, nil
}
