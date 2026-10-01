package couchbase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/couchbase/gocb/v2"
)

// cluster is the subset of the SDK the connector uses (faked in tests).
type cluster interface {
	WaitReady(ctx context.Context, timeout time.Duration) error
	// Services reports reachability and latency per service ("kv", "query", …).
	Services(ctx context.Context) (map[string]ServiceReport, error)
	Version(ctx context.Context) (string, error)
	MyRoles(ctx context.Context) ([]Role, error)
	// Nodes lists the host:port of every node endpoint the SDK connected to (egress check).
	Nodes(ctx context.Context) ([]string, error)
	Close() error
}

// ServiceReport is one service's health.
type ServiceReport struct {
	OK      bool  `json:"ok"`
	Latency int64 `json:"latencyMs"`
}

type gocbCluster struct{ c *gocb.Cluster }

func connectGocb(connStr string, opts gocb.ClusterOptions) (cluster, error) {
	c, err := gocb.Connect(connStr, opts)
	if err != nil {
		return nil, err
	}
	return &gocbCluster{c: c}, nil
}

func (g *gocbCluster) WaitReady(ctx context.Context, timeout time.Duration) error {
	return g.c.WaitUntilReady(timeout, &gocb.WaitUntilReadyOptions{Context: ctx, ServiceTypes: []gocb.ServiceType{gocb.ServiceTypeQuery}})
}

func (g *gocbCluster) Services(ctx context.Context) (map[string]ServiceReport, error) {
	out := map[string]ServiceReport{}
	ping, err := g.c.Ping(&gocb.PingOptions{Context: ctx, ServiceTypes: []gocb.ServiceType{gocb.ServiceTypeQuery, gocb.ServiceTypeManagement}})
	if err == nil {
		for st, eps := range ping.Services {
			name := serviceName(st)
			for _, ep := range eps {
				r := out[name]
				if ep.State == gocb.PingStateOk {
					r.OK = true
					if l := ep.Latency.Milliseconds(); r.Latency == 0 || l < r.Latency {
						r.Latency = max(l, 1)
					}
				}
				out[name] = r
			}
		}
	}
	// KV can't be pinged without a bucket; the cluster-level diagnostics show its connections.
	diag, derr := g.c.Diagnostics(&gocb.DiagnosticsOptions{})
	if derr == nil {
		for _, eps := range diag.Services {
			for _, ep := range eps {
				if ep.Type == gocb.ServiceTypeKeyValue {
					r := out["kv"]
					r.OK = r.OK || ep.State == gocb.EndpointStateConnected
					out["kv"] = r
				}
			}
		}
	}
	if err != nil && derr != nil {
		return nil, err
	}
	return out, nil
}

func serviceName(st gocb.ServiceType) string {
	switch st {
	case gocb.ServiceTypeQuery:
		return "query"
	case gocb.ServiceTypeManagement:
		return "management"
	case gocb.ServiceTypeKeyValue:
		return "kv"
	}
	return fmt.Sprint(st)
}

func (g *gocbCluster) queryOne(ctx context.Context, stmt string, dst any) error {
	res, err := g.c.Query(stmt, &gocb.QueryOptions{Context: ctx, Readonly: true})
	if err != nil {
		return err
	}
	defer func() { _ = res.Close() }()
	if !res.Next() {
		if err := res.Err(); err != nil {
			return err
		}
		return errors.New("no result")
	}
	return res.Row(dst)
}

func (g *gocbCluster) Version(ctx context.Context) (string, error) {
	var v string
	if err := g.queryOne(ctx, "SELECT RAW ds_version()", &v); err != nil {
		return "", err
	}
	return v, nil
}

func (g *gocbCluster) MyRoles(ctx context.Context) ([]Role, error) {
	var info struct {
		Roles []Role `json:"roles"`
	}
	if err := g.queryOne(ctx, "SELECT RAW u FROM system:my_user_info u", &info); err != nil {
		return nil, err
	}
	return info.Roles, nil
}

func (g *gocbCluster) Close() error { return g.c.Close(nil) }

func (g *gocbCluster) Nodes(context.Context) ([]string, error) { return diagnosticNodes(g.c) }

// diagnosticNodes reads the remote addresses of the SDK's open endpoints.
func diagnosticNodes(c *gocb.Cluster) ([]string, error) {
	diag, err := c.Diagnostics(&gocb.DiagnosticsOptions{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, eps := range diag.Services {
		for _, ep := range eps {
			if ep.Remote != "" {
				out = append(out, ep.Remote)
			}
		}
	}
	return out, nil
}

// parseVersion turns "7.6.2-3721-enterprise" into (7, 6).
func parseVersion(v string) (major, minor int) {
	core, _, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) > 0 {
		major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	return major, minor
}

// --- keyspace administration (OC-013, OC-014) ---

func (g *gocbCluster) GetAllBuckets(ctx context.Context) (map[string]gocb.BucketSettings, error) {
	return g.c.Buckets().GetAllBuckets(&gocb.GetAllBucketsOptions{Context: ctx})
}

func (g *gocbCluster) CreateBucket(ctx context.Context, s gocb.CreateBucketSettings) error {
	return g.c.Buckets().CreateBucket(s, &gocb.CreateBucketOptions{Context: ctx})
}

func (g *gocbCluster) WaitBucket(ctx context.Context, bucket string, timeout time.Duration) error {
	return g.c.Bucket(bucket).WaitUntilReady(timeout, &gocb.WaitUntilReadyOptions{Context: ctx, ServiceTypes: []gocb.ServiceType{gocb.ServiceTypeKeyValue}})
}

func (g *gocbCluster) GetAllScopes(ctx context.Context, bucket string) ([]gocb.ScopeSpec, error) {
	return g.c.Bucket(bucket).CollectionsV2().GetAllScopes(&gocb.GetAllScopesOptions{Context: ctx})
}

func (g *gocbCluster) CreateScope(ctx context.Context, bucket, scope string) error {
	return g.c.Bucket(bucket).CollectionsV2().CreateScope(scope, &gocb.CreateScopeOptions{Context: ctx})
}

func (g *gocbCluster) CreateCollection(ctx context.Context, bucket, scope, collection string, maxExpiry time.Duration) error {
	return g.c.Bucket(bucket).CollectionsV2().CreateCollection(scope, collection,
		&gocb.CreateCollectionSettings{MaxExpiry: maxExpiry}, &gocb.CreateCollectionOptions{Context: ctx})
}

func (g *gocbCluster) ProbeCollection(ctx context.Context, bucket, scope, collection string) error {
	_, err := g.c.Bucket(bucket).Scope(scope).Collection(collection).Get(probeCollection, &gocb.GetOptions{Context: ctx, Timeout: 2 * time.Second})
	return err
}

func (g *gocbCluster) QueryRows(ctx context.Context, statement string, params map[string]any, timeout time.Duration) ([]json.RawMessage, error) {
	res, err := g.c.Query(statement, &gocb.QueryOptions{Context: ctx, Timeout: timeout, NamedParameters: params, Readonly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Close() }()
	var rows []json.RawMessage
	for res.Next() {
		var r json.RawMessage
		if err := res.Row(&r); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, res.Err()
}

func (g *gocbCluster) SampleScan(ctx context.Context, bucket, scope, collection string, n int) ([]json.RawMessage, error) {
	res, err := g.c.Bucket(bucket).Scope(scope).Collection(collection).Scan(gocb.SamplingScan{Limit: uint64(max(n, 1))}, &gocb.ScanOptions{Context: ctx}) //nolint:gosec // n is 1–1000 (SampleDocuments clamps it)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Close() }()
	var out []json.RawMessage
	for item := res.Next(); item != nil; item = res.Next() {
		var doc json.RawMessage
		if err := item.Content(&doc); err != nil {
			continue // non-JSON documents can't inform a design
		}
		out = append(out, doc)
	}
	return out, res.Err()
}

func (g *gocbCluster) Get(ctx context.Context, bucket, scope, collection, key string) ([]byte, error) {
	res, err := g.c.Bucket(bucket).Scope(scope).Collection(collection).Get(key, &gocb.GetOptions{Context: ctx, Timeout: 5 * time.Second, Transcoder: raw})
	if err != nil {
		return nil, err
	}
	var b []byte
	err = res.Content(&b)
	return b, err
}

func (g *gocbCluster) Exists(ctx context.Context, bucket, scope, collection, key string) (bool, error) {
	res, err := g.c.Bucket(bucket).Scope(scope).Collection(collection).Exists(key, &gocb.ExistsOptions{Context: ctx, Timeout: 5 * time.Second})
	if err != nil {
		return false, err
	}
	return res.Exists(), nil
}
