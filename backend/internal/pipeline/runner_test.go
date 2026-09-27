package pipeline

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
)

const ws store.WorkspaceID = "ws-1"

// fakeSource serves the people table from memory as a SnapshotReader session.
type fakeSource struct {
	mu     sync.Mutex
	rows   [][]any // sorted by id (int32 at [0])
	delay  time.Duration
	reads  int
	opened int
	// starts records each partition's first read: its bounds and where it continued after.
	starts []readStart
	// related serves embed lookups (nil: none).
	related *fakeRelated
}

type readStart struct {
	lo, hi any
	after  []any
}

func (f *fakeSource) Ping(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{}, nil
}

func (f *fakeSource) CheckPrivileges(context.Context) (source.PrivilegeReport, error) {
	return source.PrivilegeReport{}, nil
}

func (f *fakeSource) EstimateRelations(context.Context, source.DiscoverOptions) (int, error) {
	return 0, nil
}

func (f *fakeSource) Discover(context.Context, source.DiscoverOptions) (*schema.Snapshot, error) {
	return nil, nil
}

func (f *fakeSource) Preview(context.Context, source.PreviewRequest) ([][]any, error) {
	return nil, nil
}
func (f *fakeSource) Close() error { return nil }
func (f *fakeSource) BeginRead(context.Context, string, bool) (source.ReadSession, error) {
	return &fakeRead{f: f}, nil
}

type fakeRead struct{ f *fakeSource }

func (r *fakeRead) SnapshotID() string { return "snap" }
func (r *fakeRead) KeyBounds(context.Context, source.TableRead) (any, any, error) {
	return int64(r.f.rows[0][0].(int32)), int64(r.f.rows[len(r.f.rows)-1][0].(int32)), nil
}

func (r *fakeRead) Count(context.Context, source.TableRead) (int64, error) {
	return int64(len(r.f.rows)), nil
}

func (r *fakeRead) OpenPartition(_ context.Context, _ source.TableRead, lo, hi any) (source.PartitionReader, error) {
	r.f.mu.Lock()
	r.f.opened++
	r.f.mu.Unlock()
	return &fakePart{f: r.f, lo: lo, hi: hi}, nil
}

func (r *fakeRead) OpenCursor(context.Context, source.TableRead) (source.CursorReader, error) {
	r.f.mu.Lock()
	r.f.opened++
	r.f.mu.Unlock()
	return &fakeCursor{f: r.f}, nil
}

// fakeCursor reads the rows once from the start (a keyless table).
type fakeCursor struct {
	f   *fakeSource
	pos int
}

func (c *fakeCursor) Fetch(_ context.Context, n int) ([][]any, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	c.f.reads++
	end := min(c.pos+n, len(c.f.rows))
	out := c.f.rows[c.pos:end]
	c.pos = end
	return out, nil
}
func (c *fakeCursor) Close() error { return nil }

func (r *fakeRead) ReadKeys(_ context.Context, _ source.TableRead, keys [][]any) ([][]any, error) {
	want := map[int64]bool{}
	for _, k := range keys {
		want[int64(k[0].(float64))] = true
	}
	var out [][]any
	for _, row := range r.f.rows {
		if want[int64(row[0].(int32))] {
			out = append(out, row)
		}
	}
	return out, nil
}
func (r *fakeRead) Close() error { return nil }

type fakePart struct {
	f       *fakeSource
	lo, hi  any
	started bool
}

func (p *fakePart) ReadAfter(_ context.Context, after []any, limit int) ([][]any, error) {
	p.f.mu.Lock()
	p.f.reads++
	d := p.f.delay
	if !p.started {
		p.started = true
		p.f.starts = append(p.f.starts, readStart{lo: p.lo, hi: p.hi, after: after})
	}
	p.f.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
	// Keyset read: rows are sorted by id, so binary-search the first candidate.
	from := int64(-1 << 62)
	if after != nil {
		from = toI64(after[0]) + 1
	}
	if p.lo != nil {
		from = max(from, p.lo.(int64))
	}
	rows := p.f.rows
	i := sort.Search(len(rows), func(i int) bool { return int64(rows[i][0].(int32)) >= from })
	var out [][]any
	for ; i < len(rows) && len(out) < limit; i++ {
		if p.hi != nil && int64(rows[i][0].(int32)) >= p.hi.(int64) {
			break
		}
		out = append(out, rows[i])
	}
	return out, nil
}
func (p *fakePart) Close() error { return nil }

func toI64(v any) int64 {
	switch x := v.(type) {
	case int32:
		return int64(x)
	case int64:
		return x
	}
	return 0
}

type runFixture struct {
	st      *memstore.Store
	src     *fakeSource
	w       *memWriter
	job     store.Job
	runner  *Runner
	project store.Project
	tables  []schema.Table
	// writeOpts records what each writer was opened with.
	writeOpts []target.WriteOptions
}

func newRunFixture(t *testing.T, rows [][]any, estimate int64, opts jobs.Options) *runFixture {
	t.Helper()
	st := memstore.New(storetest.NewClock())
	p, err := st.Projects().Create(context.Background(), ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	raw, _ := json.Marshal(peopleSpec())
	m, err := st.Mappings().Create(context.Background(), ws, store.Mapping{
		ProjectID: p.ID, SourceSchema: "hr", SourceTable: "people", Spec: raw, Status: "ready",
		Target: store.MappingTarget{Bucket: "b", Scope: "s", Collection: "people"},
	})
	require.NoError(t, err)
	opts.MappingIDs = []string{m.ID}
	optRaw, _ := json.Marshal(opts)
	j, err := st.Jobs().Create(context.Background(), ws, store.Job{ProjectID: p.ID, Options: optRaw, MappingIDs: []string{m.ID}})
	require.NoError(t, err)
	table := peopleTable()
	table.RowEstimate = &estimate
	f := &runFixture{st: st, src: &fakeSource{rows: rows}, w: newMemWriter(), job: j, project: p, tables: []schema.Table{*table}}
	f.runner = NewRunner(f.deps())
	return f
}

func (f *runFixture) deps() Deps {
	return Deps{
		Store: f.st,
		OpenSource: func(context.Context, store.WorkspaceID, string) (source.Session, store.Connection, error) {
			return f.src, store.Connection{}, nil
		},
		OpenWriter: func(_ context.Context, _ store.WorkspaceID, _ string, o target.WriteOptions) (target.DocWriter, error) {
			f.src.mu.Lock()
			f.writeOpts = append(f.writeOpts, o)
			f.src.mu.Unlock()
			return f.w, nil
		},
		Latest: func(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
			return store.DiscoverySnapshot{}, &schema.Snapshot{Schemas: []schema.Schema{{Name: "hr", Tables: f.tables}}}, nil
		},
		PublishInterval: 10 * time.Millisecond, TransformWorkers: 3, WriterWorkers: 2,
	}
}

func (f *runFixture) run(ctl jobs.Control) jobs.Outcome {
	if ctl.Drain == nil {
		ctl.Drain = make(chan struct{})
	}
	if ctl.Abort == nil {
		ctl.Abort = make(chan struct{})
	}
	return f.runner.Start(context.Background(), f.job, ctl).Wait()
}

func opts(batch int) jobs.Options {
	o := jobs.DefaultOptions()
	o.BatchSize = batch
	return o
}

func TestRunner_EndToEndWithFakes(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 2500, 7, 1500), 2500, opts(100))
	o := f.run(jobs.Control{})
	require.True(t, o.Finished)
	require.Nil(t, o.Fatal)
	require.EqualValues(t, 2, o.Failed)
	require.Equal(t, 2498, f.w.count())
	require.Equal(t, jobs.EvCompleteWithErrors, jobs.FinalEvent(o))

	snap := progress.Parse(f.job.ID, o.Progress)
	require.EqualValues(t, 2498, snap.Written)
	require.EqualValues(t, 2, snap.Failed)
	require.EqualValues(t, 100, snap.Percent, "final")
	require.Equal(t, progress.PhaseDone, snap.Mappings[0].Phase)
	require.Equal(t, []progress.CodeCount{{Code: "PARSE_FAILED", Count: 2}}, snap.ErrorsByCode)

	errs, _, err := f.st.Jobs().ListErrors(context.Background(), ws, f.job.ID, store.JobErrorFilter{}, store.Page{})
	require.NoError(t, err)
	require.Len(t, errs, 2)
	// Batches are acknowledged in any order, so the errors' numbering can be too.
	sort.Slice(errs, func(i, j int) bool { return len(errs[i].SourceKey) < len(errs[j].SourceKey) })
	require.JSONEq(t, `[7]`, string(errs[0].SourceKey))
	require.JSONEq(t, `[1500]`, string(errs[1].SourceKey))
	require.Contains(t, string(errs[0].Sample), `"age":"x"`)

	cp, err := ParseCheckpoint(o.Checkpoint)
	require.NoError(t, err)
	require.True(t, cp.Mappings[f.job.MappingIDs[0]].Done)
	require.EqualValues(t, 2, cp.ErrorSeq)
	_, _, err = f.st.Jobs().GetProgress(context.Background(), f.job.ID)
	require.NoError(t, err, "progress was published")
}

func TestRunner_ParallelPartitionsForLargeTables(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 3000), 3_000_000, opts(100)) // the estimate asks for 3 readers
	o := f.run(jobs.Control{})
	require.True(t, o.Finished)
	require.Equal(t, 3000, f.w.count())
	require.Equal(t, 3, f.src.opened)
	cp, _ := ParseCheckpoint(o.Checkpoint)
	require.Len(t, cp.Mappings[f.job.MappingIDs[0]].Partitions, 3)
}

func TestPipeline_AC5_FatalErrorStopsJob(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 5000), 5000, opts(100))
	f.w.fail["person::150"] = target.WriteResult{Outcome: target.Failed, Code: target.CodeKeyspaceMissing, Fatal: true}
	f.src.delay = time.Millisecond
	o := f.run(jobs.Control{})
	require.NotNil(t, o.Fatal)
	require.Equal(t, target.CodeKeyspaceMissing, o.Fatal.Code)
	require.False(t, o.Finished)
	require.Less(t, f.w.count(), 5000, "stopped early")
	require.Equal(t, jobs.EvFail, jobs.FinalEvent(o))
}

func TestPipeline_AC3_FatalAbortsImmediately(t *testing.T) {
	// A threshold breach is fatal too: maxErrors 5 with every 10th row bad.
	var bad []int32
	for i := int32(10); i <= 5000; i += 10 {
		bad = append(bad, i)
	}
	o2 := opts(100)
	o2.ErrorPolicy.MaxErrors = 5
	f := newRunFixture(t, peopleRows(1, 5000, bad...), 5000, o2)
	f.src.delay = time.Millisecond
	o := f.run(jobs.Control{})
	require.NotNil(t, o.Fatal)
	require.Equal(t, jobs.ReasonErrorThreshold, o.Fatal.Code)
	require.Contains(t, o.Fatal.Message, "the limit is 5")
	require.Less(t, f.w.count(), 4500)
}

func TestRunner_DrainThenResumeHasNoGapsOrDuplicates(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 4000), 4000, opts(100))
	f.src.delay = 2 * time.Millisecond
	drain := make(chan struct{})
	go func() {
		require.Eventually(t, func() bool { return f.w.count() >= 1000 }, 5*time.Second, time.Millisecond)
		close(drain)
	}()
	o := f.run(jobs.Control{Drain: drain})
	require.False(t, o.Finished, "paused part-way")
	require.Nil(t, o.Fatal)
	first := f.w.count()
	require.Less(t, first, 4000)
	cp, _ := ParseCheckpoint(o.Checkpoint)
	part := cp.Mappings[f.job.MappingIDs[0]].Partitions[0]
	require.EqualValues(t, first/100, part.Seq, "every acknowledged batch is in the checkpoint")

	// Resume: a new run from the stored checkpoint writes exactly the rest.
	f.job.Checkpoint = o.Checkpoint
	f.job.Progress = o.Progress
	f.src.delay = 0
	before := f.w.calls
	o = f.run(jobs.Control{})
	require.True(t, o.Finished)
	require.Equal(t, 4000, f.w.count())
	require.Equal(t, 40-first/100, f.w.calls-before, "no batch written twice")
	snap := progress.Parse(f.job.ID, o.Progress)
	require.EqualValues(t, 4000, snap.Written, "progress continues across the resume")
}

func TestRetry_AC6_KeyFilteredReader(t *testing.T) {
	bad := []int32{12, 50, 99, 150, 201, 250, 299}
	f := newRunFixture(t, peopleRows(1, 300, bad...), 300, opts(100))
	o := f.run(jobs.Control{})
	require.EqualValues(t, 7, o.Failed)

	// The user fixes the data; a retry job reads just the failed keys.
	f.src.rows = peopleRows(1, 300)
	ro := opts(100)
	ro.RetryOf, ro.KeyFilter = f.job.ID, "failed"
	ro.MappingIDs = f.job.MappingIDs
	raw, _ := json.Marshal(ro)
	retry := f.job
	retry.ID, retry.Options, retry.Checkpoint, retry.Progress = "retry-1", raw, nil, nil
	f.job = retry
	f.w = newMemWriter()
	o = f.run(jobs.Control{})
	require.True(t, o.Finished)
	require.Zero(t, o.Failed)
	keys := make([]string, 0, len(f.w.docs))
	for k := range f.w.docs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	require.Equal(t, []string{"person::12", "person::150", "person::201", "person::250", "person::299", "person::50", "person::99"}, keys,
		"exactly the 7 failed keys, with the current (fixed) data")
	snap := progress.Parse(f.job.ID, o.Progress)
	require.EqualValues(t, 7, snap.Total)
	require.Equal(t, progress.TotalExact, snap.TotalKind)
}
