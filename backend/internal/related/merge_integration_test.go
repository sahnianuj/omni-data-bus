//go:build integration

package related

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// TestMerge_AC2_TextKeysCollation merges text keys that sort differently under the database's
// default collation (en_US: a < B < c) and byte-wise (B < a < c): sorted-merge reads the child
// with COLLATE "C" and compares in Go byte-wise, so it equals the batched lookups.
func TestMerge_AC2_TextKeysCollation(t *testing.T) {
	host := os.Getenv("OC_IT_PG_HOST")
	if host == "" {
		t.Skip("set OC_IT_PG_HOST/PORT (the dev stack)")
	}
	port, _ := strconv.Atoi(os.Getenv("OC_IT_PG_PORT"))
	if port == 0 {
		port = 5432
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://shop_owner:shop_owner@%s:%d/shop?sslmode=disable", host, port))
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	sch := fmt.Sprintf("it_merge_%d", time.Now().UnixNano()%1_000_000_000)
	codes := []string{"a", "B", "c", "D", "aa", "Ab", "é", "Z", "_x", "b"}
	_, err = conn.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %[1]s;
		CREATE TABLE %[1]s.parent (code text PRIMARY KEY);
		CREATE TABLE %[1]s.child (id serial PRIMARY KEY, code text NOT NULL, n int NOT NULL);
		CREATE INDEX ON %[1]s.child (code);`, sch))
	require.NoError(t, err)
	defer func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+sch+" CASCADE") }()
	for i, c := range codes {
		_, err = conn.Exec(ctx, "INSERT INTO "+sch+".parent VALUES ($1)", c)
		require.NoError(t, err)
		for n := range i % 4 { // 0 to 3 children, some parents have none
			_, err = conn.Exec(ctx, "INSERT INTO "+sch+".child (code, n) VALUES ($1, $2)", c, n)
			require.NoError(t, err)
		}
	}

	cfg, _ := json.Marshal(map[string]any{"host": host, "port": port, "database": "shop", "username": "shop_owner", "sslMode": "disable"})
	sess, err := postgres.New().Open(ctx, cfg, connectors.Credentials{Password: "shop_owner"})
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()
	snap, err := sess.Discover(ctx, source.DiscoverOptions{Schemas: []string{sch}})
	require.NoError(t, err)
	spec := &mapping.Spec{DocSchema: docschema.Schema{Root: docschema.NewRoot()}, Relations: []mapping.Relation{{
		ID: "c", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: sch, Name: "child"},
		Join: []mapping.JoinPair{{Parent: "code", Child: "code"}}, OrderBy: []mapping.OrderColumn{{Column: "n"}},
	}}}
	plans, err := Plans(spec, snap.Table(sch, "parent"), func(s, n string) *schema.Table { return snap.Table(s, n) })
	require.NoError(t, err)
	rs, err := sess.(source.SnapshotReader).BeginRead(ctx, "odb it", false)
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()
	rr := rs.(source.RelatedReader)

	rows := make([][]any, len(codes))
	for i, c := range codes {
		rows[i] = []any{c}
	}
	batched, _, err := (&Resolver{Plans: plans, Reader: rr}).Resolve(ctx, rows)
	require.NoError(t, err)
	plans[0].Strategy = StrategySortedMerge
	merged, _, err := (&Resolver{Plans: plans, Reader: rr}).Resolve(ctx, rows)
	require.NoError(t, err)
	require.Equal(t, batched, merged)
	for i := range codes {
		require.Equal(t, i%4, merged[i]["c"].Len(), codes[i])
	}
}
