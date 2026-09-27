//go:build integration

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/couchbase"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/verify"
)

// tamperWriter changes one document right after its batch is written.
type tamperWriter struct {
	target.DocWriter
	coll *gocb.Collection
	key  string
}

func (w tamperWriter) WriteBatch(ctx context.Context, docs []target.Doc) []target.WriteResult {
	res := w.DocWriter.WriteBatch(ctx, docs)
	for _, d := range docs {
		if d.Key == w.key {
			_, _ = w.coll.Upsert(w.key, map[string]any{"tampered": true}, nil)
		}
	}
	return res
}

// TestVerify_AC3_Integration verifies shop.customer with every row sampled: all documents
// match except the one changed in Couchbase, and the collection count reconciles.
func TestVerify_AC3_Integration(t *testing.T) {
	ctx := context.Background()
	pgCfg, pgPass, cbCfg, cbPass, cl := e2eEnv(t)
	pgSess, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
	require.NoError(t, err)
	defer func() { _ = pgSess.Close() }()
	snap, err := pgSess.Discover(ctx, source.DiscoverOptions{Schemas: []string{"shop"}})
	require.NoError(t, err)
	customer := snap.Table("shop", "customer")

	scope := fmt.Sprintf("it_verify_%d", time.Now().UnixNano()%1_000_000_000)
	mgr := cl.Bucket("sandbox").CollectionsV2()
	require.NoError(t, mgr.CreateScope(scope, nil))
	t.Cleanup(func() { _ = mgr.DropScope(scope, nil) })
	require.NoError(t, mgr.CreateCollection(scope, "customer", nil, nil))
	coll := cl.Bucket("sandbox").Scope(scope).Collection("customer")
	require.Eventually(t, func() bool {
		_, err := coll.Upsert("probe", map[string]int{}, nil)
		return err == nil
	}, 60*time.Second, 200*time.Millisecond)
	_, _ = coll.Remove("probe", nil)

	st := memstore.New(nil)
	p, err := st.Projects().Create(ctx, "ws", store.NewProject{Name: "verify"})
	require.NoError(t, err)
	spec := specFor(customer)
	raw, _ := json.Marshal(spec)
	m, err := st.Mappings().Create(ctx, "ws", store.Mapping{
		ProjectID: p.ID, SourceSchema: "shop", SourceTable: "customer", Spec: raw, Status: "ready",
		Target: store.MappingTarget{Bucket: "sandbox", Scope: scope, Collection: "customer"},
	})
	require.NoError(t, err)
	opts := jobs.DefaultOptions()
	opts.MappingIDs, opts.VerifySampleSize = []string{m.ID}, 10000
	opts.TargetEmpty = map[string]bool{m.ID: true}
	optRaw, _ := json.Marshal(opts)
	job, err := st.Jobs().Create(ctx, "ws", store.Job{ProjectID: p.ID, Options: optRaw, MappingIDs: opts.MappingIDs})
	require.NoError(t, err)
	tampered := "customer::17"

	runner := NewRunner(Deps{
		Store: st,
		OpenSource: func(ctx context.Context, _ store.WorkspaceID, _ string) (source.Session, store.Connection, error) {
			s, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
			return s, store.Connection{}, err
		},
		OpenWriter: func(ctx context.Context, _ store.WorkspaceID, _ string, o target.WriteOptions) (target.DocWriter, error) {
			w, err := couchbase.New().OpenWriter(ctx, cbCfg, connectors.Credentials{Password: cbPass}, o)
			if err != nil {
				return nil, err
			}
			return struct {
				tamperWriter
				target.DocReader
			}{tamperWriter{w, coll, tampered}, w.(target.DocReader)}, nil
		},
		Latest: func(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
			return store.DiscoverySnapshot{}, snap, nil
		},
		CountTarget: func(context.Context, store.WorkspaceID, string, store.MappingTarget) (int64, error) {
			var n int64
			q, err := cl.Query(fmt.Sprintf("SELECT RAW COUNT(*) FROM sandbox.%s.customer", scope), &gocb.QueryOptions{ScanConsistency: gocb.QueryScanConsistencyRequestPlus})
			if err != nil {
				return 0, err
			}
			q.Next()
			return n, q.Row(&n)
		},
	})
	o := runner.Start(ctx, job, jobs.Control{Drain: make(chan struct{}), Abort: make(chan struct{})}).Wait()
	require.Nil(t, o.Fatal)
	require.True(t, o.Finished)

	rawReport, err := st.Jobs().GetReport(ctx, job.ID)
	require.NoError(t, err)
	r := verify.ParseReport(rawReport).Mappings[m.ID]
	total := exactCount(t, pgCfg, pgPass, "customer")
	require.Equal(t, int(total), r.Sample.Checked, "every row sampled")
	require.Equal(t, 1, r.Sample.Mismatched)
	require.Equal(t, tampered, r.Sample.Mismatches[0].Key)
	require.Equal(t, int(total)-1, r.Sample.Matched)
	require.Zero(t, r.Sample.Missing)
	require.True(t, r.Counts.Reconciled)
	require.NotNil(t, r.Counts.TargetCountMatch)
	require.True(t, *r.Counts.TargetCountMatch, "%+v", r.Counts)
	require.Equal(t, verify.PassedWithWarnings, r.Result, "1 mismatch in 2,000 is under 1%")
}
