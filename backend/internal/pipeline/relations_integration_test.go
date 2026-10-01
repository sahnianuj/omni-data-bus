//go:build integration

package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/couchbase"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/pathcodec"
	"omnidatabus/internal/related"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

// relSchema creates a throwaway schema: 300 orders × 3 items, 20 products, 4 brands.
func relSchema(t *testing.T, pgCfg json.RawMessage, pgPass string) (string, *schema.Snapshot) {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("it_rel_%d", time.Now().UnixNano()%1_000_000_000)
	pg := pgxConn(t, pgCfg, pgPass)
	_, err := pg.Exec(ctx, fmt.Sprintf(`
		CREATE SCHEMA %[1]s;
		CREATE TABLE %[1]s.brands (brand_id int PRIMARY KEY, brand text NOT NULL);
		CREATE TABLE %[1]s.products (product_id int PRIMARY KEY, name text NOT NULL, brand_id int REFERENCES %[1]s.brands);
		CREATE TABLE %[1]s.orders (order_id bigint PRIMARY KEY, note text);
		CREATE TABLE %[1]s.order_items (order_item_id bigint PRIMARY KEY, order_id bigint NOT NULL REFERENCES %[1]s.orders,
			line_no int NOT NULL, product_id int NOT NULL REFERENCES %[1]s.products, qty int NOT NULL);
		CREATE INDEX ON %[1]s.order_items (order_id, line_no);
		INSERT INTO %[1]s.brands SELECT i, 'Brand ' || i FROM generate_series(1, 4) i;
		INSERT INTO %[1]s.products SELECT i, 'Product ' || i, 1 + i %% 4 FROM generate_series(1, 20) i;
		INSERT INTO %[1]s.orders SELECT i, 'note ' || i FROM generate_series(1, 300) i;
		INSERT INTO %[1]s.order_items SELECT o * 10 + l, o, l, 1 + (o * 3 + l) %% 20, l FROM generate_series(1, 300) o, generate_series(1, 3) l;
		ANALYZE %[1]s.orders, %[1]s.order_items, %[1]s.products, %[1]s.brands;`, name))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pgxConn(t, pgCfg, pgPass).Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE") })
	sess, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()
	snap, err := sess.Discover(ctx, source.DiscoverOptions{Schemas: []string{name}})
	require.NoError(t, err)
	return name, snap
}

// orderSpec is orders with items[] → product{} → brand{} (a 3-level path added in one step,
// OC-043 AC7) and products[] through order_items (many-to-many) with each product's brand.
func orderSpec(t *testing.T, sch string, snap *schema.Snapshot) mapping.Spec {
	t.Helper()
	rel := func(s, n string) *schema.Table { return snap.Table(s, n) }
	spec := specFor(snap.Table(sch, "orders"))
	spec, _, _, err := automap.AddRelation(spec, rel, automap.EmbedInput{
		Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: sch, Name: "order_items"}, Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}},
		TargetName: "items", OrderBy: []mapping.OrderColumn{{Column: "line_no"}}, Generate: true,
		Then: []automap.EmbedInput{
			{Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: sch, Name: "products"}, Join: []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, TargetName: "product", Generate: true},
			{Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: sch, Name: "brands"}, Join: []mapping.JoinPair{{Parent: "brand_id", Child: "brand_id"}}, TargetName: "brand", Generate: true},
		},
	}, docschema.GenerateOptions{})
	require.NoError(t, err)
	var m2m []mapping.Relation
	spec, m2m, _, err = automap.AddRelation(spec, rel, automap.EmbedInput{
		Kind: mapping.RelationManyToMany, Table: mapping.TableRef{Schema: sch, Name: "products"}, TargetName: "products", Generate: true,
		Via: &mapping.Via{
			Table:      mapping.TableRef{Schema: sch, Name: "order_items"},
			ParentJoin: []mapping.ViaJoin{{Parent: "order_id", Via: "order_id"}}, ChildJoin: []mapping.ViaJoin{{Via: "product_id", Child: "product_id"}},
			Columns: []string{"qty"},
		},
		OrderBy: []mapping.OrderColumn{{Column: "line_no", From: mapping.FromVia}},
		Then:    []automap.EmbedInput{{Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: sch, Name: "brands"}, Join: []mapping.JoinPair{{Parent: "brand_id", Child: "brand_id"}}, TargetName: "brand", Generate: true}},
	}, docschema.GenerateOptions{})
	require.NoError(t, err)
	require.Len(t, m2m, 2)
	for i := range spec.FieldMappings {
		spec.FieldMappings[i].State = mapping.StateActive
	}
	for _, is := range mapping.Validate(&spec, mapping.Context{Table: snap.Table(sch, "orders"), Related: rel}) {
		require.NotEqual(t, mapping.SeverityError, is.Severity, "%s: %s", is.Path, is.Message)
	}
	return spec
}

// countingRelated counts related lookups.
type countingRelated struct {
	source.RelatedReader
	n atomic.Int32
}

func (c *countingRelated) ReadRelated(ctx context.Context, q source.RelatedQuery, keys [][]any) ([][]any, error) {
	c.n.Add(1)
	return c.RelatedReader.ReadRelated(ctx, q, keys)
}

func TestResolveRelations_AC6_NestedUnderM2M(t *testing.T) {
	ctx := context.Background()
	pgCfg, pgPass := pgOnlyEnv(t)
	sch, snap := relSchema(t, pgCfg, pgPass)
	spec := orderSpec(t, sch, snap)
	// Keep only products[] (many-to-many) and its brand.
	var keep []mapping.Relation
	for _, r := range spec.Relations {
		if r.Kind == mapping.RelationManyToMany || (r.Parent() != "" && spec.RelationByID(r.Parent()).Kind == mapping.RelationManyToMany) {
			keep = append(keep, r)
		}
	}
	spec.Relations = keep
	orders := snap.Table(sch, "orders")
	plans, err := related.Plans(&spec, orders, func(s, n string) *schema.Table { return snap.Table(s, n) })
	require.NoError(t, err)

	sess, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()
	rs, err := sess.(source.SnapshotReader).BeginRead(ctx, "odb it", false)
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()
	cr := &countingRelated{RelatedReader: rs.(source.RelatedReader)}
	rows := make([][]any, 100)
	for i := range rows {
		rows[i] = []any{int64(i + 1), "n"}
	}
	got, fails, err := (&related.Resolver{Plans: plans, Reader: cr}).Resolve(ctx, rows)
	require.NoError(t, err)
	require.Empty(t, fails)
	require.EqualValues(t, 2, cr.n.Load(), "one query for products via order_items, one for brands")
	m2m := keep[0].ID
	for i := range rows {
		set := got[i][m2m]
		require.Equal(t, 3, set.Len(), "3 lines per order")
		for _, p := range set.Rows {
			require.Equal(t, 1, p.Sub[keep[1].ID].Len(), "each product holds its brand")
		}
	}
}

// runRelJob runs one mapping of orders to a target and returns it when done.
func runRelJob(t *testing.T, sch string, snap *schema.Snapshot, spec mapping.Spec, loc store.Location,
	pgCfg json.RawMessage, pgPass string, open func(ctx context.Context, o target.WriteOptions) (target.DocWriter, error),
) {
	t.Helper()
	runTableJob(t, sch, "orders", 50, snap, spec, loc, pgCfg, pgPass, open)
}

// runTableJob migrates one mapping of sch.table with a batch size.
func runTableJob(t *testing.T, sch, table string, batch int, snap *schema.Snapshot, spec mapping.Spec, loc store.Location,
	pgCfg json.RawMessage, pgPass string, open func(ctx context.Context, o target.WriteOptions) (target.DocWriter, error),
) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New(nil)
	p, err := st.Projects().Create(ctx, "ws", store.NewProject{Name: "relations"})
	require.NoError(t, err)
	raw, _ := json.Marshal(spec)
	nm := store.Mapping{ProjectID: p.ID, SourceSchema: sch, SourceTable: table, Spec: raw, Status: "ready"}
	nm.SetLocation(loc)
	m, err := st.Mappings().Create(ctx, "ws", nm)
	require.NoError(t, err)
	opts := jobs.DefaultOptions()
	opts.MappingIDs, opts.BatchSize = []string{m.ID}, batch
	optRaw, _ := json.Marshal(opts)
	job, err := st.Jobs().Create(ctx, "ws", store.Job{ProjectID: p.ID, Options: optRaw, MappingIDs: opts.MappingIDs})
	require.NoError(t, err)
	runner := NewRunner(Deps{
		Store: st,
		OpenSource: func(ctx context.Context, _ store.WorkspaceID, _ string) (source.Session, store.Connection, error) {
			s, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
			return s, store.Connection{}, err
		},
		OpenWriter: func(ctx context.Context, _ store.WorkspaceID, _ string, o target.WriteOptions) (target.DocWriter, error) {
			return open(ctx, o)
		},
		Latest: func(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
			return store.DiscoverySnapshot{}, snap, nil
		},
	})
	o := runner.Start(ctx, job, jobs.Control{Drain: make(chan struct{}), Abort: make(chan struct{})}).Wait()
	require.Nil(t, o.Fatal)
	require.True(t, o.Finished)
	require.Zero(t, o.Failed)
}

// readFolder reads a directory target folder's documents by key.
func readFolder(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		key, ok := pathcodec.Decode(strings.TrimSuffix(d.Name(), ".json"))
		require.True(t, ok, p)
		b, err := os.ReadFile(p) //nolint:gosec // test
		require.NoError(t, err)
		out[key] = b
		return nil
	}))
	return out
}

func TestRelations_AC8_SnapshotAllDepths(t *testing.T) {
	pgCfg, pgPass := pgOnlyEnv(t)
	sch, snap := relSchema(t, pgCfg, pgPass)
	spec := orderSpec(t, sch, snap)
	root := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "orders"), 0o750))
	conn := directory.New([]string{root})
	cfg, _ := json.Marshal(map[string]any{"path": root, "durability": "none"})

	// Product 1 and brand 1 are renamed once the run is under way.
	pg := pgxConn(t, pgCfg, pgPass)
	var once sync.Once
	rename := func() {
		_, err := pg.Exec(context.Background(), "UPDATE "+sch+".products SET name = 'Renamed' WHERE product_id = 1; UPDATE "+sch+".brands SET brand = 'Renamed' WHERE brand_id = 1")
		require.NoError(t, err)
	}
	runRelJob(t, sch, snap, spec, store.Location{Kind: store.KindDirectory, Folder: "orders"}, pgCfg, pgPass, func(ctx context.Context, o target.WriteOptions) (target.DocWriter, error) {
		w, err := conn.OpenWriter(ctx, cfg, connectors.Credentials{}, o)
		return hookWriter{w, &once, rename}, err
	})
	docs := readFolder(t, filepath.Join(root, "orders"))
	require.Len(t, docs, 300)
	all := bytes.Join(func() [][]byte {
		var out [][]byte
		for _, d := range docs {
			out = append(out, d)
		}
		return out
	}(), nil)
	require.NotContains(t, string(all), "Renamed", "documents show names as of the snapshot, at depth 2 and 3")
	require.Contains(t, string(all), `"name":"Product 1"`)
	require.Contains(t, string(all), `"brand":"Brand 1"`)

	var doc struct {
		Items []struct {
			LineNo  int `json:"lineNo"`
			Product struct {
				Name  string `json:"name"`
				Brand struct {
					Brand string `json:"brand"`
				} `json:"brand"`
			} `json:"product"`
		} `json:"items"`
		Products []struct {
			Qty   int `json:"qty"`
			Brand struct {
				Brand string `json:"brand"`
			} `json:"brand"`
		} `json:"products"`
	}
	require.NoError(t, json.Unmarshal(docs["orders::7"], &doc), string(docs["orders::7"]))
	require.Len(t, doc.Items, 3)
	require.Equal(t, 1, doc.Items[0].LineNo)
	require.NotEmpty(t, doc.Items[0].Product.Brand.Brand, "items[].product.brand, three levels deep")
	require.Len(t, doc.Products, 3)
	require.Equal(t, []int{1, 2, 3}, []int{doc.Products[0].Qty, doc.Products[1].Qty, doc.Products[2].Qty}, "junction payload in line_no order")
}

// TestRelations_TargetParity runs the same relationship mapping to Couchbase and to a directory:
// the keys and documents are identical whichever target is used.
func TestRelations_TargetParity(t *testing.T) {
	pgCfg, pgPass, cbCfg, cbPass, cl := e2eEnv(t)
	sch, snap := relSchema(t, pgCfg, pgPass)
	spec := orderSpec(t, sch, snap)
	require.Equal(t, "orders::{order_id}", spec.Key.Template, "the same default key for every target")

	root := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "orders"), 0o750))
	conn := directory.New([]string{root})
	cfg, _ := json.Marshal(map[string]any{"path": root, "durability": "none"})
	runRelJob(t, sch, snap, spec, store.Location{Kind: store.KindDirectory, Folder: "orders"}, pgCfg, pgPass, func(ctx context.Context, o target.WriteOptions) (target.DocWriter, error) {
		return conn.OpenWriter(ctx, cfg, connectors.Credentials{}, o)
	})

	scope := fmt.Sprintf("it_rel_%d", time.Now().UnixNano()%1_000_000_000)
	mgr := cl.Bucket("sandbox").CollectionsV2()
	require.NoError(t, mgr.CreateScope(scope, nil))
	t.Cleanup(func() { _ = mgr.DropScope(scope, nil) })
	require.NoError(t, mgr.CreateCollection(scope, "orders", nil, nil))
	coll := cl.Bucket("sandbox").Scope(scope).Collection("orders")
	require.Eventually(t, func() bool {
		_, err := coll.Upsert("probe", map[string]int{}, nil)
		return err == nil
	}, 60*time.Second, 200*time.Millisecond)
	_, _ = coll.Remove("probe", nil)
	runRelJob(t, sch, snap, spec, store.Location{Kind: store.KindCouchbase, Bucket: "sandbox", Scope: scope, Collection: "orders"}, pgCfg, pgPass,
		func(ctx context.Context, o target.WriteOptions) (target.DocWriter, error) {
			return couchbase.New().OpenWriter(ctx, cbCfg, connectors.Credentials{Password: cbPass}, o)
		})

	files := readFolder(t, filepath.Join(root, "orders"))
	require.Len(t, files, 300)
	for key, want := range files {
		res, err := coll.Get(key, nil)
		require.NoError(t, err, "Couchbase has %s too", key)
		var got json.RawMessage
		require.NoError(t, res.Content(&got))
		require.JSONEq(t, string(want), string(got), key)
	}
}
