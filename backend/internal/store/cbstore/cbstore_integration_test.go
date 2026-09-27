//go:build integration

package cbstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"omnidatabus/internal/store"
	"omnidatabus/internal/store/provision"
	"omnidatabus/internal/store/storetest"

	"github.com/couchbase/gocb/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	tccouchbase "github.com/testcontainers/testcontainers-go/modules/couchbase"
)

// Integration tests run against Couchbase Server 8.0.3. By default a container is started;
// set OC_IT_META_CONNSTR (+ _USERNAME/_PASSWORD) to reuse e.g. the dev stack instead.
const itImage = "couchbase/server:enterprise-8.0.3"

var shared *Store

func TestMain(m *testing.M) {
	os.Exit(runIntegration(m))
}

func runIntegration(m *testing.M) int {
	ctx := context.Background()
	capella := strings.HasPrefix(os.Getenv("OC_IT_META_CONNSTR"), "couchbases://")
	cfg := Config{
		ConnStr: os.Getenv("OC_IT_META_CONNSTR"), Username: os.Getenv("OC_IT_META_USERNAME"),
		Password: os.Getenv("OC_IT_META_PASSWORD"), Bucket: "omniconnect",
		// Unique scope per run so reruns and the dev stack's real "meta" scope are untouched.
		Scope: "it_" + uuid.NewString()[:8], ConnectTimeout: 2 * time.Minute, Capella: capella,
	}
	if b := os.Getenv("OC_IT_META_BUCKET"); b != "" {
		cfg.Bucket = b
	}
	if cfg.ConnStr == "" {
		c, err := tccouchbase.Run(ctx, itImage, tccouchbase.WithBuckets(tccouchbase.NewBucket("omniconnect").WithQuota(256)))
		if err != nil {
			fmt.Fprintln(os.Stderr, "start couchbase container:", err)
			return 1
		}
		defer func() { _ = c.Terminate(ctx) }()
		if cfg.ConnStr, err = c.ConnectionString(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		cfg.Username, cfg.Password = c.Username(), c.Password()
	}
	s, err := Open(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open store:", err)
		return 1
	}
	defer func() { _ = s.Close() }()
	// Leave nothing behind: the throwaway scope (and all its test documents) is dropped at the end.
	defer func() {
		if err := s.bucket.CollectionsV2().DropScope(cfg.Scope, &gocb.DropScopeOptions{Context: ctx}); err != nil {
			fmt.Fprintln(os.Stderr, "drop test scope", cfg.Scope, ":", err)
		}
	}()
	if _, err := s.Provision(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "provision:", err)
		return 1
	}
	shared = s
	return m.Run()
}

func TestCBStore_Contract(t *testing.T) {
	storetest.Run(t, func(_ *testing.T, clock store.Clock) store.Store { return shared.WithClock(clock) })
}

func TestProvision_AC1_Idempotent(t *testing.T) {
	ctx := context.Background()
	res, err := shared.Provision(ctx)
	require.NoError(t, err)
	require.Empty(t, res.Applied, "second run applies nothing")
	v, err := env{shared}.CurrentVersion(ctx)
	require.NoError(t, err)
	steps := provision.Steps()
	require.Equal(t, steps[len(steps)-1].Version, v, "at the latest schema version")
}

func TestPing_AC4_MetadataReachable(t *testing.T) {
	require.NoError(t, shared.Ping(context.Background()))
}
