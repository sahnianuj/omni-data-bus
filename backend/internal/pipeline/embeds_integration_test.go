//go:build integration

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/jackc/pgx/v5"
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
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

// hookWriter runs a function once, before the first batch is written.
type hookWriter struct {
	target.DocWriter
	once *sync.Once
	fn   func()
}

func (w hookWriter) WriteBatch(ctx context.Context, docs []target.Doc) []target.WriteResult {
	w.once.Do(w.fn)
	return w.DocWriter.WriteBatch(ctx, docs)
}

// TestEmbeds_AC5_Integration embeds shop.orders (one-to-many) and shop.address (many-to-one)
// into shop.customer. An order inserted while the job runs isn't visible: the lookups share the
// parents' snapshot. Every order ends up in exactly one customer's array.
func TestEmbeds_AC5_Integration(t *testing.T) {
	ctx := context.Background()
	pgCfg, pgPass, cbCfg, cbPass, cl := e2eEnv(t)
	pgSess, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
	require.NoError(t, err)
	defer func() { _ = pgSess.Close() }()
	snap, err := pgSess.Discover(ctx, source.DiscoverOptions{Schemas: []string{"shop"}})
	require.NoError(t, err)
	customer := snap.Table("shop", "customer")
	rel := func(s, n string) *schema.Table { return snap.Table(s, n) }

	// customer + address{…} + orders[…] (newest first), generated and auto-mapped like the dialog.
	spec := specFor(customer)
	var cands []mapping.EmbedCandidate
	for _, c := range mapping.Candidates(snap, customer, &spec) {
		if c.Table.Name == "orders" || c.Table.Name == "address" {
			cands = append(cands, c)
		}
	}
	require.Len(t, cands, 2)
	for _, c := range cands {
		in := automap.EmbedInput{Kind: c.Kind, Table: c.Table, Join: c.Join, Generate: true}
		if c.Kind == mapping.EmbedOneToMany {
			in.OrderBy = []mapping.OrderColumn{{Column: "order_id", Desc: true}}
		}
		spec, _, _, err = automap.AddEmbed(spec, snap.Table(c.Table.Schema, c.Table.Name), in, docschema.GenerateOptions{})
		require.NoError(t, err)
	}
	for i := range spec.FieldMappings {
		spec.FieldMappings[i].State = mapping.StateActive
	}
	for _, i := range mapping.Validate(&spec, mapping.Context{Table: customer, Related: rel}) {
		require.NotEqual(t, mapping.SeverityError, i.Severity, "%s: %s", i.Path, i.Message)
	}

	scope := fmt.Sprintf("it_embed_%d", time.Now().UnixNano()%1_000_000_000)
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
	p, err := st.Projects().Create(ctx, "ws", store.NewProject{Name: "embeds"})
	require.NoError(t, err)
	raw, _ := json.Marshal(spec)
	m, err := st.Mappings().Create(ctx, "ws", store.Mapping{
		ProjectID: p.ID, SourceSchema: "shop", SourceTable: "customer", Spec: raw, Status: "ready",
		Target: store.MappingTarget{Bucket: "sandbox", Scope: scope, Collection: "customer"},
	})
	require.NoError(t, err)
	opts := jobs.DefaultOptions()
	opts.MappingIDs, opts.BatchSize = []string{m.ID}, 100
	optRaw, _ := json.Marshal(opts)
	job, err := st.Jobs().Create(ctx, "ws", store.Job{ProjectID: p.ID, Options: optRaw, MappingIDs: opts.MappingIDs})
	require.NoError(t, err)

	// The last customer gets a new order once the run is under way.
	var lastCustomer, ordersBefore int64
	pg := pgxConn(t, pgCfg, pgPass)
	require.NoError(t, pg.QueryRow(ctx, "SELECT max(customer_id) FROM shop.customer").Scan(&lastCustomer))
	require.NoError(t, pg.QueryRow(ctx, "SELECT count(*) FROM shop.orders").Scan(&ordersBefore))
	var inserted int64
	insert := func() {
		require.NoError(t, pg.QueryRow(ctx, `INSERT INTO shop.orders (order_id, customer_id, order_date, total)
			SELECT max(order_id) + 1, $1, now(), 1 FROM shop.orders RETURNING order_id`, lastCustomer).Scan(&inserted))
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), "DELETE FROM shop.orders WHERE order_id = $1", inserted) })

	var once sync.Once
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
			return hookWriter{w, &once, insert}, nil
		},
		Latest: func(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
			return store.DiscoverySnapshot{}, snap, nil
		},
	})
	o := runner.Start(ctx, job, jobs.Control{Drain: make(chan struct{}), Abort: make(chan struct{})}).Wait()
	require.Nil(t, o.Fatal)
	require.True(t, o.Finished)
	require.Zero(t, o.Failed)
	require.NotZero(t, inserted, "the order was inserted during the run")

	// The last customer's array doesn't have the new order; all orders are embedded once.
	var last struct {
		Orders  []map[string]any `json:"orders"`
		Address map[string]any   `json:"address"`
	}
	res, err := coll.Get(fmt.Sprintf("customer::%d", lastCustomer), nil)
	require.NoError(t, err)
	require.NoError(t, res.Content(&last))
	for _, o := range last.Orders {
		require.NotEqualValues(t, inserted, o["orderId"], "an order inserted after the snapshot isn't embedded")
	}
	q, err := cl.Query(fmt.Sprintf("SELECT RAW SUM(ARRAY_LENGTH(c.orders)) FROM sandbox.%s.customer AS c", scope),
		&gocb.QueryOptions{ScanConsistency: gocb.QueryScanConsistencyRequestPlus, Timeout: time.Minute})
	if err == nil {
		var total int64
		require.True(t, q.Next())
		require.NoError(t, q.Row(&total))
		require.Equal(t, ordersBefore, total, "every order at the snapshot is in exactly one array")
	} else if os.Getenv("OC_IT_REQUIRE_QUERY") != "" {
		require.NoError(t, err)
	}
}

func pgxConn(t *testing.T, cfg json.RawMessage, pass string) *pgx.Conn {
	var c struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Database string `json:"database"`
		Username string `json:"username"`
	}
	require.NoError(t, json.Unmarshal(cfg, &c))
	conn, err := pgx.Connect(context.Background(), fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", c.Username, pass, c.Host, c.Port, c.Database))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}
