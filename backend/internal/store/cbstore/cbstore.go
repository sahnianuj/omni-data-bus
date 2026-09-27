// Package cbstore implements store.Store on Couchbase (Capella in production,
// Couchbase Server 8.0.3 locally). See OC-003 and architecture §5.
package cbstore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"omnidatabus/internal/store"
	"omnidatabus/internal/store/provision"

	"github.com/couchbase/gocb/v2"
)

// Config selects the metadata cluster, bucket and scope.
type Config struct {
	ConnStr        string
	Username       string
	Password       string
	Bucket         string
	Scope          string
	Capella        bool
	TLSSkipVerify  bool
	ConnectTimeout time.Duration
	// AuditRetention is how long audit events are kept (document expiry); 0 = AuditRetention.
	AuditRetention time.Duration
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_%.-]{0,99}$`)

// tsLayout is fixed-width so timestamps sort correctly as strings in SQL++.
const tsLayout = "2006-01-02T15:04:05.000000000Z"

func formatTS(t time.Time) string { return t.UTC().Format(tsLayout) }

func parseTS(s string) time.Time {
	t, _ := time.Parse(tsLayout, s)
	return t
}

// Store is the Couchbase-backed metadata store.
type Store struct {
	cluster *gocb.Cluster
	bucket  *gocb.Bucket
	cfg     Config
	clock   store.Clock
	queries map[string]Query
}

// Open connects and waits until KV and Query are ready. It does not provision; call Provision.
func Open(ctx context.Context, cfg Config, clock store.Clock) (*Store, error) {
	if !validName.MatchString(cfg.Bucket) || !validName.MatchString(cfg.Scope) {
		return nil, fmt.Errorf("cbstore: invalid bucket %q or scope %q", cfg.Bucket, cfg.Scope)
	}
	if clock == nil {
		clock = store.SystemClock{}
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 30 * time.Second
	}
	opts := gocb.ClusterOptions{
		Authenticator:  gocb.PasswordAuthenticator{Username: cfg.Username, Password: cfg.Password},
		SecurityConfig: gocb.SecurityConfig{TLSSkipVerify: cfg.TLSSkipVerify},
	}
	if cfg.Capella {
		if err := opts.ApplyProfile(gocb.ClusterConfigProfileWanDevelopment); err != nil {
			return nil, err
		}
	}
	cluster, err := gocb.Connect(cfg.ConnStr, opts)
	if err != nil {
		return nil, fmt.Errorf("cbstore: connect: %w", err)
	}
	bucket := cluster.Bucket(cfg.Bucket)
	wait := &gocb.WaitUntilReadyOptions{
		Context:      ctx,
		ServiceTypes: []gocb.ServiceType{gocb.ServiceTypeKeyValue, gocb.ServiceTypeQuery},
	}
	if err := bucket.WaitUntilReady(cfg.ConnectTimeout, wait); err != nil {
		_ = cluster.Close(nil)
		return nil, fmt.Errorf("cbstore: metadata bucket %q not ready (check OC_META_* settings and that the bucket exists): %w", cfg.Bucket, err)
	}
	s := &Store{cluster: cluster, bucket: bucket, cfg: cfg, clock: clock}
	if s.queries, err = loadQueries(queryFiles, s.keyspace); err != nil {
		_ = cluster.Close(nil)
		return nil, err
	}
	return s, nil
}

func (s *Store) coll(name string) *gocb.Collection {
	return s.bucket.Scope(s.cfg.Scope).Collection(name)
}

func (s *Store) keyspace(collection string) string {
	return fmt.Sprintf("`%s`.`%s`.`%s`", s.cfg.Bucket, s.cfg.Scope, collection)
}

// Projects implements store.Store.
func (s *Store) Projects() store.Projects { return projects{s} }

// Connections implements store.Store.
func (s *Store) Connections() store.Connections { return connections{s} }

// Ping checks that the metadata bucket answers on KV.
func (s *Store) Ping(ctx context.Context) error {
	res, err := s.bucket.Ping(&gocb.PingOptions{ServiceTypes: []gocb.ServiceType{gocb.ServiceTypeKeyValue}, Context: ctx})
	if err != nil {
		return store.Unavailable(err)
	}
	for _, eps := range res.Services {
		for _, ep := range eps {
			if ep.State == gocb.PingStateOk {
				return nil
			}
		}
	}
	return store.Unavailable(errors.New("no healthy KV endpoint"))
}

// Close releases the cluster connection.
func (s *Store) Close() error { return s.cluster.Close(nil) }

// Provision creates or upgrades the scope, collections and indexes.
func (s *Store) Provision(ctx context.Context) (provision.Result, error) {
	return provision.Run(ctx, env{s}, provision.Steps())
}

// Plan lists what provisioning creates (for `admin provision --print`).
func (s *Store) Plan() []string { return provision.Plan(env{s}, provision.Steps()) }

// tx runs fn in a Couchbase ACID transaction.
func (s *Store) tx(fn func(tx *gocb.TransactionAttemptContext) error) error {
	_, err := s.cluster.Transactions().Run(fn, &gocb.TransactionOptions{Timeout: 15 * time.Second})
	return err
}

func (s *Store) query(ctx context.Context, name string, params map[string]any) (*gocb.QueryResult, error) {
	q, ok := s.queries[name]
	if !ok {
		return nil, fmt.Errorf("cbstore: unknown query %q", name)
	}
	return s.cluster.Query(q.SQL, &gocb.QueryOptions{
		NamedParameters: params,
		ScanConsistency: gocb.QueryScanConsistencyRequestPlus,
		Adhoc:           false,
		Context:         ctx,
	})
}

// WithClock returns a view of the store that uses clock (tests share one connection).
func (s *Store) WithClock(clock store.Clock) *Store {
	c := *s
	c.clock = clock
	return &c
}
