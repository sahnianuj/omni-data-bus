//go:build integration

package couchbase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// OC-013 / OC-014 against a real cluster: a Couchbase Server 8.0.3 container by default, or
// OC_IT_TARGET_CONNSTR/_USERNAME/_PASSWORD (e.g. the dev stack). Capella clusters are only
// browsed: nothing is created there.
func TestCreateCollection_AC2_Integration(t *testing.T) {
	ctx := context.Background()
	connStr, user, pass := os.Getenv("OC_IT_TARGET_CONNSTR"), os.Getenv("OC_IT_TARGET_USERNAME"), os.Getenv("OC_IT_TARGET_PASSWORD")
	bucket := "sandbox"
	if connStr == "" {
		c := sharedContainer(t)
		connStr, user, pass = c.connStr, c.user, c.pass
	}
	capella := strings.Contains(connStr, "cloud.couchbase.com")
	deployment := SelfManaged
	if capella {
		deployment = Capella
	}
	cfg, _ := json.Marshal(map[string]any{"connectionString": connStr, "username": user, "deployment": deployment, "connectTimeoutMs": 30000})
	conn := New()
	t.Cleanup(func() { _ = conn.Close(ctx) })
	ks, err := conn.Keyspaces(ctx, "it::target", "1", cfg, connectors.Credentials{Password: pass})
	require.NoError(t, err)

	list, err := ks.ListBuckets(ctx)
	require.NoError(t, err)
	t.Logf("buckets (partial=%v): %+v", list.Partial, list.Buckets)
	require.NotEmpty(t, list.Buckets)
	if capella {
		b := list.Buckets[0].Name
		scopes, err := ks.ListScopes(ctx, b)
		require.NoError(t, err)
		require.Equal(t, "_default", scopes[0].Name)
		require.True(t, problem.HasCode(ks.CreateBucket(ctx, target.BucketSpec{Name: "x", RAMQuotaMB: 256, BucketType: "couchbase", StorageBackend: "couchstore"}), problem.CodeCapellaBucketCreate))
		return
	}

	scope := fmt.Sprintf("it_%d", time.Now().UnixNano()%1_000_000_000)
	t.Cleanup(func() {
		cl, err := gocb.Connect(connStr, gocb.ClusterOptions{Authenticator: gocb.PasswordAuthenticator{Username: user, Password: pass}})
		if err == nil {
			_ = cl.Bucket(bucket).CollectionsV2().DropScope(scope, nil)
			_ = cl.Close(nil)
		}
	})
	start := time.Now()
	require.NoError(t, ks.CreateScope(ctx, bucket, scope))
	if os.Getenv("OC_IT_CB_EDITION") == "community" {
		// OC-072: Community Edition has no collection TTL; the refusal says so in plain words.
		err := ks.CreateCollection(ctx, bucket, scope, target.CollectionSpec{Name: "orders", MaxExpirySec: 3600})
		require.True(t, problem.HasCode(err, problem.CodeValidation), "%v", err)
		require.ErrorContains(t, err, "Community Edition")
		require.NoError(t, ks.CreateCollection(ctx, bucket, scope, target.CollectionSpec{Name: "orders"}))
	} else {
		require.NoError(t, ks.CreateCollection(ctx, bucket, scope, target.CollectionSpec{Name: "orders", MaxExpirySec: 3600}))
	}
	t.Logf("scope + collection ready in %s", time.Since(start))

	err = ks.CreateCollection(ctx, bucket, scope, target.CollectionSpec{Name: "orders"})
	require.True(t, problem.HasCode(err, problem.CodeKeyspaceExists), "%v", err)
	require.True(t, problem.HasCode(ks.CreateScope(ctx, bucket, scope), problem.CodeKeyspaceExists))

	scopes, err := ks.ListScopes(ctx, bucket)
	require.NoError(t, err)
	var found *target.ScopeInfo
	for i := range scopes {
		if scopes[i].Name == scope {
			found = &scopes[i]
		}
	}
	require.NotNil(t, found)
	require.Len(t, found.Collections, 1)
	require.Equal(t, "orders", found.Collections[0].Name)
	wantTTL := int64(3600)
	if os.Getenv("OC_IT_CB_EDITION") == "community" {
		wantTTL = 0
	}
	require.Equal(t, wantTTL, found.Collections[0].MaxExpirySec)

	c, err := ks.Collection(ctx, bucket, scope, "orders")
	require.NoError(t, err)
	if c.DocCountEstimate != nil {
		require.Equal(t, int64(0), *c.DocCountEstimate)
	}
	_, err = ks.ListScopes(ctx, "no-such-bucket")
	require.True(t, problem.HasCode(err, problem.CodeNotFound), "%v", err)
}
