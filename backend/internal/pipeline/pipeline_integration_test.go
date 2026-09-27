//go:build integration

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/couchbase"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/naming"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/transform"
)

// The end-to-end run needs the dev stack (make dev-stack): PostgreSQL with the shop seed on
// OC_IT_PG_HOST/PORT, and Couchbase on OC_IT_TARGET_CONNSTR with a "sandbox" bucket.
func e2eEnv(t *testing.T) (pgCfg json.RawMessage, pgPass string, cbCfg json.RawMessage, cbPass string, cb *gocb.Cluster) {
	t.Helper()
	host, cbConn := os.Getenv("OC_IT_PG_HOST"), os.Getenv("OC_IT_TARGET_CONNSTR")
	if host == "" || cbConn == "" {
		t.Skip("set OC_IT_PG_HOST/PORT and OC_IT_TARGET_CONNSTR/_USERNAME/_PASSWORD (the dev stack)")
	}
	port, _ := strconv.Atoi(os.Getenv("OC_IT_PG_PORT"))
	if port == 0 {
		port = 5432
	}
	pgCfg, _ = json.Marshal(map[string]any{"host": host, "port": port, "database": "shop", "username": "shop_owner", "sslMode": "disable"})
	user, pass := os.Getenv("OC_IT_TARGET_USERNAME"), os.Getenv("OC_IT_TARGET_PASSWORD")
	cbCfg, _ = json.Marshal(map[string]any{"connectionString": cbConn, "username": user, "deployment": "self-managed", "connectTimeoutMs": 30000, "kvTimeoutMs": 10000})
	cl, err := gocb.Connect(cbConn, gocb.ClusterOptions{Authenticator: gocb.PasswordAuthenticator{Username: user, Password: pass}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close(nil) })
	return pgCfg, "shop_owner", cbCfg, pass, cl
}

// specFor designs a document from the table and auto-maps it, like a new mapping in the app.
func specFor(t *schema.Table) mapping.Spec {
	tpl, _ := mapping.DefaultKeyTemplate(t)
	spec := mapping.Spec{
		DocSchema: docschema.FromTable(t, docschema.GenerateOptions{}), Key: mapping.KeySpec{Template: tpl},
		TypeField: mapping.TypeField{Enabled: true, Name: "docType", Value: naming.SingularSnake(t.Name)}, NullHandling: mapping.NullInclude,
	}
	spec.Normalize()
	spec.FieldMappings = automap.Run(&spec, automap.Columns(t), automap.Options{}).FieldMappings
	for i := range spec.FieldMappings {
		spec.FieldMappings[i].State = mapping.StateActive // accept suggestions too
	}
	spec.Normalize()
	return spec
}

func TestPipeline_AC8_Integration_EndToEnd(t *testing.T) {
	ctx := context.Background()
	pgCfg, pgPass, cbCfg, cbPass, cl := e2eEnv(t)
	pgSess, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
	require.NoError(t, err)
	defer func() { _ = pgSess.Close() }()
	snap, err := pgSess.Discover(ctx, source.DiscoverOptions{Schemas: []string{"shop"}})
	require.NoError(t, err)

	// A throwaway scope in the sandbox bucket, one collection per table.
	scope := fmt.Sprintf("e2e_%d", time.Now().UnixNano()%1_000_000_000)
	mgr := cl.Bucket("sandbox").CollectionsV2()
	require.NoError(t, mgr.CreateScope(scope, nil))
	t.Cleanup(func() { _ = mgr.DropScope(scope, nil) })
	tables := []string{"customer", "big_events"}
	if os.Getenv("OC_IT_SKIP_BIG") != "" {
		tables = tables[:1]
	}
	for _, tb := range tables {
		require.NoError(t, mgr.CreateCollection(scope, tb, nil, nil))
	}
	for _, tb := range tables {
		c := cl.Bucket("sandbox").Scope(scope).Collection(tb)
		require.Eventually(t, func() bool {
			_, err := c.Upsert("probe", map[string]int{}, nil)
			return err == nil
		}, 60*time.Second, 200*time.Millisecond)
		_, _ = c.Remove("probe", nil)
	}

	st := memstore.New(nil)
	p, err := st.Projects().Create(ctx, "ws", store.NewProject{Name: "e2e"})
	require.NoError(t, err)
	var ids []string
	specs := map[string]mapping.Spec{}
	for _, tb := range tables {
		spec := specFor(snap.Table("shop", tb))
		raw, _ := json.Marshal(spec)
		m, err := st.Mappings().Create(ctx, "ws", store.Mapping{
			ProjectID: p.ID, SourceSchema: "shop", SourceTable: tb, Spec: raw, Status: "ready",
			Target: store.MappingTarget{Bucket: "sandbox", Scope: scope, Collection: tb},
		})
		require.NoError(t, err)
		ids = append(ids, m.ID)
		specs[tb] = spec
	}
	opts := jobs.DefaultOptions()
	opts.MappingIDs = ids
	optRaw, _ := json.Marshal(opts)
	job, err := st.Jobs().Create(ctx, "ws", store.Job{ProjectID: p.ID, Options: optRaw, MappingIDs: ids})
	require.NoError(t, err)

	runner := NewRunner(Deps{
		Store: st,
		OpenSource: func(ctx context.Context, _ store.WorkspaceID, _ string) (source.Session, store.Connection, error) {
			s, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
			return s, store.Connection{}, err
		},
		OpenWriter: func(ctx context.Context, _ store.WorkspaceID, _ string, o target.WriteOptions) (target.DocWriter, error) {
			return couchbase.New().OpenWriter(ctx, cbCfg, connectors.Credentials{Password: cbPass}, o)
		},
		Latest: func(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
			return store.DiscoverySnapshot{}, snap, nil
		},
	})
	start := time.Now()
	o := runner.Start(ctx, job, jobs.Control{Drain: make(chan struct{}), Abort: make(chan struct{})}).Wait()
	elapsed := time.Since(start)
	require.Nil(t, o.Fatal)
	require.True(t, o.Finished)
	require.Zero(t, o.Failed)

	for _, tb := range tables {
		n := exactCount(t, pgCfg, pgPass, tb)
		c := cl.Bucket("sandbox").Scope(scope).Collection(tb)
		// Document count: every source row became a document.
		require.Equal(t, n, countDocs(t, cl, scope, tb), "table %s", tb)

		// 20 random documents equal Program.Apply of their source row.
		table := snap.Table("shop", tb)
		spec := specs[tb]
		prog, issues := transform.Compile(&spec, table, transform.Options{})
		require.NotNil(t, prog, "%v", issues)
		sample := sampleRows(t, pgCfg, pgPass, table, 20)
		for _, row := range sample {
			key, want, _, rerr := prog.Apply(row, nil)
			require.Nil(t, rerr)
			res, err := c.Get(key, &gocb.GetOptions{Transcoder: gocb.NewRawJSONTranscoder()})
			require.NoError(t, err, key)
			var got []byte
			require.NoError(t, res.Content(&got))
			require.JSONEq(t, string(want), string(got), key)
		}
	}
	snapOut := string(o.Progress)
	t.Logf("migrated %v in %s: %s", tables, elapsed, snapOut[:min(len(snapOut), 300)])
}

func pgConn(t *testing.T, cfg json.RawMessage, pass string) source.Session {
	s, err := postgres.New().Open(context.Background(), cfg, connectors.Credentials{Password: pass})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// exactCount counts the table through a read session (count(*) in a snapshot).
func exactCount(t *testing.T, cfg json.RawMessage, pass, table string) int64 {
	rs, err := pgConn(t, cfg, pass).(source.SnapshotReader).BeginRead(context.Background(), "e2e count", false)
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()
	n, err := rs.Count(context.Background(), source.TableRead{Schema: "shop", Table: table})
	require.NoError(t, err)
	return n
}

// sampleRows reads n random rows of the table (all columns, table order).
func sampleRows(t *testing.T, cfg json.RawMessage, pass string, table *schema.Table, n int) [][]any {
	rows, err := pgConn(t, cfg, pass).Preview(context.Background(), source.PreviewRequest{
		Schema: "shop", Table: table.Name, Columns: table.Columns, Random: true, RowEstimate: func() int64 {
			if table.RowEstimate != nil {
				return *table.RowEstimate
			}
			return 0
		}(), Limit: n, TimeoutSec: 30,
	})
	require.NoError(t, err)
	rand.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	return rows
}

// countDocs counts a collection's documents with a KV range scan over all keys.
func countDocs(t *testing.T, cl *gocb.Cluster, scope, coll string) int64 {
	res, err := cl.Bucket("sandbox").Scope(scope).Collection(coll).Scan(gocb.RangeScan{}, &gocb.ScanOptions{IDsOnly: true, Timeout: 5 * time.Minute})
	require.NoError(t, err)
	var n int64
	for res.Next() != nil {
		n++
	}
	require.NoError(t, res.Err())
	return n
}
