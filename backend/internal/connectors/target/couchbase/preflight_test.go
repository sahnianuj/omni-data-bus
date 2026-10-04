package couchbase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// memKS is an in-memory keyspace browser: bucket.scope.collection → document count.
// PreflightKeyspaces checks locations concurrently, so every method holds mu.
type memKS struct {
	mu        sync.Mutex
	colls     map[string]int64
	scopes    map[string]bool
	buckets   []target.BucketInfo
	created   []string
	createErr error
}

func (m *memKS) ListBuckets(context.Context) (target.BucketList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return target.BucketList{Buckets: m.buckets}, nil
}

func (m *memKS) ListScopes(_ context.Context, bucket string) ([]target.ScopeInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []target.ScopeInfo
	for s := range m.scopes {
		if b, name, _ := cut(s); b == bucket {
			si := target.ScopeInfo{Name: name}
			for c := range m.colls {
				if cb, rest, _ := cut(c); cb == bucket {
					if sc, cn, _ := cut(rest); sc == name {
						si.Collections = append(si.Collections, target.CollectionInfo{Name: cn})
					}
				}
			}
			out = append(out, si)
		}
	}
	return out, nil
}

func cut(s string) (string, string, bool) {
	for i := range len(s) {
		if s[i] == '.' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func (m *memKS) Collection(_ context.Context, b, s, c string) (target.CollectionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.colls[b+"."+s+"."+c]
	if !ok {
		return target.CollectionInfo{}, problem.New(http.StatusNotFound, problem.CodeNotFound, "Collection not found", "")
	}
	return target.CollectionInfo{Name: c, DocCountEstimate: &n}, nil
}
func (m *memKS) CreateBucket(context.Context, target.BucketSpec) error { return nil }
func (m *memKS) CreateScope(_ context.Context, b, s string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scopes[b+"."+s] = true
	m.created = append(m.created, "scope "+b+"."+s)
	return nil
}

func (m *memKS) CreateCollection(_ context.Context, b, s string, spec target.CollectionSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	m.colls[b+"."+s+"."+spec.Name] = 0
	m.created = append(m.created, "collection "+b+"."+s+"."+spec.Name)
	return nil
}

func (m *memKS) SampleDocuments(context.Context, string, string, string, int) ([]json.RawMessage, string, error) {
	return nil, "", nil
}

func (m *memKS) ExistingKeys(context.Context, string, string, string, []string) (map[string]bool, error) {
	return nil, nil
}

func loc(b, s, c string) target.Location {
	return target.Location{Kind: target.KindCouchbase, Bucket: b, Scope: s, Collection: c}
}

func TestPreflight_AC2_CouchbaseChecksMoved(t *testing.T) {
	ctx := context.Background()
	ks := &memKS{colls: map[string]int64{"shop.sales.customer": 1200, "shop.sales.empty": 0}, scopes: map[string]bool{"shop.sales": true}}
	res := PreflightKeyspaces(ctx, ks, []target.PreflightLocation{
		{MappingID: "m1", Location: loc("shop", "sales", "customer"), WriteMode: "insert"},
		{MappingID: "m2", Location: loc("shop", "sales", "empty")},
		{MappingID: "m3", Location: loc("shop", "archive", "orders"), CreateIfMissing: true},
		{MappingID: "m4", Location: loc("shop", "sales", "missing")},
	})
	require.Len(t, res, 4)

	// An existing, non-empty collection: ok + a warning (insert mode says so).
	require.Equal(t, []target.PreflightCheck{
		{ID: "keyspace", Label: "target shop.sales.customer", Status: "ok", MappingID: "m1"},
		{
			ID: "empty", Label: "target collection is empty", Status: "warn", Code: WarnInsertExisting, MappingID: "m1",
			Detail: "shop.sales.customer already holds about 1200 documents. Insert mode skips documents whose keys already exist.",
		},
	}, res[0].Checks)
	require.False(t, res[0].Empty)
	require.True(t, res[1].Empty)
	require.Len(t, res[1].Checks, 1)

	// A missing collection with createIfMissing is created, scope first.
	require.Equal(t, "ok", res[2].Checks[0].Status)
	require.Equal(t, "Created shop.archive.orders.", res[2].Checks[0].Detail)
	require.True(t, res[2].Empty)
	require.Equal(t, []string{"scope shop.archive", "collection shop.archive.orders"}, ks.created)

	// Without it, the check fails with TARGET_KEYSPACE_MISSING.
	require.Equal(t, "fail", res[3].Checks[0].Status)
	require.Equal(t, target.CodeKeyspaceMissing, res[3].Checks[0].Code)

	// A creation error fails the check with a hint.
	ks.createErr = errors.New("forbidden")
	res = PreflightKeyspaces(ctx, ks, []target.PreflightLocation{{Location: loc("shop", "x", "y"), CreateIfMissing: true}})
	require.Equal(t, "fail", res[0].Checks[0].Status)
	require.Contains(t, res[0].Checks[0].Detail, "forbidden")
	require.NotEmpty(t, res[0].Checks[0].Hint)
}

func TestLocations_BrowseStatCreate(t *testing.T) {
	ctx := context.Background()
	ks := &memKS{
		buckets: []target.BucketInfo{{Name: "shop", CanManage: true}},
		colls:   map[string]int64{"shop.sales.orders": 7}, scopes: map[string]bool{"shop.sales": true},
	}
	nodes, err := ListKeyspaceLocations(ctx, ks, target.Location{})
	require.NoError(t, err)
	require.Equal(t, []target.LocationNode{{Name: "shop", Level: "bucket", Path: "shop", Location: target.Location{Kind: "couchbase", Bucket: "shop"}, CanCreate: true}}, nodes)

	nodes, err = ListKeyspaceLocations(ctx, ks, target.Location{Bucket: "shop"})
	require.NoError(t, err)
	require.Equal(t, "sales", nodes[0].Name)
	require.Equal(t, "shop/sales", nodes[0].Path)

	nodes, err = ListKeyspaceLocations(ctx, ks, target.Location{Bucket: "shop", Scope: "sales"})
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.True(t, nodes[0].Leaf)
	require.Equal(t, loc("shop", "sales", "orders"), nodes[0].Location)

	_, err = ListKeyspaceLocations(ctx, ks, target.Location{Bucket: "shop", Scope: "nope"})
	require.True(t, problem.HasCode(err, problem.CodeNotFound))
	nodes, err = ListKeyspaceLocations(ctx, ks, loc("shop", "sales", "orders"))
	require.NoError(t, err)
	require.Empty(t, nodes)

	n, err := StatKeyspaceLocation(ctx, ks, loc("shop", "sales", "orders"))
	require.NoError(t, err)
	require.EqualValues(t, 7, *n.DocCount)
	_, err = StatKeyspaceLocation(ctx, ks, target.Location{Bucket: "shop"})
	require.True(t, problem.HasCode(err, problem.CodeLocationInvalid))

	require.NoError(t, EnsureCollection(ctx, ks, loc("shop", "sales", "items")))
	require.Equal(t, []string{"collection shop.sales.items"}, ks.created, "an existing scope isn't re-created")
	require.True(t, problem.HasCode(New().CreateLocation(ctx, target.Conn{}, target.Location{Bucket: "shop"}), problem.CodeLocationInvalid))
}

func TestConnectorPorts_CapabilitiesAndOpenFailures(t *testing.T) {
	ctx := context.Background()
	c := New()
	caps := c.Capabilities()
	require.True(t, caps.SupportsDurability && caps.SupportsExpiry && caps.RequiresCredentials)
	require.EqualValues(t, 20<<20, caps.MaxDocBytes)

	// A config that doesn't parse fails every port before any connection is made.
	bad := target.Conn{ID: "t", Version: "1", Config: json.RawMessage(`{"connectionString":""}`)}
	_, err := c.ListLocations(ctx, bad, target.Location{})
	require.Error(t, err)
	_, err = c.StatLocation(ctx, bad, loc("b", "s", "c"))
	require.Error(t, err)
	require.Error(t, c.CreateLocation(ctx, bad, loc("b", "s", "c")))
	res := c.Preflight(ctx, bad, []target.PreflightLocation{{MappingID: "m1", Location: loc("b", "s", "c")}})
	require.Len(t, res, 1)
	require.Equal(t, "fail", res[0].Checks[0].Status)
	require.Equal(t, "m1", res[0].Checks[0].MappingID)
	require.Equal(t, "target b.s.c", res[0].Checks[0].Label)

	require.Equal(t, "plain", errMessage(errors.New("plain")))
	require.Equal(t, "Title only", errMessage(problemer{problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Title only", "")}))
	require.Equal(t, "the detail", errMessage(problemer{problem.New(http.StatusBadRequest, problem.CodeBadRequest, "T", "the detail")}))
}

// problemer is a domain error that knows its problem (like connectors.ConnError).
type problemer struct{ p problem.Problem }

func (e problemer) Error() string            { return e.p.Error() }
func (e problemer) Problem() problem.Problem { return e.p }
