package couchbase

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

func TestCreateCollection_AC2_WaitsUntilReady(t *testing.T) {
	f := admin()
	f.manifestLag = 1
	f.probeErrs = []error{gocb.ErrCollectionNotFound, gocb.ErrCollectionNotFound, gocb.ErrCollectionNotFound}
	var slept []time.Duration
	k := browser(f, SelfManaged)
	k.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }

	require.NoError(t, k.CreateCollection(ctx, "travel-sample", "inventory", target.CollectionSpec{Name: "orders", MaxExpirySec: 60}))
	creates := 0
	for _, c := range f.calls {
		if c == "CreateCollection:orders" {
			creates++
		}
	}
	require.Equal(t, 1, creates, "the SDK create is called once")
	require.Equal(t, []time.Duration{100 * time.Millisecond, 150 * time.Millisecond, 225 * time.Millisecond, 337500 * time.Microsecond}, slept,
		"backoff ×1.5: one wait for the manifest, three for the probe")
	require.Equal(t, time.Minute, f.scopes["travel-sample"][0].Collections[2].MaxExpiry)
}

func TestCreateCollection_AC2_TimeoutPending(t *testing.T) {
	f := admin()
	f.probeErrs = make([]error, 1000)
	for i := range f.probeErrs {
		f.probeErrs[i] = gocb.ErrUnambiguousTimeout
	}
	err := browser(f, SelfManaged).CreateCollection(ctx, "travel-sample", "inventory", target.CollectionSpec{Name: "orders"})
	require.ErrorIs(t, err, target.ErrPending)

	// A cancelled request also stops waiting (the collection may still appear).
	k := browser(admin(), SelfManaged)
	k.api.(*fakeAdmin).manifestLag = 5
	k.sleep = func(context.Context, time.Duration) error { return context.Canceled }
	require.ErrorIs(t, k.CreateScope(ctx, "travel-sample", "late"), target.ErrPending)
}

func TestCreateScope_AC2_CallsSDK(t *testing.T) {
	f := admin()
	require.NoError(t, browser(f, SelfManaged).CreateScope(ctx, "travel-sample", "tenant_2"))
	require.Contains(t, f.calls, "CreateScope:tenant_2")
	scopes, err := browser(f, SelfManaged).ListScopes(ctx, "travel-sample")
	require.NoError(t, err)
	require.Len(t, scopes, 4)
}

func TestCreateBucket_AC2_WaitsForKV(t *testing.T) {
	f := admin()
	f.openable["orders"] = true
	spec := target.BucketSpec{Name: "orders", RAMQuotaMB: 256, BucketType: "couchbase", NumReplicas: 1, StorageBackend: "magma"}
	err := browser(f, SelfManaged).CreateBucket(ctx, spec)
	require.True(t, problem.HasCode(err, problem.CodeValidation), "magma needs 1 GB")
	spec.RAMQuotaMB = 1024
	require.NoError(t, browser(f, SelfManaged).CreateBucket(ctx, spec))
	require.Equal(t, gocb.StorageBackendMagma, f.buckets["orders"].StorageBackend)

	eph := target.BucketSpec{Name: "cache2", RAMQuotaMB: 100, BucketType: "ephemeral"}
	require.ErrorIs(t, browser(f, SelfManaged).CreateBucket(ctx, eph), target.ErrPending, "not ready in time")
	require.Equal(t, gocb.EphemeralBucketType, f.buckets["cache2"].BucketType)
}

func TestCreate_AC3_ExistsMapsTo409(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		run  func(k *keyspaces) error
	}{
		{"bucket", gocb.ErrBucketExists, func(k *keyspaces) error {
			return k.CreateBucket(ctx, target.BucketSpec{Name: "travel-sample", RAMQuotaMB: 256, BucketType: "couchbase", StorageBackend: "couchstore"})
		}},
		{"scope", gocb.ErrScopeExists, func(k *keyspaces) error { return k.CreateScope(ctx, "travel-sample", "inventory") }},
		{"collection", gocb.ErrCollectionExists, func(k *keyspaces) error {
			return k.CreateCollection(ctx, "travel-sample", "inventory", target.CollectionSpec{Name: "route"})
		}},
	} {
		f := admin()
		f.createErr = tc.err
		err := tc.run(browser(f, SelfManaged))
		p := problem.From(err)
		require.Equal(t, http.StatusConflict, p.Status, tc.name)
		require.Equal(t, problem.CodeKeyspaceExists, p.Code, tc.name)
	}
}

func TestCreate_AC4_PermissionAndCapellaGuards(t *testing.T) {
	f := admin()
	f.roles = []Role{{Role: "data_writer", Bucket: "travel-sample"}}
	k := browser(f, SelfManaged)
	err := k.CreateCollection(ctx, "travel-sample", "inventory", target.CollectionSpec{Name: "orders"})
	require.True(t, problem.HasCode(err, "TARGET_PERMISSION_DENIED"))
	require.True(t, problem.HasCode(k.CreateScope(ctx, "travel-sample", "s1"), "TARGET_PERMISSION_DENIED"))
	require.True(t, problem.HasCode(k.CreateBucket(ctx, target.BucketSpec{Name: "b", RAMQuotaMB: 256, BucketType: "couchbase", StorageBackend: "couchstore"}), "TARGET_PERMISSION_DENIED"))
	for _, c := range f.calls {
		require.NotContains(t, c, "Create", "the SDK is never called without permission")
	}

	f = admin()
	err = browser(f, Capella).CreateBucket(ctx, target.BucketSpec{Name: "b", RAMQuotaMB: 256, BucketType: "couchbase", StorageBackend: "couchstore"})
	require.True(t, problem.HasCode(err, problem.CodeCapellaBucketCreate))
	require.Empty(t, f.calls)

	f = admin()
	f.rolesErr = errors.New("query down")
	require.True(t, problem.HasCode(browser(f, SelfManaged).CreateScope(ctx, "travel-sample", "s1"), "TARGET_PERMISSION_DENIED"), "unknown roles deny")
}

func TestCreate_ValidationBeforeSDK(t *testing.T) {
	f := admin()
	k := browser(f, SelfManaged)
	require.True(t, problem.HasCode(k.CreateScope(ctx, "travel-sample", "_bad"), problem.CodeValidation))
	require.True(t, problem.HasCode(k.CreateCollection(ctx, "travel-sample", "inventory", target.CollectionSpec{Name: "ok", MaxExpirySec: -5}), problem.CodeValidation))
	require.True(t, problem.HasCode(k.CreateCollection(ctx, "travel-sample", "bad scope", target.CollectionSpec{Name: "ok"}), problem.CodeNotFound))
	require.Empty(t, f.calls)
}

func TestMapErr(t *testing.T) {
	require.NoError(t, mapErr(nil, "bucket", "b"))
	require.True(t, problem.HasCode(mapErr(gocb.ErrScopeNotFound, "scope", "s"), problem.CodeNotFound))
	require.True(t, problem.HasCode(mapErr(gocb.ErrInvalidArgument, "bucket", "b"), problem.CodeValidation))
	require.True(t, problem.HasCode(mapErr(gocb.ErrUnambiguousTimeout, "bucket", "b"), "TARGET_UNREACHABLE"))
}

func TestMissingBucketFailsFast(t *testing.T) {
	f := admin()
	k := browser(f, SelfManaged)
	_, err := k.ListScopes(ctx, "no-such-bucket")
	require.True(t, problem.HasCode(err, problem.CodeNotFound))
	require.True(t, problem.HasCode(k.CreateScope(ctx, "no-such-bucket", "s1"), problem.CodeNotFound))
	require.True(t, problem.HasCode(k.CreateCollection(ctx, "no-such-bucket", "s", target.CollectionSpec{Name: "c"}), problem.CodeNotFound))
	require.NotContains(t, f.calls, "CreateScope:s1")
	require.NotContains(t, f.calls, "GetAllScopes:no-such-bucket")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = k.ListScopes(cancelled, "no-such-bucket")
	require.False(t, problem.HasCode(err, problem.CodeNotFound), "a cancelled request isn't a missing bucket")
}
