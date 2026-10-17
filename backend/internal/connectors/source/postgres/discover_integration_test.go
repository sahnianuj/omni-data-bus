//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/encode"
	"omnidatabus/internal/schema"
)

var update = flag.Bool("update", false, "rewrite golden files")

// normalize drops what varies between servers and runs (statistics, sizes, patch version).
func normalize(s *schema.Snapshot) {
	s.ServerVersion = ""
	for i := range s.Schemas {
		for j := range s.Schemas[i].Tables {
			t := &s.Schemas[i].Tables[j]
			t.RowEstimate, t.RowCountExact, t.StatsStale, t.SizeBytes = nil, false, false, 0
		}
	}
}

// AC1: the seed schema, discovered as the read-only role, matches the golden snapshot.
func TestDiscover_AC1_SeedSchema(t *testing.T) {
	ctx := context.Background()
	srv := pgServer(t)
	sess, err := New().Open(ctx, srv.cfg("omni_ro"), connectors.Credentials{Password: "omni_ro"})
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()

	// big_events only exists on the dev stack (02-big-events.sql); exclude it so both match.
	opts := source.DiscoverOptions{ExcludePatterns: []string{"big_events"}}
	n, err := sess.EstimateRelations(ctx, opts)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 10)

	snap, err := sess.Discover(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, "shop", snap.Database)
	require.Regexp(t, `^16\.`, snap.ServerVersion)

	cust := snap.Table("shop", "customer")
	require.NotNil(t, cust)
	require.Equal(t, schema.TypeString, cust.Column("email_address").JSONType, "citext")
	require.True(t, cust.Column("email_address").IsUnique)
	require.Equal(t, schema.TypeArray, cust.Column("tags").JSONType)
	require.Equal(t, []string{"pending", "paid", "shipped", "cancelled"}, snap.Table("shop", "orders").Column("status").EnumValues)
	require.Equal(t, schema.WarnUnsupportedType, snap.Table("shop", "product").Column("search_vector").Warning)
	require.Equal(t, []string{"order_id", "line_no"}, snap.Table("shop", "Order Details").PrimaryKey)
	require.Empty(t, snap.Table("shop", "page_view").PrimaryKey)
	require.False(t, snap.Table("billing", "invoice").Readable, "omni_ro can't SELECT billing")
	require.True(t, cust.Readable)

	normalize(snap)
	got, err := json.MarshalIndent(snap, "", "  ")
	require.NoError(t, err)
	golden := filepath.Join("testdata", "shop-snapshot.golden.json")
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(golden, append(got, '\n'), 0o644)) //nolint:gosec // a checked-in test fixture
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "run with -update to create the golden file")
	require.JSONEq(t, string(want), string(got))
}

// AC8: a 1,000-table × 20-column catalog is discovered end to end in < 10 s.
func TestDiscover_AC8_LargeCatalog(t *testing.T) {
	ctx := context.Background()
	srv := pgServer(t)
	owner, err := New().Open(ctx, srv.cfg(srv.owner), connectors.Credentials{Password: srv.ownerPw})
	require.NoError(t, err)
	t.Cleanup(func() { _ = owner.Close() }) // cleanups run last-in first-out: after the DROP below
	conn := owner.(*pgSession).conn
	// Connector sessions are read-only by design; this setup session needs to write.
	_, err = conn.Exec(ctx, "SET default_transaction_read_only = off")
	require.NoError(t, err)

	schemaName := fmt.Sprintf("it_big_%d", time.Now().UnixNano())
	_, err = conn.Exec(ctx, `
DO $$
DECLARE i int; cols text;
BEGIN
  EXECUTE format('CREATE SCHEMA %I', '`+schemaName+`');
  SELECT string_agg(format('c%s int', g), ', ') INTO cols FROM generate_series(2, 20) g;
  FOR i IN 1..1000 LOOP
    EXECUTE format('CREATE TABLE %I.%I (id int PRIMARY KEY, %s)', '`+schemaName+`', 't' || i, cols);
  END LOOP;
END $$`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := conn.Exec(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
		require.NoError(t, err)
	})

	start := time.Now()
	snap, err := owner.Discover(ctx, source.DiscoverOptions{Schemas: []string{schemaName}})
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.Equal(t, 1000, snap.TableCount())
	require.Len(t, snap.Table(schemaName, "t1").Columns, 20)
	require.Less(t, elapsed, 10*time.Second)
	t.Logf("discovered 1,000 tables in %s", elapsed)
}

// OC-011 AC5: previews read real rows with quoted identifiers and every §6.4 type.
func TestPreview_AC5_SeedTables(t *testing.T) {
	ctx := context.Background()
	srv := pgServer(t)
	sess, err := New().Open(ctx, srv.cfg("omni_ro"), connectors.Credentials{Password: "omni_ro"})
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()
	snap, err := sess.Discover(ctx, source.DiscoverOptions{Schemas: []string{"shop"}})
	require.NoError(t, err)

	od := snap.Table("shop", "Order Details")
	rows, err := sess.Preview(ctx, source.PreviewRequest{Schema: "shop", Table: "Order Details", Columns: od.Columns, OrderBy: od.PrimaryKey, Limit: 5})
	require.NoError(t, err)
	require.Len(t, rows, 5)
	require.Equal(t, int64(1), rows[0][0])

	for _, name := range []string{"product", "customer", "address", "orders"} {
		tb := snap.Table("shop", name)
		rows, err := sess.Preview(ctx, source.PreviewRequest{Schema: "shop", Table: name, Columns: tb.Columns, OrderBy: tb.PrimaryKey, Limit: 3})
		require.NoError(t, err, name)
		require.Len(t, rows, 3, name)
		require.Len(t, rows[0], len(tb.Columns), name)
	}

	// Real driver values through the pipeline encoders (§6.4).
	encoded := func(table string, id int) map[string]any {
		tb := snap.Table("shop", table)
		rows, err := sess.Preview(ctx, source.PreviewRequest{Schema: "shop", Table: table, Columns: tb.Columns, OrderBy: tb.PrimaryKey, Limit: id})
		require.NoError(t, err)
		out := map[string]any{}
		for i, c := range tb.Columns {
			out[c.Name] = encode.Value(rows[id-1][i], c)
		}
		return out
	}
	p := encoded("product", 1)
	require.Equal(t, 6.01, p["price"], "numeric(12,2) → number")
	require.Equal(t, 6.0, p["list_price"], "money → locale-free number")
	require.Equal(t, "1.0000000001", p["precise_cost"], "numeric(38,10) keeps every digit as a string")
	require.Equal(t, map[string]any{"color": "blue"}, p["attributes"])
	require.Equal(t, "<spec><id>1</id></spec>", p["spec_sheet"], "xml as text")
	require.Equal(t, "09:00:00", p["open_from"])
	require.Equal(t, "17:30:00+05:30", p["open_until"])
	require.Equal(t, "P1Y6M", p["warranty"])
	require.Equal(t, []any{int64(1), int64(1), int64(1)}, p["size_matrix"])
	require.Equal(t, "Product 1", p["title"])

	c := encoded("customer", 10)
	require.Equal(t, "customer10@example.com", c["email_address"], "citext")
	require.Equal(t, "iVBORw0KGgo=", c["avatar"], "bytea → base64")
	require.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, c["external_ref"])
	require.Equal(t, "2020-01-01T10:00:00Z", c["created_on"], "timestamptz → UTC Z")
	require.Equal(t, "2024-06-01T12:10:00", c["updated_at"], "timestamp without zone")
	require.Equal(t, []any{"tier-1", "region-2"}, c["tags"])
	require.Equal(t, map[string]any{"language": "es", "newsletter": true}, c["preferences"])
	a := encoded("address", 1)
	require.Equal(t, "US", a["country_code"], "char(2)")
	require.Equal(t, "10.0.1.1", a["location_ip"], "inet host without /32")
	require.Equal(t, "10.0.0.0/16", a["network"])
	require.Equal(t, "08:00:2b:01:02:03", a["device_mac"])
	require.Equal(t, "paid", encoded("orders", 1)["status"], "enum label")

	rows, err = sess.Preview(ctx, source.PreviewRequest{Schema: "shop", Table: "orders", Columns: snap.Table("shop", "orders").Columns, Limit: 1000})
	require.NoError(t, err)
	require.Len(t, rows, source.MaxPreviewLimit, "capped at 200")
}

// OC-157: a partial unique index allows duplicates outside its WHERE, so it isn't reported as
// unique; a full unique index on the same column is, and a foreign key on it reads as one-to-one.
func TestDiscover_PartialUniqueIndexNotUnique(t *testing.T) {
	ctx := context.Background()
	srv := pgServer(t)
	owner, err := New().Open(ctx, srv.cfg(srv.owner), connectors.Credentials{Password: srv.ownerPw})
	require.NoError(t, err)
	t.Cleanup(func() { _ = owner.Close() })
	conn := owner.(*pgSession).conn
	_, err = conn.Exec(ctx, "SET default_transaction_read_only = off")
	require.NoError(t, err)

	schemaName := fmt.Sprintf("it_uniq_%d", time.Now().UnixNano())
	_, err = conn.Exec(ctx, fmt.Sprintf(`
CREATE SCHEMA %[1]s;
CREATE TABLE %[1]s.cart (id int PRIMARY KEY);
CREATE TABLE %[1]s.cart_note (id int PRIMARY KEY, cart_id int REFERENCES %[1]s.cart(id), active bool);
CREATE UNIQUE INDEX cart_note_active ON %[1]s.cart_note (cart_id) WHERE active;
CREATE TABLE %[1]s.cart_total (id int PRIMARY KEY, cart_id int REFERENCES %[1]s.cart(id));
CREATE UNIQUE INDEX cart_total_cart ON %[1]s.cart_total (cart_id);`, schemaName))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := conn.Exec(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
		require.NoError(t, err)
	})

	snap, err := owner.Discover(ctx, source.DiscoverOptions{Schemas: []string{schemaName}})
	require.NoError(t, err)
	note := snap.Table(schemaName, "cart_note")
	require.False(t, note.Column("cart_id").IsUnique)
	require.False(t, note.UniqueKey([]string{"cart_id"}))
	total := snap.Table(schemaName, "cart_total")
	require.True(t, total.Column("cart_id").IsUnique)
	require.True(t, total.UniqueKey([]string{"cart_id"}))
}
