//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"omnidatabus/internal/connectors"
)

// Runs the real connector against PostgreSQL 16 with the dev seed (read-only role omni_ro that
// can't read schema billing).
func TestTester_AC4_Integration(t *testing.T) {
	ctx := context.Background()
	srv := pgServer(t)
	cfg := srv.cfg
	owner, ownerPw := srv.owner, srv.ownerPw
	c := New()

	res := c.Test(ctx, cfg("omni_ro"), connectors.Credentials{Password: "omni_ro"})
	require.True(t, res.OK, "%+v", res.Steps)
	require.Equal(t, connectors.StepWarn, res.Steps[4].Status)
	require.Contains(t, res.Steps[4].Detail, "schema 'billing'")

	res = c.Test(ctx, cfg(owner), connectors.Credentials{Password: ownerPw})
	require.True(t, res.OK)
	require.Equal(t, connectors.StepWarn, res.Steps[4].Status, "owner/superuser gets a least-privilege warning: %s", res.Steps[4].Detail)

	res = c.Test(ctx, cfg("omni_ro"), connectors.Credentials{Password: "wrong"})
	require.False(t, res.OK)
	require.Equal(t, "SOURCE_AUTH_FAILED", res.Steps[2].Code)
}

// testServer is a PostgreSQL 16 server loaded with deploy/compose/seed/source/01-shop.sql.
type testServer struct {
	host           string
	port           int
	owner, ownerPw string
}

func (s testServer) cfg(user string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"host": s.host, "port": s.port, "database": "shop", "username": user, "sslMode": "disable"})
	return b
}

// pgServer uses OC_IT_PG_HOST/PORT when set (e.g. `make dev-stack`), otherwise starts a
// seeded container for the test.
func pgServer(t *testing.T) testServer {
	t.Helper()
	ctx := context.Background()
	srv := testServer{owner: envOr("OC_IT_PG_OWNER", "shop_owner"), ownerPw: envOr("OC_IT_PG_OWNER_PASSWORD", "shop_owner")}
	if srv.host = os.Getenv("OC_IT_PG_HOST"); srv.host != "" {
		srv.port, _ = strconv.Atoi(os.Getenv("OC_IT_PG_PORT"))
		if srv.port == 0 {
			srv.port = 5432
		}
		return srv
	}
	// The seed the golden snapshot was taken from: the shop schema and the relations demo.
	seed := "../../../../../deploy/compose/seed/source/"
	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("shop"), tcpostgres.WithUsername("shop_owner"), tcpostgres.WithPassword("shop_owner"),
		tcpostgres.WithInitScripts(seed+"01-shop.sql", seed+"03-relations.sql"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(2*time.Minute)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	srv.host, err = pg.Host(ctx)
	require.NoError(t, err)
	mapped, err := pg.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)
	srv.port = int(mapped.Num())
	srv.owner, srv.ownerPw = "shop_owner", "shop_owner"
	return srv
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
