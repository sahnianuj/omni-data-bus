//go:build integration

package relations

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
	"omnidatabus/internal/store"
)

func TestDetectCardinality_AC4_Suggests(t *testing.T) {
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
	sch := fmt.Sprintf("it_detect_%d", time.Now().UnixNano()%1_000_000_000)
	_, err = conn.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %[1]s;
		CREATE TABLE %[1]s.customers (code text, name text);
		CREATE TABLE %[1]s.orders (order_id int, cust_code text);
		INSERT INTO %[1]s.customers SELECT 'C' || i, 'n' FROM generate_series(1, 50) i;
		INSERT INTO %[1]s.orders SELECT i, 'C' || (1 + i %% 50) FROM generate_series(1, 400) i;`, sch))
	require.NoError(t, err)
	defer func() { _, _ = conn.Exec(context.Background(), "DROP SCHEMA "+sch+" CASCADE") }()

	cfg, _ := json.Marshal(map[string]any{"host": host, "port": port, "database": "shop", "username": "shop_owner", "sslMode": "disable"})
	sess, err := postgres.New().Open(ctx, cfg, connectors.Credentials{Password: "shop_owner"})
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()
	kc := sess.(source.KeyCounter)
	orders := store.RelationshipEnd{Schema: sch, Table: "orders", Columns: []string{"cust_code"}}
	customers := store.RelationshipEnd{Schema: sch, Table: "customers", Columns: []string{"code"}}

	d, err := DetectWith(ctx, kc, orders, customers, "", time.Now())
	require.NoError(t, err)
	require.Equal(t, CardinalityManyToOne, d.Suggested, "customers.code is unique in the data")
	require.EqualValues(t, 1, d.MaxPerKey)
	require.EqualValues(t, 8, d.FromMaxPerKey)

	_, err = conn.Exec(ctx, "INSERT INTO "+sch+".customers VALUES ('C7', 'dup'), ('C7', 'dup2')")
	require.NoError(t, err)
	d, err = DetectWith(ctx, kc, orders, customers, CardinalityManyToOne, time.Now())
	require.NoError(t, err)
	require.Equal(t, CardinalityOneToMany, d.Suggested, "duplicates")
	require.EqualValues(t, 3, d.MaxPerKey)
	require.Contains(t, d.Warning, "contradicts")
}
