//go:build integration

package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

// TestMerge_AC3_ResumeFromKey kills a job whose items[] relation is sorted-merged a third of the
// way, then resumes it: the remaining orders are written, the ones already checkpointed are not
// read or written again, and every order ends up with its items.
func TestMerge_AC3_ResumeFromKey(t *testing.T) {
	ctx := context.Background()
	pgCfg, pgPass := pgOnlyEnv(t)
	sch, snap := relSchema(t, pgCfg, pgPass)
	rel := func(s, n string) *schema.Table { return snap.Table(s, n) }
	spec := specFor(snap.Table(sch, "orders"))
	spec, _, _, err := automap.AddRelation(spec, rel, automap.EmbedInput{
		Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: sch, Name: "order_items"}, Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}},
		TargetName: "items", OrderBy: []mapping.OrderColumn{{Column: "line_no"}}, Strategy: mapping.StrategySortedMerge, Generate: true,
	}, docschema.GenerateOptions{})
	require.NoError(t, err)
	for i := range spec.FieldMappings {
		spec.FieldMappings[i].State = mapping.StateActive
	}

	root := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "orders"), 0o750))
	conn := directory.New([]string{root})
	cfg, _ := json.Marshal(map[string]any{"path": root, "durability": "none"})
	st := memstore.New(nil)
	p, err := st.Projects().Create(ctx, "ws", store.NewProject{Name: "merge-resume"})
	require.NoError(t, err)
	raw, _ := json.Marshal(spec)
	nm := store.Mapping{ProjectID: p.ID, SourceSchema: sch, SourceTable: "orders", Spec: raw, Status: "ready"}
	nm.SetLocation(store.Location{Kind: store.KindDirectory, Folder: "orders"})
	m, err := st.Mappings().Create(ctx, "ws", nm)
	require.NoError(t, err)
	opts := jobs.DefaultOptions()
	opts.MappingIDs, opts.BatchSize = []string{m.ID}, 20
	optRaw, _ := json.Marshal(opts)
	job, err := st.Jobs().Create(ctx, "ws", store.Job{ProjectID: p.ID, Options: optRaw, MappingIDs: opts.MappingIDs})
	require.NoError(t, err)

	newRunner := func(written *atomic.Int64) *Runner {
		return NewRunner(Deps{
			Store: st,
			OpenSource: func(ctx context.Context, _ store.WorkspaceID, _ string) (source.Session, store.Connection, error) {
				s, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
				return s, store.Connection{}, err
			},
			OpenWriter: func(ctx context.Context, _ store.WorkspaceID, _ string, o target.WriteOptions) (target.DocWriter, error) {
				w, err := conn.OpenWriter(ctx, cfg, connectors.Credentials{}, o)
				return countingWriter{w, written}, err
			},
			Latest: func(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
				return store.DiscoverySnapshot{}, snap, nil
			},
			PersistEvery: 50 * time.Millisecond, PersistBatches: 1,
		})
	}
	var first atomic.Int64
	abort := make(chan struct{})
	go func() {
		for first.Load() < 100 {
			time.Sleep(time.Millisecond)
		}
		close(abort)
	}()
	o := newRunner(&first).Start(ctx, job, jobs.Control{Drain: make(chan struct{}), Abort: abort}).Wait()
	require.False(t, o.Finished)
	stored, err := st.Jobs().GetAny(ctx, job.ID)
	require.NoError(t, err)
	stored.Checkpoint = o.Checkpoint // what the jobs service saves when the run stops
	cp, err := ParseCheckpoint(o.Checkpoint)
	require.NoError(t, err)
	done := cp.Mappings[m.ID].Written
	require.Positive(t, done)
	require.Less(t, done, int64(300))

	var second atomic.Int64
	o = newRunner(&second).Start(ctx, stored, jobs.Control{Drain: make(chan struct{}), Abort: make(chan struct{})}).Wait()
	require.Nil(t, o.Fatal)
	require.True(t, o.Finished)
	require.Equal(t, 300-done, second.Load(), "only the orders after the checkpoint are read and written again")
	docs := readFolder(t, filepath.Join(root, "orders"))
	require.Len(t, docs, 300)
	for key, doc := range docs {
		var d struct {
			Items []json.RawMessage `json:"items"`
		}
		require.NoError(t, json.Unmarshal(doc, &d))
		require.Len(t, d.Items, 3, key)
	}
}
