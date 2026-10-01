//go:build integration

package pipeline

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

// TestDirectoryJob_AC4_CrashResume (OC-040): a PostgreSQL → directory run is killed a third of
// the way (no drain), the next preflight cleans the temp files it left, and a new runner resumes
// from the checkpoint: one valid file per source row, a complete manifest, no temp files.
func TestDirectoryJob_AC4_CrashResume(t *testing.T) {
	ctx := context.Background()
	pgCfg, pgPass := pgOnlyEnv(t)
	sess, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()
	snap, err := sess.Discover(ctx, source.DiscoverOptions{Schemas: []string{"shop"}})
	require.NoError(t, err)
	const table = "Order Details"

	root := filepath.Join(t.TempDir(), "odb-out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "order_details"), 0o750))
	conn := directory.New([]string{root})
	cfg, _ := json.Marshal(map[string]any{"path": root, "durability": "none"})

	st := memstore.New(nil)
	p, err := st.Projects().Create(ctx, "ws", store.NewProject{Name: "dir-crash"})
	require.NoError(t, err)
	raw, _ := json.Marshal(specFor(snap.Table("shop", table)))
	nm := store.Mapping{ProjectID: p.ID, SourceSchema: "shop", SourceTable: table, Spec: raw, Status: "ready"}
	nm.SetLocation(store.Location{Kind: store.KindDirectory, Folder: "order_details"})
	m, err := st.Mappings().Create(ctx, "ws", nm)
	require.NoError(t, err)
	opts := jobs.DefaultOptions()
	opts.MappingIDs, opts.BatchSize = []string{m.ID}, 100
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
				w, err := conn.OpenWriter(ctx, cfg, connectors.Credentials{}, o)
				if err != nil {
					return nil, err
				}
				return countingWriter{w, &written}, nil
			},
			Latest: func(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
				return store.DiscoverySnapshot{}, snap, nil
			},
			PersistEvery: 200 * time.Millisecond, PersistBatches: 5,
		})
	}

	total := exactCount(t, pgCfg, pgPass, table)
	abort := make(chan struct{})
	go func() {
		for written.Load() < total/3 {
			time.Sleep(2 * time.Millisecond)
		}
		close(abort)
	}()
	o := newRunner().Start(ctx, job, jobs.Control{Drain: make(chan struct{}), Abort: abort}).Wait()
	require.False(t, o.Finished)
	stored, err := st.Jobs().GetAny(ctx, job.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stored.Checkpoint)

	// The next preflight removes temp files the killed run left.
	time.Sleep(10 * time.Millisecond)
	conn.Preflight(ctx, target.Conn{Config: cfg}, []target.PreflightLocation{{MappingID: m.ID, Location: m.Where()}})
	tmps, _ := filepath.Glob(filepath.Join(root, "order_details", ".odb-tmp", "*.tmp"))
	require.Empty(t, tmps)

	o = newRunner().Start(ctx, stored, jobs.Control{Drain: make(chan struct{}), Abort: make(chan struct{})}).Wait()
	require.Nil(t, o.Fatal)
	require.True(t, o.Finished)
	require.Zero(t, o.Failed)

	var files int64
	require.NoError(t, filepath.WalkDir(filepath.Join(root, "order_details"), func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		require.True(t, strings.HasSuffix(p, ".json"), p)
		b, err := os.ReadFile(p) //nolint:gosec // test
		require.NoError(t, err)
		require.True(t, json.Valid(b), p)
		files++
		return nil
	}))
	require.Equal(t, total, files, "exactly one file per source row")
	lines, err := directory.ReadManifest(root, m.ID)
	require.NoError(t, err)
	require.EqualValues(t, total, len(lines), "the manifest lists every document once")
	tmps, _ = filepath.Glob(filepath.Join(root, "order_details", ".odb-tmp", "*"))
	require.Empty(t, tmps)
	info, err := directory.ReadRunInfo(root)
	require.NoError(t, err)
	require.Equal(t, m.ID, info.Mappings["order_details"].MappingID)
	t.Logf("%d files, manifest %d lines, killed after %d writes", files, len(lines), written.Load())
}

// pgOnlyEnv is the dev stack's PostgreSQL alone (no Couchbase needed).
func pgOnlyEnv(t *testing.T) (json.RawMessage, string) {
	t.Helper()
	host := os.Getenv("OC_IT_PG_HOST")
	if host == "" {
		t.Skip("set OC_IT_PG_HOST/PORT (the dev stack)")
	}
	port := 5432
	if v := os.Getenv("OC_IT_PG_PORT"); v != "" {
		_ = json.Unmarshal([]byte(v), &port)
	}
	cfg, _ := json.Marshal(map[string]any{"host": host, "port": port, "database": "shop", "username": "shop_owner", "sslMode": "disable"})
	return cfg, "shop_owner"
}
