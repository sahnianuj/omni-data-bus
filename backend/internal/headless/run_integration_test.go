//go:build integration

package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connections"
	"omnidatabus/internal/export"
	"omnidatabus/internal/store"
)

// TestRun_AC1_DirectoryEndToEnd: a sealed bundle of orders (with their customer and items) runs
// with no metadata store: one file per order, and report.json's counts match.
func TestRun_AC1_DirectoryEndToEnd(t *testing.T) {
	tl := newTool(t, t.TempDir())
	out := t.TempDir()
	b := tl.export(t, out, export.Request{})
	var stdout, stderr bytes.Buffer
	cfg := runConfig(b, t.TempDir(), &stdout, &stderr)
	rep, code, err := Run(context.Background(), cfg)
	require.NoError(t, err, stderr.String())
	require.Equal(t, ExitOK, code, stderr.String())

	n := countRows(t, "shop.orders")
	docs := documents(t, out)
	require.Len(t, docs, n, "one file per order")
	require.Equal(t, StatusCompleted, rep.Status)
	require.EqualValues(t, n, rep.Totals.Written)
	require.Len(t, rep.Mappings, 1)
	require.EqualValues(t, n, rep.Mappings[0].Written)
	require.Equal(t, b.SHA256(), rep.BundleSHA256)

	saved, err := cfg.State.ReadReport()
	require.NoError(t, err)
	require.Equal(t, rep.RunID, saved.RunID)

	// A document has the embedded customer and items.
	for _, raw := range docs {
		var doc map[string]any
		require.NoError(t, json.Unmarshal(raw, &doc))
		require.Contains(t, doc, "customer")
		require.Contains(t, doc, "items")
		break
	}
}

// TestRun_AC2_ParityWithServer: the same mappings through the server pipeline and odb run give
// byte-identical documents.
func TestRun_AC2_ParityWithServer(t *testing.T) {
	serverDir := t.TempDir()
	tl := newTool(t, serverDir)
	o := tl.runServer(t)
	require.Nil(t, o.Fatal)
	require.True(t, o.Finished)

	runnerDir := t.TempDir()
	b := tl.export(t, runnerDir, export.Request{CredentialMode: export.ModePlaintext})
	var stdout, stderr bytes.Buffer
	_, code, err := Run(context.Background(), runConfig(b, t.TempDir(), &stdout, &stderr))
	require.NoError(t, err)
	require.Equal(t, ExitOK, code, stderr.String())

	server, runner := documents(t, serverDir), documents(t, runnerDir)
	require.Equal(t, countRows(t, "shop.orders"), len(server))
	require.Equal(t, len(server), len(runner))
	for path, doc := range server {
		require.Equal(t, string(doc), string(runner[path]), path)
	}
	require.Contains(t, stderr.String(), "plaintext source credentials", "the runner warns about plaintext credentials")
}

// TestRun_AC1_CouchbaseEndToEnd: the same bundle with a Couchbase target override writes one
// document per order to the collection (OC_IT_TARGET_CONNSTR, the dev stack's "sandbox" bucket).
func TestRun_AC1_CouchbaseEndToEnd(t *testing.T) {
	conn, user, pass := os.Getenv("OC_IT_TARGET_CONNSTR"), os.Getenv("OC_IT_TARGET_USERNAME"), os.Getenv("OC_IT_TARGET_PASSWORD")
	if conn == "" {
		t.Skip("set OC_IT_TARGET_CONNSTR/_USERNAME/_PASSWORD (the dev stack)")
	}
	tl := newTool(t, t.TempDir())
	scope := "odb_run_" + strconv.FormatInt(time.Now().UnixNano()%1e9, 36)
	cfg, _ := json.Marshal(map[string]any{"connectionString": conn, "username": user, "deployment": "self-managed"})
	// The project's target becomes the cluster (Target step, mappings moved there, OC-068).
	_, _, err := tl.conns.Save(context.Background(), itWS, "u1", tl.project.ID, store.RoleTarget, connections.SaveInput{
		Type: "couchbase", Config: cfg, Secrets: connections.SecretsPatch{Password: &pass},
		Relocate: &connections.Relocate{Bucket: "sandbox", Scope: scope},
	})
	require.NoError(t, err)
	b := tl.export(t, "", export.Request{})
	require.Equal(t, "sandbox", b.Mappings[0].Location.Bucket)
	require.True(t, b.Mappings[0].Location.CreateIfMissing)

	cl, err := gocb.Connect(conn, gocb.ClusterOptions{Authenticator: gocb.PasswordAuthenticator{Username: user, Password: pass}})
	require.NoError(t, err)
	defer func() { _ = cl.Close(nil) }()
	mgr := cl.Bucket("sandbox").CollectionsV2()
	require.NoError(t, mgr.CreateScope(scope, nil))
	t.Cleanup(func() { _ = mgr.DropScope(scope, nil) })

	var stdout, stderr bytes.Buffer
	rep, code, err := Run(context.Background(), runConfig(b, t.TempDir(), &stdout, &stderr))
	require.NoError(t, err, stderr.String())
	require.Equal(t, ExitOK, code, stderr.String())
	n := countRows(t, "shop.orders")
	require.EqualValues(t, n, rep.Totals.Written)

	coll := cl.Bucket("sandbox").Scope(scope).Collection("orders")
	got, err := coll.Get("orders::1", nil)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, got.Content(&doc))
	require.Contains(t, doc, "items")
	res, err := cl.Query("SELECT RAW COUNT(*) FROM `sandbox`.`"+scope+"`.`orders`", &gocb.QueryOptions{ScanConsistency: gocb.QueryScanConsistencyRequestPlus})
	require.NoError(t, err)
	var count int
	require.True(t, res.Next())
	require.NoError(t, res.Row(&count))
	require.Equal(t, n, count)
}
