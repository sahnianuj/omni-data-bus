//go:build integration

package couchbase

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	tccouchbase "github.com/testcontainers/testcontainers-go/modules/couchbase"

	"omnidatabus/internal/connectors"
)

// TestTester_AC3_Integration runs the real connection test against Couchbase: a Couchbase
// Server 8.0.3 container by default, or the cluster in OC_IT_TARGET_CONNSTR/_USERNAME/_PASSWORD
// (e.g. Capella). The test only reads (ping, version, roles); it writes nothing.
func TestTester_AC3_Integration(t *testing.T) {
	ctx := context.Background()
	connStr, user, pass := os.Getenv("OC_IT_TARGET_CONNSTR"), os.Getenv("OC_IT_TARGET_USERNAME"), os.Getenv("OC_IT_TARGET_PASSWORD")
	// The auth probe asks the Query service on its standard port (8093); a container with
	// remapped ports can't answer it, so a wrong password reads as unreachable there.
	queryOnStdPort := true
	if connStr == "" {
		c, err := tccouchbase.Run(ctx, "couchbase/server:enterprise-8.0.3", tccouchbase.WithBuckets(tccouchbase.NewBucket("sandbox").WithQuota(256)))
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Terminate(ctx) })
		connStr, err = c.ConnectionString(ctx)
		require.NoError(t, err)
		user, pass = c.Username(), c.Password()
		qp, err := c.MappedPort(ctx, "8093/tcp")
		require.NoError(t, err)
		queryOnStdPort = qp.Port() == "8093"
	}
	deployment := SelfManaged
	if strings.Contains(connStr, "cloud.couchbase.com") {
		deployment = Capella
	}
	cfg, _ := json.Marshal(map[string]any{"connectionString": connStr, "username": user, "deployment": deployment, "connectTimeoutMs": 30000})
	conn := New()

	res := conn.Test(ctx, cfg, connectors.Credentials{Password: pass})
	require.True(t, res.OK, "%+v", res.Steps)
	var caps Capabilities
	require.NoError(t, json.Unmarshal(res.Capabilities, &caps))
	require.True(t, caps.CanWriteDocs, "%+v", caps)
	if deployment == Capella {
		require.False(t, caps.CanCreateBucket)
	}

	bad := conn.Test(ctx, cfg, connectors.Credentials{Password: "definitely-wrong"})
	require.False(t, bad.OK)
	if !queryOnStdPort {
		t.Log("Query service isn't on 8093 (remapped container port): not checking TARGET_AUTH_FAILED")
		return
	}
	require.Equal(t, "TARGET_AUTH_FAILED", bad.Steps[1].Code, "%+v", bad.Steps)
}
