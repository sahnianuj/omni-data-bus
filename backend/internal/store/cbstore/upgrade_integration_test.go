//go:build integration

package cbstore

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/couchbase/gocb/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/provision"
)

// oneZero opens a store on a fresh scope provisioned the way 1.0 left it (steps 1–3), and drops
// the scope at the end.
func oneZero(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	cfg := shared.cfg
	cfg.Scope = "it_up_" + uuid.NewString()[:8]
	s, err := Open(ctx, cfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = s.bucket.CollectionsV2().DropScope(cfg.Scope, &gocb.DropScopeOptions{Context: ctx})
		_ = s.Close()
	})
	res, err := provision.Run(ctx, env{s}, provision.Steps()[:3])
	require.NoError(t, err)
	require.Equal(t, 3, res.To, "1.0's provisioning")
	return s
}

// TestProvision_AC1_UpgradeFrom1_0 (OC-057): `odb admin migrate` on a 1.0 store adds the 2.0
// collections and indexes, records the new version, and a second run changes nothing.
func TestProvision_AC1_UpgradeFrom1_0(t *testing.T) {
	ctx := context.Background()
	s := oneZero(t)
	exists := func(c string) bool {
		ok, err := env{s}.collectionExists(ctx, s.cfg.Scope, c)
		require.NoError(t, err)
		return ok
	}
	require.False(t, exists("relationships"))

	res, err := s.Provision(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, res.From)
	require.Equal(t, len(provision.Steps()), res.To)
	require.Equal(t, []string{"4 relationship collections", "5 export collections", "6 claim index"}, res.Applied)
	for _, c := range []string{"relationships", "exports"} {
		require.True(t, exists(c), c)
	}
	v, err := env{s}.CurrentVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, len(provision.Steps()), v)
	rows, err := s.cluster.Query("SELECT RAW name FROM system:indexes WHERE bucket_id = $b AND scope_id = $s AND keyspace_id = 'jobs'",
		&gocb.QueryOptions{Context: ctx, NamedParameters: map[string]any{"b": s.cfg.Bucket, "s": s.cfg.Scope}})
	require.NoError(t, err)
	var names []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Row(&n))
		names = append(names, n)
	}
	require.Contains(t, names, "idx_jobs_claim")

	again, err := s.Provision(ctx)
	require.NoError(t, err)
	require.Empty(t, again.Applied, "a second run makes no changes")
}

// TestUpgrade_AC2_DataPreserved (OC-057): projects, a 1.0 mapping with embeds and jobs written
// before the upgrade are all listed and readable after it, and pre-2.0 queued jobs stay
// claimable by every worker release.
func TestUpgrade_AC2_DataPreserved(t *testing.T) {
	ctx := context.Background()
	s := oneZero(t)
	ws := store.WorkspaceID("ws-upgrade")
	p, err := s.Projects().Create(ctx, ws, store.NewProject{Name: "From 1.0"})
	require.NoError(t, err)
	v1, err := os.ReadFile("../../mapping/testdata/specs/v1/customer_address_orders.json")
	require.NoError(t, err)
	m, err := s.Mappings().Create(ctx, ws, store.Mapping{
		ProjectID: p.ID, SourceSchema: "shop", SourceTable: "customer", Spec: v1, Status: "ready",
		Target: store.MappingTarget{Bucket: "sandbox", Scope: "s", Collection: "customer"},
	})
	require.NoError(t, err)
	done, err := s.Jobs().Create(ctx, ws, store.Job{ProjectID: p.ID, Status: store.JobCompleted, Options: json.RawMessage(`{}`), MappingIDs: []string{m.ID}})
	require.NoError(t, err)
	queued, err := s.Jobs().Create(ctx, ws, store.Job{ProjectID: p.ID, Options: json.RawMessage(`{}`), MappingIDs: []string{m.ID}})
	require.NoError(t, err)

	_, err = s.Provision(ctx)
	require.NoError(t, err)

	projects, _, err := s.Projects().List(ctx, ws, store.Page{})
	require.NoError(t, err)
	require.Len(t, projects, 1)
	got, err := s.Mappings().Get(ctx, ws, m.ID)
	require.NoError(t, err)
	require.Equal(t, 1, mapping.VersionOf(got.Spec), "no bulk rewrite: v1 specs upgrade on read")
	var spec mapping.Spec
	require.NoError(t, json.Unmarshal(got.Spec, &spec))
	require.Len(t, spec.Relations, 2, "the 1.0 embeds")
	require.Equal(t, "customer", got.Where().Collection)
	jobs, _, err := s.Jobs().List(ctx, ws, p.ID, store.Page{})
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	j, err := s.Jobs().Get(ctx, ws, done.ID)
	require.NoError(t, err)
	require.Equal(t, store.JobCompleted, j.Status)
	claimable, err := s.Jobs().ListClaimable(ctx, 1000001, 100)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	require.Equal(t, queued.ID, claimable[0].ID, "a pre-2.0 job: any release may run it")
}
