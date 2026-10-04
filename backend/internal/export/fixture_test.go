package export

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/mappings"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

var ctx = context.Background()

const ws store.WorkspaceID = "ws-1"

// The fixture's credentials: the scanner must never find them outside sealed blocks.
const (
	pgPassword = "pg-Secr3t-migrator-pw"
	cbPassword = "cb-Target-Passw0rd-x"
	passphrase = "correct-horse-battery-staple-orbit"
)

func est(n int64) *int64 { return &n }

func col(name, udt, js string, ord int, pk bool) schema.Column {
	return schema.Column{Name: name, Ordinal: ord, UDTName: udt, DataType: udt, JSONType: js, IsPrimaryKey: pk}
}

// shopSnapshot is orders → order_items → products, with no database FK from order_items to
// products (a virtual relationship).
func shopSnapshot() *schema.Snapshot {
	return &schema.Snapshot{Database: "shop", Schemas: []schema.Schema{{Name: "public", Tables: []schema.Table{
		{Name: "orders", PrimaryKey: []string{"id"}, RowEstimate: est(1000), Readable: true, Columns: []schema.Column{
			col("id", "int4", schema.TypeInteger, 1, true), col("customer_id", "int4", schema.TypeInteger, 2, false),
			col("status", "text", schema.TypeString, 3, false),
		}, ReferencedBy: []schema.Reference{{Schema: "public", Table: "order_items", Columns: []string{"order_id"}, RefColumns: []string{"id"}}}},
		{Name: "order_items", PrimaryKey: []string{"id"}, RowEstimate: est(5000), Readable: true, Columns: []schema.Column{
			col("id", "int4", schema.TypeInteger, 1, true), col("order_id", "int4", schema.TypeInteger, 2, false),
			col("product_id", "int4", schema.TypeInteger, 3, false), col("quantity", "int4", schema.TypeInteger, 4, false),
		}, ForeignKeys: []schema.ForeignKey{{Name: "order_items_order_fk", Columns: []string{"order_id"}, RefSchema: "public", RefTable: "orders", RefColumns: []string{"id"}}}},
		{Name: "products", PrimaryKey: []string{"id"}, RowEstimate: est(50), Readable: true, Columns: []schema.Column{
			col("id", "int4", schema.TypeInteger, 1, true), col("name", "text", schema.TypeString, 2, false),
			{Name: "unused", Ordinal: 3, UDTName: "text", DataType: "text", JSONType: schema.TypeString, Nullable: true},
		}},
	}}}}
}

type fakeSchemas struct{ snap *schema.Snapshot }

func (f *fakeSchemas) Latest(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
	if f.snap == nil {
		return store.DiscoverySnapshot{}, nil, problem.New(http.StatusNotFound, problem.CodeSchemaNotDiscovered, "Not discovered", "")
	}
	return store.DiscoverySnapshot{ID: "snap"}, f.snap, nil
}

type fixture struct {
	st      *memstore.Store
	sealer  crypto.Sealer
	maps    *mappings.Service
	schemas *fakeSchemas
	b       *Builder
	project store.Project
	orders  store.Mapping
	items   store.Mapping
	usedRel string
}

func newSealer(t *testing.T) crypto.Sealer {
	t.Helper()
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	kr, err := crypto.ParseKeyring(base64.StdEncoding.EncodeToString(key), "", "")
	require.NoError(t, err)
	return crypto.NewLocalSealer(kr)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	clock := storetest.NewClock()
	st := memstore.New(clock)
	f := &fixture{st: st, sealer: newSealer(t), schemas: &fakeSchemas{snap: shopSnapshot()}}
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "Shop → docs", CreatedBy: "u1"})
	require.NoError(t, err)
	f.project = p
	f.connection(t, store.RoleSource, "postgres", `{"host":"db.internal","port":5432,"database":"shop","username":"migrator","sslMode":"disable"}`, pgPassword)
	f.connection(t, store.RoleTarget, store.KindCouchbase, `{"connectionString":"couchbases://cb.example.com","username":"cbuser","deployment":"self-managed"}`, cbPassword)
	f.maps = mappings.NewService(st, f.schemas, nil, nil)

	loc := func(c string) *mapping.Location {
		return &mapping.Location{Kind: store.KindCouchbase, Bucket: "shop", Scope: "sales", Collection: c, CreateIfMissing: true}
	}
	ov, err := f.maps.Create(ctx, ws, "u1", p.ID, mappings.CreateInput{SourceSchema: "public", SourceTable: "orders", Location: loc("orders")})
	require.NoError(t, err)
	iv, err := f.maps.Create(ctx, ws, "u1", p.ID, mappings.CreateInput{SourceSchema: "public", SourceTable: "order_items", Location: loc("order_items")})
	require.NoError(t, err)
	// order_items embeds its product through a virtual relationship; another one is unused.
	used, err := st.Relationships().Create(ctx, ws, store.Relationship{
		ProjectID: p.ID, Name: "item product",
		From: store.RelationshipEnd{Schema: "public", Table: "order_items", Columns: []string{"product_id"}},
		To:   store.RelationshipEnd{Schema: "public", Table: "products", Columns: []string{"id"}}, Cardinality: "many-to-one",
	})
	require.NoError(t, err)
	f.usedRel = used.ID
	_, err = st.Relationships().Create(ctx, ws, store.Relationship{
		ProjectID: p.ID, Name: "unused",
		From: store.RelationshipEnd{Schema: "public", Table: "orders", Columns: []string{"customer_id"}},
		To:   store.RelationshipEnd{Schema: "public", Table: "products", Columns: []string{"id"}}, Cardinality: "many-to-one",
	})
	require.NoError(t, err)
	_, err = f.maps.AddEmbed(ctx, ws, "u1", p.ID, iv.ID, 0, automap.EmbedInput{
		Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "public", Name: "products"},
		Join: []mapping.JoinPair{{Parent: "product_id", Child: "id"}}, TargetName: "product", Generate: true,
	})
	require.NoError(t, err)
	f.orders, err = st.Mappings().Get(ctx, ws, ov.ID)
	require.NoError(t, err)
	f.items, err = st.Mappings().Get(ctx, ws, iv.ID)
	require.NoError(t, err)

	f.b = &Builder{
		Store: st, Sealer: f.sealer, Validate: f.maps.Validate, Latest: f.schemas.Latest,
		IsBundled: func(c store.Connection) bool { return c.Name == connections.BundledName },
		Version:   "2.0.0", Now: func() time.Time { return time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC) },
	}
	return f
}

func (f *fixture) connection(t *testing.T, role store.Role, typ, cfg, password string) {
	t.Helper()
	id := store.ConnectionID(f.project.ID, role)
	sec := connections.Secrets{Password: password}
	sealed, err := connections.Seal(f.sealer, ws, f.project.ID, id, sec)
	require.NoError(t, err)
	_, err = f.st.Connections().Upsert(ctx, ws, f.project.ID, role, store.ConnectionInput{
		Type: typ, Name: typ + " conn", Config: json.RawMessage(cfg), Secret: sealed, SecretMeta: connections.Meta(sec), LastTestStatus: "ok",
	})
	require.NoError(t, err)
}

func (f *fixture) build(t *testing.T, req Request) Built {
	t.Helper()
	out, err := f.b.Build(ctx, ws, "user@example.com", f.project.ID, req)
	require.NoError(t, err)
	return out
}

// toDirectory switches the project's target to a folder and moves the mappings there, as the
// Target step does (OC-068).
func (f *fixture) toDirectory(t *testing.T, path string) {
	t.Helper()
	_, err := f.st.Connections().Upsert(ctx, ws, f.project.ID, store.RoleTarget, store.ConnectionInput{
		Type: store.KindDirectory, Name: path, Config: json.RawMessage(`{"path":"` + path + `","layout":"flat","compression":"none","durability":"none","fileMode":"0640","manifest":true,"maxDocBytes":20971520}`),
		LastTestStatus: "ok",
	})
	require.NoError(t, err)
	for _, m := range []*store.Mapping{&f.orders, &f.items} {
		cur, err := f.st.Mappings().Get(ctx, ws, m.ID)
		require.NoError(t, err)
		cur.SetLocation(store.Location{Kind: store.KindDirectory, Folder: cur.SourceTable, CreateIfMissing: true})
		*m, err = f.st.Mappings().Update(ctx, ws, cur)
		require.NoError(t, err)
	}
}

// draft takes a mapping out of the export (only ready mappings are exported).
func (f *fixture) draft(t *testing.T, m store.Mapping) {
	t.Helper()
	cur, err := f.st.Mappings().Get(ctx, ws, m.ID)
	require.NoError(t, err)
	cur.Status = "draft"
	_, err = f.st.Mappings().Update(ctx, ws, cur)
	require.NoError(t, err)
}

func externalReq() Request {
	return Request{CredentialMode: ModeExternal, Runtime: "bundle"}
}
