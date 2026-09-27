package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
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

func est(n int64) *int64 { return &n }

func shopSnapshot() *schema.Snapshot {
	return &schema.Snapshot{Database: "shop", Schemas: []schema.Schema{{Name: "shop", Tables: []schema.Table{
		{Name: "customer", PrimaryKey: []string{"customer_id"}, RowEstimate: est(2000), Readable: true, Columns: []schema.Column{
			{Name: "customer_id", Ordinal: 1, UDTName: "int4", JSONType: schema.TypeInteger, IsPrimaryKey: true},
			{Name: "email", Ordinal: 2, UDTName: "text", JSONType: schema.TypeString},
		}},
		{Name: "page_view", RowEstimate: est(5000), Readable: true, Columns: []schema.Column{
			{Name: "url", Ordinal: 1, UDTName: "text", JSONType: schema.TypeString, Nullable: true},
		}},
		{Name: "secret", Readable: false, Columns: []schema.Column{{Name: "x", Ordinal: 1, UDTName: "int4", JSONType: schema.TypeInteger}}},
	}}}}
}

// fakeKS is a target keyspace browser; missing collections are created on request.
type fakeKS struct {
	mu        sync.Mutex
	existing  map[string]int64 // bucket.scope.collection → doc count
	scopes    map[string]bool  // bucket.scope
	created   []string
	createErr error
}

func (f *fakeKS) ListBuckets(context.Context) (target.BucketList, error) {
	return target.BucketList{}, nil
}

func (f *fakeKS) ListScopes(_ context.Context, bucket string) ([]target.ScopeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []target.ScopeInfo
	for k := range f.scopes {
		if len(k) > len(bucket) && k[:len(bucket)+1] == bucket+"." {
			out = append(out, target.ScopeInfo{Name: k[len(bucket)+1:]})
		}
	}
	return out, nil
}

func (f *fakeKS) Collection(_ context.Context, b, s, c string) (target.CollectionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.existing[b+"."+s+"."+c]
	if !ok {
		return target.CollectionInfo{}, problem.New(http.StatusNotFound, problem.CodeNotFound, "Collection not found", "")
	}
	return target.CollectionInfo{Name: c, DocCountEstimate: &n}, nil
}
func (f *fakeKS) CreateBucket(context.Context, target.BucketSpec) error { return nil }
func (f *fakeKS) CreateScope(_ context.Context, b, s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopes[b+"."+s] = true
	f.created = append(f.created, "scope "+b+"."+s)
	return nil
}

func (f *fakeKS) CreateCollection(_ context.Context, b, s string, spec target.CollectionSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.existing[b+"."+s+"."+spec.Name] = 0
	f.created = append(f.created, "collection "+b+"."+s+"."+spec.Name)
	return nil
}

func (f *fakeKS) SampleDocuments(context.Context, string, string, string, int) ([]json.RawMessage, string, error) {
	return nil, "", nil
}

func (f *fakeKS) ExistingKeys(context.Context, string, string, string, []string) (map[string]bool, error) {
	return nil, nil
}

// fakeSession is a source session that can (or can't) export snapshots.
type fakeSession struct{ exportErr error }

func (f *fakeSession) Ping(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{}, nil
}

func (f *fakeSession) CheckPrivileges(context.Context) (source.PrivilegeReport, error) {
	return source.PrivilegeReport{}, nil
}

func (f *fakeSession) EstimateRelations(context.Context, source.DiscoverOptions) (int, error) {
	return 0, nil
}

func (f *fakeSession) Discover(context.Context, source.DiscoverOptions) (*schema.Snapshot, error) {
	return nil, nil
}

func (f *fakeSession) Preview(context.Context, source.PreviewRequest) ([][]any, error) {
	return nil, nil
}
func (f *fakeSession) Close() error                              { return nil }
func (f *fakeSession) ProbeSnapshotExport(context.Context) error { return f.exportErr }

type fixture struct {
	st        *memstore.Store
	clock     *storetest.Clock
	project   store.Project
	maps      map[string]store.Mapping // by table
	ks        *fakeKS
	sess      *fakeSession
	issues    map[string][]mapping.Issue // mapping ID → validation issues
	connCalls []string                   // connection IDs looked up (tests + opens)
	testFail  map[string]bool            // connection ID → test fails
	mu        sync.Mutex
	pre       *Preflighter
	svc       *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	clock := storetest.NewClock()
	st := memstore.New(clock)
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "Shop", CreatedBy: "u1"})
	require.NoError(t, err)
	f := &fixture{
		st: st, clock: clock, project: p, maps: map[string]store.Mapping{}, issues: map[string][]mapping.Issue{},
		testFail: map[string]bool{}, sess: &fakeSession{},
		ks: &fakeKS{existing: map[string]int64{"shop.sales.customer": 0}, scopes: map[string]bool{"shop.sales": true}},
	}
	for _, role := range []store.Role{store.RoleSource, store.RoleTarget} {
		_, err := st.Connections().Upsert(ctx, ws, p.ID, role, store.ConnectionInput{Type: "fake", Config: json.RawMessage(`{}`), LastTestStatus: "ok"})
		require.NoError(t, err)
	}
	f.addMapping(t, "customer", "ready", false)
	track := func(id string) {
		f.mu.Lock()
		f.connCalls = append(f.connCalls, id)
		f.mu.Unlock()
	}
	f.pre = &Preflighter{
		Store: st,
		Validate: func(_ context.Context, w store.WorkspaceID, projectID, id string) (mappings.ValidationResult, error) {
			if w != ws || projectID != p.ID {
				return mappings.ValidationResult{}, store.ErrNotFound
			}
			found := f.issues[id]
			return mappings.ValidationResult{Issues: found, Ready: mapping.Ready(found)}, nil
		},
		TestConn: func(_ context.Context, _ store.WorkspaceID, _, id string) (connectors.TestResult, error) {
			track(id)
			if f.testFail[id] {
				return connectors.TestResult{OK: false, Steps: []connectors.TestStep{{ID: "connect", Status: connectors.StepFail, Detail: "refused", Code: "SOURCE_UNREACHABLE"}}}, nil
			}
			return connectors.TestResult{OK: true, Capabilities: json.RawMessage(`{"canWriteDocs":true}`)}, nil
		},
		OpenSource: func(_ context.Context, _ store.WorkspaceID, id string) (source.Session, store.Connection, error) {
			track(id)
			return f.sess, store.Connection{ID: id}, nil
		},
		OpenKeyspaces: func(_ context.Context, _ store.WorkspaceID, id string) (target.Keyspaces, store.Connection, error) {
			track(id)
			return f.ks, store.Connection{ID: id}, nil
		},
		Latest: func(_ context.Context, _ store.WorkspaceID, id string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
			track(id)
			return store.DiscoverySnapshot{ID: "snap"}, shopSnapshot(), nil
		},
	}
	f.svc = NewService(st, f.pre, clock.Now)
	return f
}

func (f *fixture) addMapping(t *testing.T, table, status string, createIfMissing bool) store.Mapping {
	t.Helper()
	spec := mapping.Spec{Key: mapping.KeySpec{Template: "customer::{customer_id}"}}
	if table != "customer" {
		spec.Key.Template = "{$uuid}"
	}
	raw, _ := json.Marshal(spec)
	m, err := f.st.Mappings().Create(ctx, ws, store.Mapping{
		ProjectID: f.project.ID, SourceSchema: "shop", SourceTable: table, Spec: raw, Status: status, CreatedBy: "u1",
		Target: store.MappingTarget{Bucket: "shop", Scope: "sales", Collection: table, CreateIfMissing: createIfMissing},
	})
	require.NoError(t, err)
	f.maps[table] = m
	return m
}

// fakeRunner starts controllable executions.
type fakeRunner struct {
	mu      sync.Mutex
	started []string
	execs   map[string]*fakeExec
	// behave decides how each execution ends; nil = wait for Drain/Abort or finish().
	onStart func(e *fakeExec)
}

type fakeExec struct {
	job        store.Job
	ctl        Control
	finish     chan Outcome
	checkpoint json.RawMessage
	mu         sync.Mutex
	stoppedBy  string
}

func newFakeRunner() *fakeRunner { return &fakeRunner{execs: map[string]*fakeExec{}} }

func (r *fakeRunner) Start(_ context.Context, j store.Job, ctl Control) Execution {
	e := &fakeExec{job: j, ctl: ctl, finish: make(chan Outcome, 1), checkpoint: json.RawMessage(`{"pos":1}`)}
	r.mu.Lock()
	r.started = append(r.started, j.ID)
	r.execs[j.ID] = e
	cb := r.onStart
	r.mu.Unlock()
	if cb != nil {
		cb(e)
	}
	return e
}

// exec waits until the job's execution has started and returns it.
func (r *fakeRunner) exec(id string) *fakeExec {
	deadline := time.Now().Add(3 * time.Second)
	for {
		r.mu.Lock()
		e := r.execs[id]
		r.mu.Unlock()
		if e != nil || time.Now().After(deadline) {
			return e
		}
		time.Sleep(time.Millisecond)
	}
}

// startedIDs waits for n starts and returns the started job IDs in order.
func (r *fakeRunner) startedIDs(n int) []string {
	deadline := time.Now().Add(3 * time.Second)
	for {
		r.mu.Lock()
		out := append([]string(nil), r.started...)
		r.mu.Unlock()
		if len(out) >= n || time.Now().After(deadline) {
			return out
		}
		time.Sleep(time.Millisecond)
	}
}

func (e *fakeExec) Checkpoint() json.RawMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.checkpoint
}

func (e *fakeExec) Wait() Outcome {
	select {
	case o := <-e.finish:
		return o
	case <-e.ctl.Drain:
		e.mu.Lock()
		e.stoppedBy = "drain"
		e.checkpoint = json.RawMessage(`{"pos":"drained"}`)
		e.mu.Unlock()
		return Outcome{Checkpoint: json.RawMessage(`{"pos":"drained"}`)}
	case <-e.ctl.Abort:
		e.mu.Lock()
		e.stoppedBy = "abort"
		e.mu.Unlock()
		return Outcome{}
	}
}

func (e *fakeExec) stopped() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stoppedBy
}

func fastConfig(id string) WorkerConfig {
	return WorkerConfig{
		ID: id, MaxJobs: 2, PollInterval: 5 * time.Millisecond, StatusPoll: 5 * time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond, StaleAfter: 30 * time.Second, ReaperInterval: time.Hour, ShutdownBudget: 2 * time.Second,
	}
}

func waitStatus(t *testing.T, st store.Store, id, status string) store.Job {
	t.Helper()
	var j store.Job
	require.Eventually(t, func() bool {
		var err error
		j, err = st.Jobs().GetAny(ctx, id)
		return err == nil && j.Status == status
	}, 3*time.Second, 5*time.Millisecond, "job never reached %s (last: %s)", status, j.Status)
	return j
}
