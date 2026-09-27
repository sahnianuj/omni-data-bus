//go:build integration

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

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
)

// countingWriter counts written documents so the test can "kill the worker" part-way.
type countingWriter struct {
	target.DocWriter
	n *atomic.Int64
}

func (w countingWriter) WriteBatch(ctx context.Context, docs []target.Doc) []target.WriteResult {
	res := w.DocWriter.WriteBatch(ctx, docs)
	w.n.Add(int64(len(docs)))
	return res
}

// TestCrashRecovery_AC4_Integration_KillWorker aborts a real PostgreSQL → Couchbase run
// without draining (as a killed worker would), then resumes a new runner from the persisted
// checkpoint: the collection ends with exactly the source's rows.
func TestCrashRecovery_AC4_Integration_KillWorker(t *testing.T) {
	ctx := context.Background()
	pgCfg, pgPass, cbCfg, cbPass, cl := e2eEnv(t)
	pgSess, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
	require.NoError(t, err)
	defer func() { _ = pgSess.Close() }()
	snap, err := pgSess.Discover(ctx, source.DiscoverOptions{Schemas: []string{"shop"}})
	require.NoError(t, err)

	const table = "big_events"
	scope := fmt.Sprintf("it_crash_%d", time.Now().UnixNano()%1_000_000_000)
	mgr := cl.Bucket("sandbox").CollectionsV2()
	require.NoError(t, mgr.CreateScope(scope, nil))
	t.Cleanup(func() { _ = mgr.DropScope(scope, nil) })
	require.NoError(t, mgr.CreateCollection(scope, table, nil, nil))
	c := cl.Bucket("sandbox").Scope(scope).Collection(table)
	require.Eventually(t, func() bool {
		_, err := c.Upsert("probe", map[string]int{}, nil)
		return err == nil
	}, 60*time.Second, 200*time.Millisecond)
	_, _ = c.Remove("probe", nil)

	st := memstore.New(nil)
	p, err := st.Projects().Create(ctx, "ws", store.NewProject{Name: "crash"})
	require.NoError(t, err)
	raw, _ := json.Marshal(specFor(snap.Table("shop", table)))
	m, err := st.Mappings().Create(ctx, "ws", store.Mapping{
		ProjectID: p.ID, SourceSchema: "shop", SourceTable: table, Spec: raw, Status: "ready",
		Target: store.MappingTarget{Bucket: "sandbox", Scope: scope, Collection: table},
	})
	require.NoError(t, err)
	opts := jobs.DefaultOptions()
	opts.MappingIDs = []string{m.ID}
	optRaw, _ := json.Marshal(opts)
	job, err := st.Jobs().Create(ctx, "ws", store.Job{ProjectID: p.ID, Options: optRaw, MappingIDs: opts.MappingIDs})
	require.NoError(t, err)

	var written atomic.Int64
	newRunner := func() *Runner {
		return NewRunner(Deps{
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
				return countingWriter{w, &written}, nil
			},
			Latest: func(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
				return store.DiscoverySnapshot{}, snap, nil
			},
			PersistEvery: time.Second,
		})
	}

	// Kill mid-run: abort (no drain), and forget the run's own final checkpoint.
	total := exactCount(t, pgCfg, pgPass, table)
	abort := make(chan struct{})
	go func() {
		for written.Load() < total/3 {
			time.Sleep(5 * time.Millisecond)
		}
		close(abort)
	}()
	o := newRunner().Start(ctx, job, jobs.Control{Drain: make(chan struct{}), Abort: abort}).Wait()
	require.False(t, o.Finished)
	stored, err := st.Jobs().GetAny(ctx, job.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stored.Checkpoint, "the run persisted checkpoints while it ran")
	cp, err := ParseCheckpoint(stored.Checkpoint)
	require.NoError(t, err)
	t.Logf("killed after %d writes; the persisted checkpoint covers %d rows", written.Load(), cp.Mappings[m.ID].Processed())

	// A new worker resumes from the persisted checkpoint.
	o = newRunner().Start(ctx, stored, jobs.Control{Drain: make(chan struct{}), Abort: make(chan struct{})}).Wait()
	require.Nil(t, o.Fatal)
	require.True(t, o.Finished)
	if o.Failed > 0 {
		errs, _ := st.Jobs().SummarizeErrors(ctx, "ws", job.ID)
		t.Logf("failed rows: %d %+v", o.Failed, errs)
	}
	require.Zero(t, o.Failed)
	// A KV range scan reads the persisted snapshot, so wait for the last writes to reach disk.
	var n int64
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		if n = countDocs(t, cl, scope, table); n == total {
			break
		}
	}
	require.Equal(t, total, n, "every source row is in Couchbase, none lost")
}
