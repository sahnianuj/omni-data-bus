//go:build integration

package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

// captureWriter keeps every written document in memory.
type captureWriter struct {
	mu   sync.Mutex
	docs map[string][]byte
}

func (c *captureWriter) WriteBatch(_ context.Context, docs []target.Doc) []target.WriteResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]target.WriteResult, len(docs))
	for i, d := range docs {
		c.docs[d.Key] = append([]byte(nil), d.Value...)
		out[i] = target.WriteResult{Key: d.Key, Outcome: target.Written, Attempts: 1}
	}
	return out
}
func (c *captureWriter) Close() error { return nil }

// digest is sha256 over the documents sorted by key: key, NUL, bytes, newline.
func (c *captureWriter) digest() string {
	keys := make([]string, 0, len(c.docs))
	for k := range c.docs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(c.docs[k])
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestPlan_AC2_V1ParityGolden runs the 1.0 golden migration (customer + address many-to-one
// + orders one-to-many) from the spec 1.0 stored, now upgraded to v2 and built through the
// execution plan: every document is byte-identical to what 1.0 wrote (testdata/plan_parity_1.0.json,
// captured with the 1.0 code before the refactor).
func TestPlan_AC2_V1ParityGolden(t *testing.T) {
	ctx := context.Background()
	pgCfg, pgPass, _, _, _ := e2eEnv(t)
	sess, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()
	snap, err := sess.Discover(ctx, source.DiscoverOptions{Schemas: []string{"shop"}})
	require.NoError(t, err)

	v1, err := os.ReadFile("../mapping/testdata/specs/v1/customer_address_orders.json")
	require.NoError(t, err)
	require.Equal(t, 1, mapping.VersionOf(v1))
	var golden struct {
		Documents int    `json:"documents"`
		SHA256    string `json:"sha256"`
	}
	raw, err := os.ReadFile("testdata/plan_parity_1.0.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &golden))

	st := memstore.New(nil)
	p, err := st.Projects().Create(ctx, "ws", store.NewProject{Name: "parity"})
	require.NoError(t, err)
	m, err := st.Mappings().Create(ctx, "ws", store.Mapping{
		ProjectID: p.ID, SourceSchema: "shop", SourceTable: "customer", Spec: v1, Status: "ready",
		Target: store.MappingTarget{Bucket: "b", Scope: "s", Collection: "c"},
	})
	require.NoError(t, err)
	opts := jobs.DefaultOptions()
	opts.MappingIDs, opts.BatchSize = []string{m.ID}, 100
	optRaw, _ := json.Marshal(opts)
	job, err := st.Jobs().Create(ctx, "ws", store.Job{ProjectID: p.ID, Options: optRaw, MappingIDs: opts.MappingIDs})
	require.NoError(t, err)
	cw := &captureWriter{docs: map[string][]byte{}}
	runner := NewRunner(Deps{
		Store: st,
		OpenSource: func(ctx context.Context, _ store.WorkspaceID, _ string) (source.Session, store.Connection, error) {
			s, err := postgres.New().Open(ctx, pgCfg, connectors.Credentials{Password: pgPass})
			return s, store.Connection{}, err
		},
		OpenWriter: func(context.Context, store.WorkspaceID, string, target.WriteOptions) (target.DocWriter, error) {
			return cw, nil
		},
		Latest: func(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
			return store.DiscoverySnapshot{}, snap, nil
		},
	})
	o := runner.Start(ctx, job, jobs.Control{Drain: make(chan struct{}), Abort: make(chan struct{})}).Wait()
	require.Nil(t, o.Fatal)
	require.Zero(t, o.Failed)
	require.Len(t, cw.docs, golden.Documents)
	require.Equal(t, golden.SHA256, cw.digest(), "documents differ from the 1.0 output")
}
