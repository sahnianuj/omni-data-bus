//go:build integration

package headless

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/couchbase"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/discovery"
	"omnidatabus/internal/export"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/mappings"
	"omnidatabus/internal/pipeline"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

const itWS store.WorkspaceID = "ws-it"

// pgEnv is the dev stack's PostgreSQL (OC_IT_PG_HOST/PORT), as the shop_owner role.
func pgEnv(t *testing.T) (json.RawMessage, string) {
	t.Helper()
	host := os.Getenv("OC_IT_PG_HOST")
	if host == "" {
		t.Skip("set OC_IT_PG_HOST/PORT (the dev stack)")
	}
	port := 5432
	if v := os.Getenv("OC_IT_PG_PORT"); v != "" {
		port, _ = strconv.Atoi(v)
	}
	cfg, _ := json.Marshal(map[string]any{"host": host, "port": port, "database": "shop", "username": "shop_owner", "sslMode": "disable"})
	return cfg, "shop_owner"
}

// tool is the tool's side: a project in an in-memory metadata store, with the real services.
type tool struct {
	st      *memstore.Store
	sealer  crypto.Sealer
	conns   *connections.Service
	disc    *discovery.Service
	maps    *mappings.Service
	project store.Project
	orders  store.Mapping
	targets *target.Registry
}

// newTool sets up "orders with their customer and items" (one document per order) writing to
// a directory under serverDir, as a user would in the tool.
func newTool(t *testing.T, serverDir string) *tool {
	t.Helper()
	ctx := context.Background()
	tl := newBareTool(t, serverDir, "shop")
	var err error
	v, err := tl.maps.Create(ctx, itWS, "u1", tl.project.ID, mappings.CreateInput{
		SourceSchema: "shop", SourceTable: "orders", Location: &mapping.Location{Kind: store.KindDirectory, Folder: "orders", CreateIfMissing: true},
	})
	require.NoError(t, err)
	_, err = tl.maps.AddEmbed(ctx, itWS, "u1", tl.project.ID, v.ID, 0, automap.EmbedInput{
		Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "customer"},
		Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, TargetName: "customer", Generate: true,
	})
	require.NoError(t, err)
	_, err = tl.maps.AddEmbed(ctx, itWS, "u1", tl.project.ID, v.ID, 0, automap.EmbedInput{
		Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "shop", Name: "Order Details"},
		Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetName: "items", Generate: true,
		OrderBy: []mapping.OrderColumn{{Column: "line_no"}},
	})
	require.NoError(t, err)
	res, err := tl.maps.Validate(ctx, itWS, tl.project.ID, v.ID)
	require.NoError(t, err)
	require.True(t, res.Ready, "%+v", res.Issues)
	tl.orders, err = tl.st.Mappings().Get(ctx, itWS, v.ID)
	require.NoError(t, err)
	tl.orders.Status = mapping.StatusReady
	tl.orders, err = tl.st.Mappings().Update(ctx, itWS, tl.orders)
	require.NoError(t, err)
	return tl
}

// export builds a bundle of the project as configured. A runnerDir first points the project's
// directory target there, as a user would on the Target step (exports carry no overrides).
func (tl *tool) export(t *testing.T, runnerDir string, req export.Request) export.Bundle {
	t.Helper()
	b := &export.Builder{
		Store: tl.st, Sealer: tl.sealer, Validate: tl.maps.Validate, Latest: tl.disc.Latest,
		Version: buildinfo.Get().Version,
	}
	if runnerDir != "" {
		cfg, _ := json.Marshal(map[string]any{"path": runnerDir, "createIfMissing": true})
		_, err := tl.st.Connections().Upsert(context.Background(), itWS, tl.project.ID, store.RoleTarget, store.ConnectionInput{
			Type: store.KindDirectory, Name: runnerDir, Config: cfg, LastTestStatus: "ok",
		})
		require.NoError(t, err)
	}
	if req.CredentialMode == "" {
		req.CredentialMode, req.Passphrase = export.ModeSealed, passphrase
	}
	out, err := b.Build(context.Background(), itWS, "user@example.com", tl.project.ID, req)
	require.NoError(t, err)
	return out.Bundle
}

// newBareTool is a project with the dev PostgreSQL as source (the given schemas discovered) and a
// directory target at serverDir, but no mapping yet.
func newBareTool(t *testing.T, serverDir string, schemas ...string) *tool {
	t.Helper()
	ctx := context.Background()
	pgCfg, pgPass := pgEnv(t)
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	kr, err := crypto.ParseKeyring(base64.StdEncoding.EncodeToString(key), "", "")
	require.NoError(t, err)
	tl := &tool{st: memstore.New(store.SystemClock{}), sealer: crypto.NewLocalSealer(kr)}
	tl.project, err = tl.st.Projects().Create(ctx, itWS, store.NewProject{Name: "Shop → docs", CreatedBy: "u1"})
	require.NoError(t, err)
	sources := source.NewRegistry()
	sources.Register(postgres.New())
	tl.targets = target.NewRegistry()
	tl.targets.Register(couchbase.New())
	tl.targets.Register(directory.New([]string{serverDir}))
	tl.conns = connections.NewService(tl.st, tl.sealer, sources, tl.targets, nil, nil)
	_, _, err = tl.conns.Save(ctx, itWS, "u1", tl.project.ID, store.RoleSource, connections.SaveInput{
		Type: "postgres", Config: pgCfg, Secrets: connections.SecretsPatch{Password: &pgPass},
	})
	require.NoError(t, err)
	dirCfg, _ := json.Marshal(map[string]any{"path": serverDir, "createIfMissing": true})
	_, _, err = tl.conns.Save(ctx, itWS, "u1", tl.project.ID, store.RoleTarget, connections.SaveInput{Type: store.KindDirectory, Config: dirCfg})
	require.NoError(t, err)
	tl.disc = discovery.NewService(tl.st, tl.conns.OpenSource, nil)
	_, err = tl.disc.Discover(ctx, itWS, "u1", store.ConnectionID(tl.project.ID, store.RoleSource), source.DiscoverOptions{Schemas: schemas})
	require.NoError(t, err)
	tl.disc.Wait()
	tl.maps = mappings.NewService(tl.st, tl.disc, tl.conns.OpenSource, tl.conns.OpenKeyspaces)
	tl.maps.StatLocation = tl.conns.StatLocation
	return tl
}

// ready validates a mapping and marks it ready, as saving it in the tool does.
func (tl *tool) ready(t *testing.T, id string) store.Mapping {
	t.Helper()
	ctx := context.Background()
	res, err := tl.maps.Validate(ctx, itWS, tl.project.ID, id)
	require.NoError(t, err)
	require.True(t, res.Ready, "%+v", res.Issues)
	m, err := tl.st.Mappings().Get(ctx, itWS, id)
	require.NoError(t, err)
	m.Status = mapping.StatusReady
	m, err = tl.st.Mappings().Update(ctx, itWS, m)
	require.NoError(t, err)
	return m
}

// runServer migrates the project's mappings the way the server's worker does.
func (tl *tool) runServer(t *testing.T) jobs.Outcome {
	t.Helper()
	ctx := context.Background()
	pre := &jobs.Preflighter{
		Store: tl.st, Validate: tl.maps.Validate, TestConn: tl.conns.TestStored, OpenSource: tl.conns.OpenSource,
		Target: tl.conns.PreflightTarget, Latest: tl.disc.Latest,
	}
	svc := jobs.NewService(tl.st, pre, nil)
	svc.PlanHash = tl.maps.PlanHash
	v, _, err := svc.Create(ctx, itWS, "u1", tl.project.ID, jobs.DefaultOptions())
	require.NoError(t, err)
	j, err := tl.st.Jobs().GetAny(ctx, v.ID)
	require.NoError(t, err)
	r := pipeline.NewRunner(pipeline.Deps{Store: tl.st, OpenSource: tl.conns.OpenSource, OpenWriter: tl.conns.OpenWriter, Latest: tl.disc.Latest, CountTarget: tl.conns.CountDocuments})
	return r.Start(ctx, j, jobs.Control{Drain: make(chan struct{}), Abort: make(chan struct{})}).Wait()
}

// runConfig is a Config for b with the state in stateDir; stdout and stderr are captured.
func runConfig(b export.Bundle, stateDir string, stdout, stderr io.Writer) *Config {
	bb := b
	return &Config{
		Bundle: &bb, State: StateDir{Path: stateDir}, ProgressInterval: 100 * time.Millisecond, Stdout: stdout,
		Log: slog.New(slog.NewJSONHandler(stderr, nil)), Build: buildinfo.Get(),
		Unlock: Unlock{Getenv: func(k string) string {
			if k == PassphraseEnv {
				return passphrase
			}
			return ""
		}},
	}
}

// documents reads every JSON document under dir (the _odb bookkeeping folder excluded), by
// path relative to dir.
func documents(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "_odb" {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Ext(p) != ".json" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = raw
		return nil
	}))
	return out
}

// countRows counts a table's rows in the dev PostgreSQL.
func countRows(t *testing.T, table string) int {
	t.Helper()
	host, port := os.Getenv("OC_IT_PG_HOST"), os.Getenv("OC_IT_PG_PORT")
	if port == "" {
		port = "5432"
	}
	conn, err := pgx.Connect(context.Background(), "postgres://shop_owner:shop_owner@"+host+":"+port+"/shop?sslmode=disable")
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	var n int
	require.NoError(t, conn.QueryRow(context.Background(), "select count(*) from "+table).Scan(&n))
	return n
}

func lines(b *bytes.Buffer) [][]byte { return bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n")) }
