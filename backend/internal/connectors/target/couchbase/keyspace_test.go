package couchbase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// fakeAdmin is an in-memory cluster for keyspace tests.
type fakeAdmin struct {
	mu          sync.Mutex
	roles       []Role
	rolesErr    error
	buckets     map[string]gocb.BucketSettings
	bucketsErr  error
	scopes      map[string][]gocb.ScopeSpec // by bucket
	scopesErr   error
	openable    map[string]bool // WaitBucket succeeds for these
	queryRows   map[string][]json.RawMessage
	queryErr    error
	createErr   error
	probeErrs   []error // returned in order by ProbeCollection, then ErrDocumentNotFound
	manifestLag int     // GetAllScopes omits new collections for this many calls
	calls       []string
	lastQuery   string
	version     string
	scanDocs    []json.RawMessage
	scanErr     error
}

func (f *fakeAdmin) record(c string) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
}

func (f *fakeAdmin) GetAllBuckets(context.Context) (map[string]gocb.BucketSettings, error) {
	f.record("GetAllBuckets")
	return f.buckets, f.bucketsErr
}

func (f *fakeAdmin) CreateBucket(_ context.Context, s gocb.CreateBucketSettings) error {
	f.record("CreateBucket:" + s.Name)
	if f.createErr != nil {
		return f.createErr
	}
	f.buckets[s.Name] = s.BucketSettings
	return nil
}

func (f *fakeAdmin) WaitBucket(_ context.Context, b string, _ time.Duration) error {
	f.record("WaitBucket:" + b)
	if f.openable[b] {
		return nil
	}
	return gocb.ErrUnambiguousTimeout
}

func (f *fakeAdmin) GetAllScopes(_ context.Context, b string) ([]gocb.ScopeSpec, error) {
	f.record("GetAllScopes:" + b)
	if f.scopesErr != nil {
		return nil, f.scopesErr
	}
	if f.manifestLag > 0 {
		f.manifestLag--
		return nil, nil
	}
	s, ok := f.scopes[b]
	if !ok {
		return nil, gocb.ErrBucketNotFound
	}
	return s, nil
}

func (f *fakeAdmin) CreateScope(_ context.Context, b, s string) error {
	f.record("CreateScope:" + s)
	if f.createErr != nil {
		return f.createErr
	}
	f.scopes[b] = append(f.scopes[b], gocb.ScopeSpec{Name: s})
	return nil
}

func (f *fakeAdmin) CreateCollection(_ context.Context, b, s, c string, maxExpiry time.Duration) error {
	f.record("CreateCollection:" + c)
	if f.createErr != nil {
		return f.createErr
	}
	for i := range f.scopes[b] {
		if f.scopes[b][i].Name == s {
			f.scopes[b][i].Collections = append(f.scopes[b][i].Collections, gocb.CollectionSpec{Name: c, ScopeName: s, MaxExpiry: maxExpiry})
		}
	}
	return nil
}

func (f *fakeAdmin) ProbeCollection(context.Context, string, string, string) error {
	f.record("Probe")
	if len(f.probeErrs) > 0 {
		err := f.probeErrs[0]
		f.probeErrs = f.probeErrs[1:]
		return err
	}
	return gocb.ErrDocumentNotFound
}

func (f *fakeAdmin) QueryRows(_ context.Context, stmt string, _ map[string]any, _ time.Duration) ([]json.RawMessage, error) {
	f.record("Query")
	f.lastQuery = stmt
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	for prefix, rows := range f.queryRows {
		if strings.HasPrefix(stmt, prefix) {
			return rows, nil
		}
	}
	return nil, errors.New("no such query")
}

func (f *fakeAdmin) MyRoles(context.Context) ([]Role, error) { return f.roles, f.rolesErr }

func (f *fakeAdmin) Version(context.Context) (string, error) {
	if f.version == "" {
		return "", errors.New("unknown")
	}
	return f.version, nil
}

func (f *fakeAdmin) Exists(_ context.Context, _, _, _, key string) (bool, error) {
	f.record("Exists:" + key)
	if f.createErr != nil {
		return false, f.createErr
	}
	return strings.HasPrefix(key, "exists::"), nil
}

func (f *fakeAdmin) SampleScan(context.Context, string, string, string, int) ([]json.RawMessage, error) {
	f.record("SampleScan")
	return f.scanDocs, f.scanErr
}

func admin() *fakeAdmin {
	return &fakeAdmin{
		roles: []Role{{Role: "admin"}},
		buckets: map[string]gocb.BucketSettings{
			"travel-sample": {Name: "travel-sample", RAMQuotaMB: 256, NumReplicas: 1, BucketType: gocb.CouchbaseBucketType, StorageBackend: gocb.StorageBackendCouchstore},
			"Archive":       {Name: "Archive", RAMQuotaMB: 1024, BucketType: gocb.CouchbaseBucketType, StorageBackend: gocb.StorageBackendMagma},
			"cache":         {Name: "cache", RAMQuotaMB: 100, BucketType: gocb.EphemeralBucketType},
		},
		scopes: map[string][]gocb.ScopeSpec{
			"travel-sample": {
				{Name: "inventory", Collections: []gocb.CollectionSpec{{Name: "route"}, {Name: "Airline", MaxExpiry: time.Hour}}},
				{Name: "_system", Collections: []gocb.CollectionSpec{{Name: "_mobile"}}},
				{Name: "_default", Collections: []gocb.CollectionSpec{{Name: "_default"}}},
				{Name: "Tenant", Collections: []gocb.CollectionSpec{}},
			},
		},
		openable:  map[string]bool{"travel-sample": true, "Archive": true, "cache": true},
		queryRows: map[string][]json.RawMessage{},
	}
}

func browser(f *fakeAdmin, deployment string) *keyspaces {
	return &keyspaces{api: f, deployment: deployment, sleep: func(context.Context, time.Duration) error { return nil }}
}

var ctx = context.Background()

func TestListBuckets_AC1_Full(t *testing.T) {
	f := admin()
	list, err := browser(f, SelfManaged).ListBuckets(ctx)
	require.NoError(t, err)
	require.False(t, list.Partial)
	require.Equal(t, []target.BucketInfo{
		{Name: "Archive", Type: "couchbase", RAMQuotaMB: 1024, StorageBackend: "magma", CanManage: true},
		{Name: "cache", Type: "ephemeral", RAMQuotaMB: 100, CanManage: true},
		{Name: "travel-sample", Type: "couchbase", RAMQuotaMB: 256, NumReplicas: 1, StorageBackend: "couchstore", CanManage: true},
	}, list.Buckets, "case-insensitive order")
	var caps Capabilities
	require.NoError(t, json.Unmarshal(list.Capabilities, &caps))
	require.True(t, caps.CanCreateBucket)
}

func TestListBuckets_AC1_FallbackAllowed(t *testing.T) {
	f := admin()
	f.bucketsErr = &gocb.HTTPError{StatusCode: http.StatusForbidden, InnerError: errors.New("forbidden")}
	f.roles = []Role{{Role: "data_writer", Bucket: "app"}, {Role: "data_reader", Bucket: "gone"}}
	f.openable["app"] = true
	list, err := browser(f, SelfManaged).ListBuckets(ctx)
	require.NoError(t, err)
	require.True(t, list.Partial)
	require.Equal(t, []target.BucketInfo{{Name: "app"}}, list.Buckets, "only buckets that open are kept")
	require.NotContains(t, f.calls, "Query", "role-scoped users don't need the catalog")
}

func TestListBuckets_AC1_CatalogFallbackForAllBucketCredentials(t *testing.T) {
	f := admin()
	f.bucketsErr = &gocb.HTTPError{StatusCode: http.StatusForbidden, InnerError: errors.New("forbidden")}
	f.roles = []Role{{Role: "data_writer", Bucket: "*"}} // Capella "all buckets" database credential
	f.queryRows["SELECT RAW b.name FROM system:buckets"] = []json.RawMessage{json.RawMessage(`"orders"`), json.RawMessage(`"sandbox"`)}
	f.openable["orders"], f.openable["sandbox"] = true, true
	list, err := browser(f, Capella).ListBuckets(ctx)
	require.NoError(t, err)
	require.True(t, list.Partial)
	require.Equal(t, []target.BucketInfo{{Name: "orders"}, {Name: "sandbox"}}, list.Buckets)
}

func TestListBuckets_AC1_NothingListable(t *testing.T) {
	f := admin()
	f.bucketsErr = &gocb.HTTPError{StatusCode: http.StatusForbidden, InnerError: errors.New("forbidden")}
	f.roles = []Role{{Role: "data_writer", Bucket: "*"}}
	f.queryErr = errors.New("no catalog access")
	list, err := browser(f, Capella).ListBuckets(ctx)
	require.NoError(t, err)
	require.True(t, list.Partial)
	require.Empty(t, list.Buckets)
	require.NotNil(t, list.Buckets, "[] in JSON: the UI offers manual entry")
}

func TestListScopes_AC2_HidesSystemOrdersDefaultFirst(t *testing.T) {
	scopes, err := browser(admin(), SelfManaged).ListScopes(ctx, "travel-sample")
	require.NoError(t, err)
	var names []string
	for _, s := range scopes {
		names = append(names, s.Name)
	}
	require.Equal(t, []string{"_default", "inventory", "Tenant"}, names)
	require.Equal(t, []target.CollectionInfo{{Name: "Airline", MaxExpirySec: 3600}, {Name: "route"}}, scopes[1].Collections)
	require.NotNil(t, scopes[2].Collections)

	_, err = browser(admin(), SelfManaged).ListScopes(ctx, "missing")
	require.True(t, problem.HasCode(err, problem.CodeNotFound))
	_, err = browser(admin(), SelfManaged).ListScopes(ctx, ".bad")
	require.True(t, problem.HasCode(err, problem.CodeNotFound))
}

func TestListScopes_AC2_CatalogFallback(t *testing.T) {
	f := admin()
	f.scopesErr = &gocb.HTTPError{StatusCode: http.StatusForbidden, InnerError: errors.New("forbidden")}
	f.queryRows["SELECT k.`scope`"] = []json.RawMessage{
		json.RawMessage(`{"s":"inventory","c":"route"}`), json.RawMessage(`{"s":"inventory","c":"airline"}`), json.RawMessage(`{"s":"_default","c":"_default"}`),
	}
	scopes, err := browser(f, Capella).ListScopes(ctx, "travel-sample")
	require.NoError(t, err)
	require.Equal(t, "_default", scopes[0].Name)
	require.Equal(t, []target.CollectionInfo{{Name: "airline"}, {Name: "route"}}, scopes[1].Collections)

	f.queryErr = errors.New("denied")
	_, err = browser(f, Capella).ListScopes(ctx, "travel-sample")
	require.True(t, problem.HasCode(err, "TARGET_PERMISSION_DENIED"))
}

func TestDocCount_AC4_CountAndBestEffort(t *testing.T) {
	f := admin()
	f.queryRows["SELECT RAW COUNT(*)"] = []json.RawMessage{json.RawMessage(`187`)}
	c, err := browser(f, SelfManaged).Collection(ctx, "travel-sample", "inventory", "Airline")
	require.NoError(t, err)
	require.Equal(t, int64(187), *c.DocCountEstimate)
	require.Equal(t, "SELECT RAW COUNT(*) FROM `travel-sample`.`inventory`.`Airline`", f.lastQuery)

	_, err = browser(f, SelfManaged).Collection(ctx, "travel-sample", "inventory", "nope")
	require.True(t, problem.HasCode(err, problem.CodeNotFound))
}

func TestDocCount_AC4_TimeoutReturnsUnknown(t *testing.T) {
	f := admin()
	f.queryErr = gocb.ErrUnambiguousTimeout
	c, err := browser(f, SelfManaged).Collection(ctx, "travel-sample", "inventory", "route")
	require.NoError(t, err, "a failed count is not an error")
	require.Nil(t, c.DocCountEstimate)
}

func TestKeyspaceQuoting_AC5(t *testing.T) {
	ks, err := Keyspace("my-bucket", "s%1", "c")
	require.NoError(t, err)
	require.Equal(t, "`my-bucket`.`s%1`.`c`", ks)
	for _, bad := range [][3]string{{"a`b", "s", "c"}, {"b", "s`", "c"}, {"b", "s", "c`; DROP"}, {"b", "", "c"}} {
		_, err := Keyspace(bad[0], bad[1], bad[2])
		require.Error(t, err, bad)
	}
	_, err = browser(admin(), SelfManaged).Collection(ctx, "a`b", "s", "c")
	require.True(t, problem.HasCode(err, problem.CodeValidation))
}

func TestCanManageBucket(t *testing.T) {
	require.True(t, canManageBucket([]Role{{Role: "cluster_admin"}}, "x"))
	require.True(t, canManageBucket([]Role{{Role: "bucket_admin", Bucket: "x"}}, "x"))
	require.True(t, canManageBucket([]Role{{Role: "scope_admin", Bucket: "*"}}, "x"))
	require.False(t, canManageBucket([]Role{{Role: "bucket_admin", Bucket: "y"}}, "x"))
	require.False(t, canManageBucket([]Role{{Role: "data_writer", Bucket: "x"}}, "x"))
}

// adminCluster is a pooled cluster that also administers keyspaces.
type adminCluster struct {
	*fakeAdmin
	fakeCluster
}

func (a *adminCluster) MyRoles(ctx context.Context) ([]Role, error) { return a.fakeAdmin.MyRoles(ctx) }

func (a *adminCluster) Version(ctx context.Context) (string, error) { return a.fakeAdmin.Version(ctx) }

func TestConnector_KeyspacesArePooledPerVersion(t *testing.T) {
	opened := 0
	c := &Connector{
		connect: func(string, gocb.ClusterOptions) (cluster, error) {
			opened++
			return &adminCluster{fakeAdmin: admin()}, nil
		},
		now: time.Now, pool: NewPool(time.Minute, time.Now),
	}
	cfg := cfgJSON(nil)
	ks, err := c.Keyspaces(ctx, "p1::target", "1", cfg, connectors.Credentials{Password: "pw"})
	require.NoError(t, err)
	list, err := ks.ListBuckets(ctx)
	require.NoError(t, err)
	require.Len(t, list.Buckets, 3)
	_, err = c.Keyspaces(ctx, "p1::target", "1", cfg, connectors.Credentials{Password: "pw"})
	require.NoError(t, err)
	require.Equal(t, 1, opened, "reused while the repository is unchanged")
	_, err = c.Keyspaces(ctx, "p1::target", "2", cfg, connectors.Credentials{Password: "pw"})
	require.NoError(t, err)
	require.Equal(t, 2, opened, "an edited repository reconnects")

	_, err = c.Keyspaces(ctx, "x", "1", json.RawMessage(`{}`), connectors.Credentials{})
	require.Error(t, err, "invalid config")

	// Bootstrap failures are classified and not pooled.
	c.connect = func(string, gocb.ClusterOptions) (cluster, error) {
		return &fakeCluster{readyErr: gocb.ErrUnambiguousTimeout}, nil
	}
	_, err = c.Keyspaces(ctx, "p2::target", "1", cfg, connectors.Credentials{Password: "pw"})
	require.True(t, problem.HasCode(err, "TARGET_UNREACHABLE"))
	c.connect = func(string, gocb.ClusterOptions) (cluster, error) { return nil, gocb.ErrAuthenticationFailure }
	_, err = c.Keyspaces(ctx, "p3::target", "1", cfg, connectors.Credentials{Password: "pw"})
	require.True(t, problem.HasCode(err, "TARGET_AUTH_FAILED"))
	c.connect = func(string, gocb.ClusterOptions) (cluster, error) { return &fakeCluster{}, nil }
	_, err = c.Keyspaces(ctx, "p4::target", "1", cfg, connectors.Credentials{Password: "pw"})
	require.ErrorContains(t, err, "keyspace administration")
}

func TestExistingKeys_AC7(t *testing.T) {
	f := admin()
	keys := []string{"exists::1", "new::2", "exists::3"}
	got, err := browser(f, SelfManaged).ExistingKeys(ctx, "travel-sample", "inventory", "route", keys)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"exists::1": true, "new::2": false, "exists::3": true}, got)
	many := make([]string, 80)
	for i := range many {
		many[i] = "k" + strconv.Itoa(i)
	}
	got, err = browser(f, SelfManaged).ExistingKeys(ctx, "travel-sample", "inventory", "route", many)
	require.NoError(t, err)
	require.Len(t, got, maxExistsChecks, "bounded to 50")
	f.createErr = gocb.ErrUnambiguousTimeout
	_, err = browser(f, SelfManaged).ExistingKeys(ctx, "travel-sample", "inventory", "route", keys)
	require.Error(t, err)
	_, err = browser(f, SelfManaged).ExistingKeys(ctx, "bad`b", "s", "c", keys)
	require.Error(t, err)
}
