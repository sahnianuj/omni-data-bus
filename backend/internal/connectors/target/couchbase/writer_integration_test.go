//go:build integration

package couchbase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"
	tccouchbase "github.com/testcontainers/testcontainers-go/modules/couchbase"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

type itTarget struct {
	connStr, user, pass, bucket, scope string
	cluster                            *gocb.Cluster
}

// itTargetCollection prepares bucket.<fresh scope>.<coll> on the dev stack (or a container).
func itTargetCollection(t *testing.T, coll string) itTarget {
	t.Helper()
	ctx := context.Background()
	it := itTarget{connStr: os.Getenv("OC_IT_TARGET_CONNSTR"), user: os.Getenv("OC_IT_TARGET_USERNAME"), pass: os.Getenv("OC_IT_TARGET_PASSWORD"), bucket: "sandbox"}
	if strings.Contains(it.connStr, "cloud.couchbase.com") {
		t.Skip("writes aren't tested against Capella")
	}
	if it.connStr == "" {
		c, err := tccouchbase.Run(ctx, "couchbase/server:enterprise-8.0.3", tccouchbase.WithBuckets(tccouchbase.NewBucket(it.bucket).WithQuota(256)))
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Terminate(ctx) })
		it.connStr, err = c.ConnectionString(ctx)
		require.NoError(t, err)
		it.user, it.pass = c.Username(), c.Password()
	}
	cl, err := gocb.Connect(it.connStr, gocb.ClusterOptions{Authenticator: gocb.PasswordAuthenticator{Username: it.user, Password: it.pass}})
	require.NoError(t, err)
	it.cluster = cl
	it.scope = fmt.Sprintf("itw_%d", time.Now().UnixNano()%1_000_000_000)
	mgr := cl.Bucket(it.bucket).CollectionsV2()
	require.NoError(t, mgr.CreateScope(it.scope, nil))
	require.NoError(t, mgr.CreateCollection(it.scope, coll, nil, nil))
	t.Cleanup(func() { _ = mgr.DropScope(it.scope, nil); _ = cl.Close(nil) })
	require.NoError(t, cl.Bucket(it.bucket).WaitUntilReady(30*time.Second, nil))
	// Wait until the new collection accepts writes.
	c := cl.Bucket(it.bucket).Scope(it.scope).Collection(coll)
	require.Eventually(t, func() bool {
		_, err := c.Upsert("probe", map[string]int{"x": 1}, nil)
		return err == nil
	}, 60*time.Second, 200*time.Millisecond)
	_, _ = c.Remove("probe", nil)
	return it
}

func (it itTarget) cfg() json.RawMessage {
	b, _ := json.Marshal(map[string]any{"connectionString": it.connStr, "username": it.user, "deployment": SelfManaged, "connectTimeoutMs": 30000, "kvTimeoutMs": 5000})
	return b
}

func (it itTarget) open(t *testing.T, mode, coll string) target.DocWriter {
	t.Helper()
	w, err := New().OpenWriter(context.Background(), it.cfg(), connectors.Credentials{Password: it.pass},
		target.WriteOptions{Location: target.Location{Kind: target.KindCouchbase, Bucket: it.bucket, Scope: it.scope, Collection: coll}, Mode: mode, Concurrency: 32, Expiry: time.Hour})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestWriter_AC2_Integration_AllModes(t *testing.T) {
	it := itTargetCollection(t, "docs")
	ctx := context.Background()
	up := it.open(t, "upsert", "docs")
	rs := up.WriteBatch(ctx, []target.Doc{{Key: "a", Value: []byte(`{"v":1}`)}, {Key: "b", Value: []byte(`{"v":1}`)}})
	require.Equal(t, []target.Outcome{target.Written, target.Written}, []target.Outcome{rs[0].Outcome, rs[1].Outcome})

	ins := it.open(t, "insert", "docs")
	rs = ins.WriteBatch(ctx, []target.Doc{{Key: "a", Value: []byte(`{"v":2}`)}, {Key: "c", Value: []byte(`{"v":2}`)}})
	require.Equal(t, target.Skipped, rs[0].Outcome, "insert skips an existing key")
	require.Equal(t, target.Written, rs[1].Outcome)

	rep := it.open(t, "replace", "docs")
	rs = rep.WriteBatch(ctx, []target.Doc{{Key: "b", Value: []byte(`{"v":3}`)}, {Key: "zzz", Value: []byte(`{"v":3}`)}})
	require.Equal(t, target.Written, rs[0].Outcome)
	require.Equal(t, target.Skipped, rs[1].Outcome, "replace skips a missing key")

	c := it.cluster.Bucket(it.bucket).Scope(it.scope).Collection("docs")
	var v map[string]int
	res, err := c.Get("a", nil)
	require.NoError(t, err)
	require.NoError(t, res.Content(&v))
	require.Equal(t, 1, v["v"], "the insert didn't overwrite")
	_, err = c.Get("zzz", nil)
	require.ErrorIs(t, err, gocb.ErrDocumentNotFound)

	// A missing collection is reported when the writer opens, not after a timeout per document.
	start := time.Now()
	_, err = New().OpenWriter(ctx, it.cfg(), connectors.Credentials{Password: it.pass},
		target.WriteOptions{Location: target.Location{Kind: target.KindCouchbase, Bucket: it.bucket, Scope: it.scope, Collection: "no_such_collection"}, Mode: "upsert"})
	require.True(t, problem.HasCode(err, target.CodeKeyspaceMissing), "%v", err)
	require.Less(t, time.Since(start), 10*time.Second)
}

func TestWriter_AC3_Integration_BytesIdentical(t *testing.T) {
	it := itTargetCollection(t, "raw")
	w := it.open(t, "upsert", "raw")
	value := []byte(`{"z":1.10,"a":"é","n":null,"nested":{"x":[1,2]}}`) // key order, 1.10 and é must survive
	rs := w.WriteBatch(context.Background(), []target.Doc{{Key: "k", Value: value}})
	require.Equal(t, target.Written, rs[0].Outcome)
	res, err := it.cluster.Bucket(it.bucket).Scope(it.scope).Collection("raw").Get("k", &gocb.GetOptions{Transcoder: gocb.NewRawJSONTranscoder(), WithExpiry: true})
	require.NoError(t, err)
	var got []byte
	require.NoError(t, res.Content(&got))
	require.Equal(t, string(value), string(got))
	require.NotNil(t, res.ExpiryTime(), "expiry from the options")
}
