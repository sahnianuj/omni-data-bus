package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/jobs/jobspec"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/pipeline/flowctl"
	"omnidatabus/internal/plan"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/related"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/transform"
	"omnidatabus/internal/verify"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Fatal codes raised by the pipeline itself.
const (
	CodeMappingMissing     = "MAPPING_MISSING"
	CodeSourceTableMissing = "SOURCE_TABLE_MISSING"
	CodeMappingInvalid     = "MAPPING_INVALID"
	CodeSourceUnsupported  = "SOURCE_READ_UNSUPPORTED"
	CodeTargetUnsupported  = "TARGET_WRITE_UNSUPPORTED"
)

// Deps are the runner's collaborators; every lookup uses the project's stored connections.
type Deps struct {
	Store      store.Store
	OpenSource func(ctx context.Context, ws store.WorkspaceID, connectionID string) (source.Session, store.Connection, error)
	OpenWriter func(ctx context.Context, ws store.WorkspaceID, connectionID string, o target.WriteOptions) (target.DocWriter, error)
	Latest     func(ctx context.Context, ws store.WorkspaceID, connectionID string) (store.DiscoverySnapshot, *schema.Snapshot, error)
	Now        func() time.Time
	Log        *slog.Logger

	// Tuning (zero = default).
	MemoryBudgetBytes int64         // OC_WORKER_MEMORY_BUDGET_MB, default 256 MB
	ErrorRetention    time.Duration // job_errors expiry, default 30 days
	MaxStoredErrors   int64         // OC_JOB_ERRORS_MAX_ROWS, default 100k
	PublishInterval   time.Duration // progress tick, default 1 s
	TransformWorkers  int           // default GOMAXPROCS
	WriterWorkers     int           // batches written at once, default 4
	Read              ReadConfig    // retry and clock overrides for tests
	// DrainTimeout bounds a pause: in-flight batches that aren't written by then are cancelled
	// and left out of the checkpoint (default 30 s, OC-029).
	DrainTimeout time.Duration
	// PersistEvery and PersistBatches set the checkpoint cadence (default 5 s / 50 batches).
	PersistEvery   time.Duration
	PersistBatches int
	// CountTarget counts the documents at a location (verification, OC-030); nil = skip.
	CountTarget func(ctx context.Context, ws store.WorkspaceID, connectionID string, loc store.Location) (int64, error)
}

// Runner implements jobspec.Runner.
type Runner struct{ d Deps }

// NewRunner returns the pipeline runner.
func NewRunner(d Deps) *Runner {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.MemoryBudgetBytes == 0 {
		d.MemoryBudgetBytes = 256 << 20
	}
	if d.ErrorRetention == 0 {
		d.ErrorRetention = 30 * 24 * time.Hour
	}
	if d.PublishInterval == 0 {
		d.PublishInterval = time.Second
	}
	if d.TransformWorkers == 0 {
		d.TransformWorkers = runtime.GOMAXPROCS(0)
	}
	if d.WriterWorkers == 0 {
		d.WriterWorkers = 4
	}
	if d.DrainTimeout == 0 {
		d.DrainTimeout = 30 * time.Second
	}
	if d.PersistEvery == 0 {
		d.PersistEvery = PersistEvery
	}
	if d.PersistBatches == 0 {
		d.PersistBatches = PersistBatches
	}
	return &Runner{d: d}
}

type execution struct {
	r    *Runner
	job  store.Job
	ctl  jobspec.Control
	opts jobspec.Options
	// limiter is maxDocsPerSecond, shared by every table of the job (nil = unthrottled).
	limiter *flowctl.TokenBucket

	log     *slog.Logger // job-scoped: job_id, project_id
	metrics *observability.JobMetrics

	mu      sync.Mutex
	cp      Checkpoint
	live    map[string]*checkpointer
	tracker *progress.Tracker
	sink    *ErrorSink
	persist *persister
	fatal   atomic.Pointer[jobspec.Fatal]
	cancel  context.CancelFunc
	done    chan struct{}
	outcome jobspec.Outcome

	reportMu sync.Mutex
	report   verify.ReportDoc
}

// Start implements jobspec.Runner.
func (r *Runner) Start(ctx context.Context, j store.Job, ctl jobspec.Control) jobspec.Execution {
	e := &execution{r: r, job: j, ctl: ctl, live: map[string]*checkpointer{}, done: make(chan struct{})}
	e.log = r.d.Log.With("job_id", j.ID, "project_id", j.ProjectID)
	go func() {
		defer close(e.done)
		// A run outlives the request that claimed it; it stops through Control, not ctx.
		e.outcome = e.run(context.WithoutCancel(ctx))
	}()
	return e
}

func (e *execution) Wait() jobspec.Outcome {
	<-e.done
	return e.outcome
}

// Checkpoint implements jobspec.Execution: the last durably persisted position (the heartbeat
// writes it back unchanged, so it never runs ahead of the stored errors).
func (e *execution) Checkpoint() json.RawMessage {
	e.mu.Lock()
	p := e.persist
	e.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.durable()
}

// latest is the live position of every mapping.
func (e *execution) latest() json.RawMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.r.d.Now()
	cp := Checkpoint{Version: CheckpointVersion, UpdatedAt: &now, Mappings: map[string]*MappingCheckpoint{}}
	for id, m := range e.cp.Mappings {
		c := *m
		c.describe()
		cp.Mappings[id] = &c
	}
	for id, c := range e.live {
		snap := c.snapshot()
		snap.describe()
		cp.Mappings[id] = &snap
	}
	if e.sink != nil {
		cp.ErrorSeq = e.sink.Seq()
	}
	raw, _ := json.Marshal(cp)
	return raw
}

// save stores the checkpoint on the job, fenced by ownership: when the job's claim moved to
// another worker it returns ErrLostOwnership (OC-029 AC2).
func (e *execution) save(ctx context.Context, raw json.RawMessage) error {
	ctx = context.WithoutCancel(ctx)
	for attempt := 0; attempt < 10; attempt++ {
		cur, err := e.r.d.Store.Jobs().GetAny(ctx, e.job.ID)
		if err != nil {
			return err
		}
		if cur.ClaimedBy != e.job.ClaimedBy || store.JobTerminal(cur.Status) {
			return ErrLostOwnership
		}
		cur.Checkpoint = raw
		if _, err = e.r.d.Store.Jobs().Update(ctx, cur); !errors.Is(err, store.ErrVersionConflict) {
			return err
		}
	}
	return store.ErrVersionConflict
}

// persisted reacts to a persist result: a fenced-out run stops at once.
func (e *execution) persisted(err error) {
	switch {
	case err == nil:
	case errors.Is(err, ErrLostOwnership):
		e.fail(CodeLostOwnership, "Another worker took over this job.")
	default:
		// The next persist retries; the heartbeat keeps the last durable position.
		e.log.Warn("checkpoint persist", "err", err)
	}
}

func (e *execution) event(ctx context.Context, kind, msg string) {
	// The timeline is written even while the run is stopping, so cancellation is ignored.
	_ = e.r.d.Store.Jobs().AppendEvent(context.WithoutCancel(ctx), e.job.ID, store.JobEvent{At: e.r.d.Now(), Kind: kind, Message: msg})
}

func (e *execution) fail(code, msg string) {
	e.fatal.CompareAndSwap(nil, &jobspec.Fatal{Code: code, Message: msg})
	e.cancel()
}

func drained(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (e *execution) run(base context.Context) jobspec.Outcome {
	ctx, cancel := context.WithCancel(base)
	e.cancel = cancel
	defer cancel()
	// One root span per run; mapping phases are its children (OC-031).
	ctx, span := observability.Tracer().Start(ctx, "job.run", trace.WithAttributes(
		attribute.String("job.id", e.job.ID), attribute.String("project.id", e.job.ProjectID)))
	defer func() {
		if f := e.fatal.Load(); f != nil {
			span.SetStatus(codes.Error, f.Code)
		}
		span.End()
	}()
	go func() {
		select {
		case <-e.ctl.Abort:
			cancel()
		case <-ctx.Done():
		}
	}()
	d := e.r.d
	// A pause drains: readers stop at the next batch and in-flight batches finish, for at most
	// DrainTimeout; then they are cancelled and stay out of the checkpoint (OC-029 AC5).
	go func() {
		select {
		case <-e.ctl.Drain:
		case <-ctx.Done():
			return
		}
		t := time.NewTimer(d.DrainTimeout)
		defer t.Stop()
		select {
		case <-t.C:
			e.event(ctx, "warning", fmt.Sprintf("Batches still writing after %s were cancelled; they will be written on resume", d.DrainTimeout))
			cancel()
		case <-ctx.Done():
		}
	}()
	ws := e.job.WorkspaceID
	var err error
	if e.opts, err = jobspec.ParseOptions(e.job.Options); err != nil {
		return jobspec.Outcome{Fatal: &jobspec.Fatal{Code: CodeMappingInvalid, Message: "Invalid job options: " + err.Error()}}
	}
	if rate := e.opts.MaxDocsPerSecond; rate != nil {
		// One bucket for the whole job: tables running in parallel share the rate, so the limit
		// is what the cluster sees, not a per-table allowance multiplied by tableConcurrency.
		e.limiter = flowctl.NewTokenBucket(float64(*rate), max(e.opts.BatchSize, *rate), e.r.d.Read.Clock)
	}
	cp, err := ParseCheckpoint(e.job.Checkpoint)
	if err != nil {
		cp = Checkpoint{Mappings: map[string]*MappingCheckpoint{}}
	}
	e.mu.Lock()
	e.cp = cp
	e.mu.Unlock()
	resumed := len(cp.Mappings) > 0

	// The mappings, and their tables from the latest discovery snapshot.
	var maps []store.Mapping
	for _, id := range e.job.MappingIDs {
		m, err := d.Store.Mappings().Get(ctx, ws, id)
		if err != nil {
			return jobspec.Outcome{Fatal: &jobspec.Fatal{Code: CodeMappingMissing, Message: "Mapping " + id + " no longer exists."}}
		}
		// A job runs the spec frozen when it was created, so edits while it's paused don't
		// change a resumed run (OC-029 AC7).
		if frozen, ok := e.opts.Specs[id]; ok && len(frozen) > 0 {
			m.Spec = frozen
		}
		maps = append(maps, m)
	}
	srcID := store.ConnectionID(e.job.ProjectID, store.RoleSource)
	tgtID := store.ConnectionID(e.job.ProjectID, store.RoleTarget)
	var snap *schema.Snapshot
	if d.Latest != nil {
		_, snap, _ = d.Latest(ctx, ws, srcID)
	}
	var infos []progress.MappingProgress
	for _, m := range maps {
		schemaName, table := m.Driving()
		mp := progress.MappingProgress{
			MappingID: m.ID, Table: schemaName + "." + table,
			Target: m.Where().String(), TotalKind: progress.TotalEstimate,
		}
		if snap != nil {
			if t := snap.Table(schemaName, table); t != nil && t.RowEstimate != nil {
				mp.Total = *t.RowEstimate
			}
		}
		if e.opts.KeyFilter != "" {
			mp.Total = 0 // set from the key count below
		}
		infos = append(infos, mp)
	}
	e.tracker = progress.NewTracker(e.job.ID, infos, d.Now)
	span.SetAttributes(attribute.Int("job.mappings", len(maps)), attribute.Bool("job.resumed", resumed))
	if resumed {
		prev := e.job.Progress
		if raw, _, err := d.Store.Jobs().GetProgress(ctx, e.job.ID); err == nil {
			prev = raw
		}
		e.tracker.Resume(progress.Parse(e.job.ID, prev))
		// The checkpoint's counts are authoritative: they match exactly what was checkpointed.
		var done int64
		for id, mc := range cp.Mappings {
			e.tracker.ResumeMapping(id, mc.Written, mc.Skipped, mc.Failed, mc.Done)
			if !mc.Cursor || mc.Done {
				done += mc.Processed()
			}
		}
		e.event(ctx, "resumed", fmt.Sprintf("Resumed from checkpoint (%s rows already processed); rows changed after the pause may be read with newer values", thousands(done)))
	}
	start := e.tracker.Snapshot(store.JobRunning)
	e.metrics = observability.StartJob(e.job.ID, start.Written, start.Skipped, start.Failed)
	defer e.metrics.End()
	e.log.Info("job run started", "mappings", len(maps), "resumed", resumed)
	// Earlier runs' verification results (a resumed job keeps them).
	prevReport, _ := d.Store.Jobs().GetReport(ctx, e.job.ID)
	e.report = verify.ParseReport(prevReport)
	sink := NewErrorSink(d.Store.Jobs(), e.job.ID, ws, d.ErrorRetention, d.MaxStoredErrors, cp.ErrorSeq, nil)
	per := newPersister(d.Now, sink.Flush, e.save, e.latest)
	per.every, per.batches = d.PersistEvery, d.PersistBatches
	e.mu.Lock()
	e.sink = sink
	e.persist = per
	e.mu.Unlock()
	sinkCtx, stopSink := context.WithCancel(base) // flushes until the final errors are stored
	go e.sink.Run(sinkCtx)
	pubCtx, stopPub := context.WithCancel(base)
	pubDone := make(chan struct{})
	go func() { defer close(pubDone); e.publish(pubCtx) }()

	// Mappings run tableConcurrency at a time, in order.
	slots := make(chan struct{}, max(e.opts.TableConcurrency, 1))
	var wg sync.WaitGroup
	for _, m := range maps {
		if cp := e.currentCheckpoint(m.ID); cp != nil && cp.Done {
			e.tracker.SetPhase(m.ID, progress.PhaseDone)
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil || drained(e.ctl.Drain) {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			e.runMapping(ctx, m, snap, srcID, tgtID)
		}()
	}
	wg.Wait()

	e.sink.Flush(base) // even after an abort: stored errors must be complete
	stopSink()
	finished := e.fatal.Load() == nil && ctx.Err() == nil
	for _, m := range maps {
		if cp := e.currentCheckpoint(m.ID); cp == nil || !cp.Done {
			finished = false
		}
	}
	if finished {
		e.tracker.Finish()
	}
	stopPub()
	<-pubDone
	_, _, truncated, _ := e.sink.Stats()
	_, failed := e.tracker.Totals()
	final := e.tracker.Snapshot(store.JobRunning)
	e.metrics.Observe(final.Written, final.Skipped, final.Failed, 0)
	e.log.Info("job run ended", "finished", finished, "written", final.Written, "skipped", final.Skipped, "failed", final.Failed)
	raw, _ := json.Marshal(final)
	return jobspec.Outcome{
		Finished: finished, Failed: failed, Fatal: e.fatal.Load(), Checkpoint: e.latest(), Progress: raw, ErrorsTruncated: truncated,
	}
}

func (e *execution) currentCheckpoint(id string) *MappingCheckpoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.live[id]; ok {
		s := c.snapshot()
		return &s
	}
	return e.cp.Mappings[id]
}

// publish writes the progress document every tick (OC-027): KV upsert of job_progress::<id>.
func (e *execution) publish(ctx context.Context) {
	t := time.NewTicker(e.r.d.PublishInterval)
	defer t.Stop()
	status := store.JobRunning
	for first := true; ; first = false {
		if !first {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
		if cur, err := e.r.d.Store.Jobs().GetAny(ctx, e.job.ID); err == nil && !store.JobTerminal(cur.Status) {
			status = cur.Status
		}
		s := e.tracker.Snapshot(status)
		e.metrics.Observe(s.Written, s.Skipped, s.Failed, s.Throughput)
		raw, _ := json.Marshal(s)
		_ = e.r.d.Store.Jobs().PutProgress(ctx, e.job.ID, raw)
	}
}

// sensitive are columns whose mappings mask or hash them: their values are masked in stored
// error samples (OC-026 AC1).
func sensitive(spec *mapping.Spec) map[string]bool {
	out := map[string]bool{}
	for _, fm := range spec.FieldMappings {
		for _, st := range fm.Transforms {
			if st.Op == "mask" || st.Op == "hash" {
				for _, s := range fm.Sources {
					out[s.Column] = true
				}
			}
		}
	}
	return out
}

// readKey is the keyset: the primary key, else a not-null unique key, else none (cursor).
func readKey(t *schema.Table) []string {
	if len(t.PrimaryKey) > 0 {
		return t.PrimaryKey
	}
	for _, u := range t.UniqueConstraints {
		ok := len(u) > 0
		for _, c := range u {
			if col := t.Column(c); col == nil || col.Nullable {
				ok = false
			}
		}
		if ok {
			return u
		}
	}
	return nil
}

func (e *execution) runMapping(parent context.Context, m store.Mapping, snap *schema.Snapshot, srcID, tgtID string) {
	d := e.r.d
	ws := e.job.WorkspaceID
	tr := e.tracker
	log := e.log.With("mapping_id", m.ID)
	// A composed mapping is driven by its spec's root table (OC-047).
	srcSchema, srcTable := m.Driving()
	parent, mspan := observability.Tracer().Start(parent, "job.mapping", trace.WithAttributes(
		attribute.String("mapping.id", m.ID), attribute.String("mapping.source", srcSchema+"."+srcTable)))
	defer mspan.End()
	// Phase spans: prepare, then migrate (with one event per batch), then verify.
	_, prep := observability.Tracer().Start(parent, "mapping.prepare")
	defer prep.End() // no-op once ended
	tr.SetPhase(m.ID, progress.PhasePreparing)
	log.Debug("mapping preparing")
	failMapping := func(code, msg string) {
		tr.SetPhase(m.ID, progress.PhaseFailed)
		mspan.SetStatus(codes.Error, code)
		log.Warn("mapping failed", "code", code, "detail", msg)
		e.fail(code, srcSchema+"."+srcTable+": "+msg)
	}
	var t *schema.Table
	if snap != nil {
		t = snap.Table(srcSchema, srcTable)
	}
	if t == nil {
		failMapping(CodeSourceTableMissing, "the table isn't in the latest schema snapshot")
		return
	}
	var spec mapping.Spec
	if err := json.Unmarshal(m.Spec, &spec); err != nil {
		failMapping(CodeMappingInvalid, err.Error())
		return
	}
	spec.Normalize()
	// One execution plan, the same the preview and the headless runner build (OC-037).
	pl, issues, err := plan.Compile(plan.Input{
		Spec: &spec, Root: mapping.TableRef{Schema: srcSchema, Name: srcTable}, Table: t,
		Related: func(schemaName, table string) *schema.Table { return snap.Table(schemaName, table) },
		Options: transform.Options{Now: d.Now()},
	})
	var perr *plan.Error
	switch {
	case errors.As(err, &perr) && perr.Code == plan.CodeRelatedTableMissing:
		failMapping(CodeSourceTableMissing, err.Error()) // related tables must exist before anything else
		return
	case err != nil:
		failMapping(CodeMappingInvalid, err.Error())
		return
	case pl.Program == nil:
		failMapping(CodeMappingInvalid, plan.ErrorMessages(issues))
		return
	}
	log.Debug("mapping plan", "plan_hash", pl.Hash, "columns", len(pl.Columns), "relations", len(pl.Relations))

	key := readKey(t)
	read := source.TableRead{Schema: srcSchema, Table: srcTable, Columns: t.Columns, Key: key, Project: pl.Project(key...)}
	if g := spec.GroupBy(); len(g) > 0 {
		// One document per group (OC-063): the read pages, counts, checkpoints and retries by
		// the group-by columns, one row (the first by primary key) per group; the group's rows
		// come through its relation.
		read.Key, read.Distinct, read.Within, read.Project = g, true, key, pl.Project(g...)
		key = g // partitions split on the group column, so no group spans two
	}

	e.sink.Describe(m.ID, TableInfo{Columns: columnNames(t), Sensitive: sensitive(&spec)})

	sess, _, err := d.OpenSource(parent, ws, srcID)
	if err != nil {
		failMapping(errCode(err, "SOURCE_UNREACHABLE"), errText(err))
		return
	}
	defer func() { _ = sess.Close() }()
	sr, ok := sess.(source.SnapshotReader)
	if !ok {
		failMapping(CodeSourceUnsupported, "this source can't stream tables")
		return
	}

	// The checkpoint (resume) or a fresh partition plan.
	e.mu.Lock()
	cp := e.cp.Mappings[m.ID]
	e.mu.Unlock()
	fresh := cp == nil
	retry := e.opts.KeyFilter == "failed"
	kind := KeyOther
	if len(key) == 1 {
		if c := t.Column(key[0]); c != nil {
			kind = KindOf(c.UDTName, 1)
		}
	}
	var estimate int64
	if t.RowEstimate != nil {
		estimate = *t.RowEstimate
	}
	k := 1
	if fresh && !retry && len(key) > 0 {
		k = PartitionCount(kind, estimate, e.opts.Parallelism())
	} else if cp != nil {
		k = len(cp.Partitions)
	}
	app := "omni-data-bus job " + e.job.ID
	rs, err := sr.BeginRead(parent, app, k > 1)
	if err != nil {
		failMapping(CodeSourceReadFailed, err.Error())
		return
	}
	defer func() { _ = rs.Close() }()
	var resolver *related.Resolver
	if pl.HasRelations() {
		rr, ok := rs.(source.RelatedReader)
		if !ok {
			failMapping(CodeSourceUnsupported, "this source can't read embedded tables")
			return
		}
		// Embedded rows are read in the same snapshot as their parents (OC-021 AC5).
		resolver = &related.Resolver{Plans: pl.Lookups(), Reader: rr, Concurrency: related.DefaultConcurrency}
	}
	if k > 1 && rs.SnapshotID() == "" {
		e.event(parent, "warning", read.Schema+"."+read.Table+": snapshot export unavailable; reading with one reader")
		if fresh {
			k = 1
		}
	} else if k > 1 {
		e.event(parent, "snapshot", read.Schema+"."+read.Table+": consistent snapshot taken")
	}
	if fresh {
		cp = &MappingCheckpoint{Cursor: len(key) == 0 && !retry}
		ranges := []Range{{}}
		if k > 1 {
			lo, hi, err := rs.KeyBounds(parent, read)
			if err != nil {
				failMapping(CodeSourceReadFailed, err.Error())
				return
			}
			ranges = PlanPartitions(kind, lo, hi, k)
			e.event(parent, "partitions", fmt.Sprintf("%s.%s: %d readers in parallel", read.Schema, read.Table, len(ranges)))
		}
		for _, r := range ranges {
			cp.Partitions = append(cp.Partitions, PartitionState{Lo: r.Lo, Hi: r.Hi})
		}
	} else if cp.Cursor {
		// A cursor read has no position: it starts over (OC-029 AC7).
		e.event(parent, "warning", fmt.Sprintf("%s: %s.%s has no primary key; restarted from the beginning", CodeMappingRestartedNoPK, read.Schema, read.Table))
		tr.ResetMapping(m.ID)
		cp.Written, cp.Skipped, cp.Failed = 0, 0, 0
		for i := range cp.Partitions {
			cp.Partitions[i] = PartitionState{}
		}
	}
	// On resume, an insert-mode re-write of rows written just before the stop reports "exists";
	// the first 2 batches of each partition count those as written (replayed), not skipped.
	replayUpTo := make([]uint64, len(cp.Partitions))
	if !fresh && !cp.Cursor && e.opts.WriteMode == jobspec.WriteInsert {
		for i, p := range cp.Partitions {
			replayUpTo[i] = p.Seq + 2
		}
	}
	var replayed int64
	// Verification (OC-030) samples written rows and counts distinct keys as they're written;
	// a resumed run didn't see earlier writes, so it can't count distinct keys.
	var col *collector
	if e.opts.VerifySampleSize > 0 && !retry {
		col = &collector{sample: verify.NewReservoir(e.opts.VerifySampleSize, nil), start: d.Now()}
		if fresh {
			col.keys = verify.NewKeys()
		}
	}
	processedBefore := cp.Processed()
	if cp.Cursor {
		processedBefore = 0
	}
	var rowsRead atomic.Int64
	ckp := newCheckpointer(cp)
	e.mu.Lock()
	e.live[m.ID] = ckp
	e.mu.Unlock()

	// Totals: the estimate now, the exact count in the background (OC-027).
	var keys [][]any
	if retry {
		keys, err = e.failedKeys(parent, m.ID)
		if err != nil {
			failMapping(CodeSourceReadFailed, err.Error())
			return
		}
		tr.SetTotal(m.ID, int64(len(keys)), true)
	} else if progress.CountExactly(e.opts.ExactCount, t.SizeBytes) || (read.Distinct && e.opts.ExactCount != "never") {
		// Grouped (OC-063): the row estimate isn't a document count, so count the groups.
		go func() {
			cctx, cancel := context.WithTimeout(parent, 10*time.Minute)
			defer cancel()
			if n, err := rs.Count(cctx, read); err == nil {
				tr.SetTotal(m.ID, n, true)
				e.event(parent, "count", fmt.Sprintf("%s.%s: exact count %d rows", read.Schema, read.Table, n))
			}
		}()
	}

	var avgRow int64
	if estimate > 0 && t.SizeBytes > 0 {
		avgRow = t.SizeBytes / estimate
	}
	cfg := d.Read
	cfg.BatchSize = BatchSizeFor(e.opts.BatchSize, avgRow, len(cp.Partitions), d.MemoryBudgetBytes)
	cfg.Limiter = e.limiter
	cfg.OnRetry = func(part, attempt int, err error) {
		log.Warn("source read retry", "partition", part, "attempt", attempt, "err", err)
	}

	w, err := d.OpenWriter(parent, ws, tgtID, target.WriteOptions{
		Location: m.Where(), Mode: e.opts.WriteMode,
		JobID: e.job.ID, MappingID: m.ID, PlanHash: pl.Hash, KeyTemplate: spec.Key.Template,
		Durability: e.opts.Durability, Expiry: time.Duration(spec.ExpirySec) * time.Second, Concurrency: e.opts.WriteConcurrency,
		OnConcurrency: func(limit int, reason string) {
			e.metrics.SetConcurrency(e.opts.WriteConcurrency, limit)
			e.event(parent, "concurrency", fmt.Sprintf("Concurrency %s to %d (%s)", map[bool]string{true: "reduced", false: "raised"}[reason == "cluster busy"], limit, reason))
		},
	})
	if err != nil {
		failMapping(errCode(err, "TARGET_UNREACHABLE"), errText(err))
		return
	}
	defer func() { _ = w.Close() }()
	if dl, ok := w.(target.DocLimiter); ok {
		pl.SetMaxDocBytes(dl.MaxDocBytes())
	}
	e.metrics.SetConcurrency(e.opts.WriteConcurrency, e.opts.WriteConcurrency)
	prep.End()
	tr.SetPhase(m.ID, progress.PhaseMigrating)
	log.Info("mapping migrating", "partitions", len(cp.Partitions), "resumed", !fresh)

	mctx, mig := observability.Tracer().Start(parent, "mapping.migrate")
	defer mig.End()
	// Bulk reads get batch events on this span, not a span per query.
	ctx, cancel := context.WithCancel(observability.WithoutDBSpans(mctx))
	defer cancel()
	rows := make(chan *source.Batch, RowsChanCap)
	counted := make(chan *source.Batch, RowsChanCap)
	resolved := counted
	docs := make(chan *DocBatch, DocsChanCap)
	acks := make(chan Ack, DocsChanCap)

	// Readers.
	readErrs := make([]error, len(cp.Partitions))
	var rwg sync.WaitGroup
	keyIdx := read.KeyIndexes()
	for i, p := range cp.Partitions {
		if p.Done {
			continue
		}
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			pr := read
			pr.App = fmt.Sprintf("%s p%d", app, i)
			switch {
			case retry:
				readErrs[i] = ReadKeys(ctx, rs, pr, keys, cfg, e.ctl.Drain, rows)
			case cp.Cursor:
				c, err := rs.OpenCursor(ctx, pr)
				if err != nil {
					readErrs[i] = &ReadError{Partition: i, Err: err}
					return
				}
				defer func() { _ = c.Close() }()
				readErrs[i] = ReadCursor(ctx, c, cfg, e.ctl.Drain, rows)
			default:
				r, err := rs.OpenPartition(ctx, pr, p.Lo, p.Hi)
				if err != nil {
					readErrs[i] = &ReadError{Partition: i, Err: err}
					return
				}
				defer func() { _ = r.Close() }()
				readErrs[i] = ReadPartition(ctx, r, i, keyIdx, p.LastKey, p.Seq, cfg, e.ctl.Drain, rows)
				if rsn, ok := r.(source.Resnapshotter); ok && rsn.Resnapshotted() {
					e.event(ctx, "warning", fmt.Sprintf("%s.%s: reader %d reconnected with a new snapshot", read.Schema, read.Table, i))
				}
			}
		}()
	}
	go func() { rwg.Wait(); close(rows) }()
	go func() {
		defer close(counted)
		for b := range rows {
			observability.ReaderRows.Add(float64(len(b.Rows)))
			tr.Read(m.ID, len(b.Rows))
			rowsRead.Add(int64(len(b.Rows)))
			select {
			case counted <- b:
			case <-ctx.Done():
				// Aborting: keep draining rows so the readers can exit.
			}
		}
	}()
	if resolver != nil {
		// Relation lookups for one batch run while the readers fetch the next ones.
		resolver.Do = func(ctx context.Context, read func() ([][]any, error)) ([][]any, error) {
			return readWithRetry(ctx, cfg.withDefaults(), 0, read)
		}
		resolved = make(chan *source.Batch, RowsChanCap)
		go func() {
			err := ResolveRelations(ctx, resolver, RelationWorkers, counted, resolved)
			if err != nil && ctx.Err() == nil {
				failMapping(CodeSourceReadFailed, err.Error())
			}
		}()
	}
	go func() {
		trf := Transformer{MappingID: m.ID, Workers: d.TransformWorkers, NewProgram: pl.NewProgram, KeyIdx: keyIdx}
		if err := trf.Run(ctx, resolved, docs); err != nil && ctx.Err() == nil {
			failMapping(CodeMappingInvalid, err.Error())
		}
	}()
	go Writer{MappingID: m.ID, DocWriter: w, Workers: d.WriterWorkers, KeepKeys: col != nil}.Run(ctx, docs, acks)

	policy := Policy{MaxErrors: e.opts.ErrorPolicy.MaxErrors, MaxErrorRatio: e.opts.ErrorPolicy.MaxErrorRatio, MinRowsForRatio: e.opts.ErrorPolicy.MinRowsForRatio}
	table := read.Schema + "." + read.Table
	for a := range acks {
		if a.Partition >= 0 && a.Partition < len(replayUpTo) && a.Seq <= replayUpTo[a.Partition] && a.Skipped > 0 {
			replayed += a.Skipped
			a.Written, a.Skipped = a.Written+a.Skipped, 0
		}
		// Errors go to the sink before the position moves, so a persisted checkpoint never
		// covers a failed row that isn't stored.
		if len(a.Errors) > 0 {
			e.sink.Add(context.WithoutCancel(ctx), a.Errors) // dead-letter even while aborting
		}
		col.add(a)
		mig.AddEvent("batch", trace.WithAttributes(attribute.Int("partition", a.Partition), attribute.Int64("seq", int64(a.Seq)), //nolint:gosec // batch sequence numbers fit
			attribute.Int64("written", a.Written), attribute.Int64("skipped", a.Skipped), attribute.Int64("failed", a.Failed)))
		log.Debug("batch written", "partition", a.Partition, "seq", a.Seq, "written", a.Written, "skipped", a.Skipped, "failed", a.Failed)
		n := ckp.ackPages(a.Partition, a.Seq, a.LastKey, a.Written, a.Skipped, a.Failed, a.Pages)
		tr.Acked(m.ID, a.Written, a.Skipped, a.Failed, a.Bytes)
		if n > 0 {
			e.persisted(e.persist.advanced(ctx, n, false))
		}
		if len(a.Errors) > 0 {
			for _, re := range a.Errors {
				observability.JobErrors.WithLabelValues(re.Stage, re.Code).Inc()
				sk, _ := json.Marshal(jsonValues(re.SourceKey))
				tr.Error(progress.ErrorSample{
					At: d.Now(), MappingID: m.ID, Table: table, Stage: re.Stage, Code: re.Code, Message: re.Message,
					FieldPath: re.FieldPath, SourceKey: sk,
				})
			}
		}
		if a.Fatal != nil {
			failMapping(a.Fatal.Code, a.Fatal.Message)
		}
		if processed, failed := tr.Totals(); failed > 0 {
			if over, why := policy.Exceeded(processed, failed); over {
				e.fail(jobspec.ReasonErrorThreshold, why)
			}
		}
	}
	rwg.Wait()
	mig.End()
	if replayed > 0 {
		e.event(parent, "replayed", fmt.Sprintf("%s: %s rows written again after the resume (insert mode) counted as written", table, thousands(replayed)))
	}
	partsDone := false
	for i, err := range readErrs {
		switch {
		case err == nil:
			if ctx.Err() == nil && !cp.Partitions[i].Done {
				ckp.partitionDone(i)
				partsDone = true
			}
		case errors.Is(err, ErrDrained), errors.Is(err, context.Canceled):
		default:
			var re *ReadError
			if errors.As(err, &re) {
				failMapping(CodeSourceReadFailed, err.Error())
			}
		}
	}
	final := ckp.snapshot()
	allDone := ctx.Err() == nil && e.fatal.Load() == nil
	for _, p := range final.Partitions {
		allDone = allDone && p.Done
	}
	written, skipped, failed := final.Written, final.Skipped, final.Failed
	final.Done = allDone
	e.mu.Lock()
	e.cp.Mappings[m.ID] = &final
	delete(e.live, m.ID)
	e.mu.Unlock()
	if partsDone && e.fatal.Load() == nil {
		// A finished partition (and a finished mapping) is persisted at once.
		e.persisted(e.persist.persist(parent))
	}
	if allDone && col != nil {
		tr.SetPhase(m.ID, progress.PhaseVerifying)
		vctx, vspan := observability.Tracer().Start(parent, "mapping.verify")
		e.saveVerification(parent, e.verifyMapping(vctx, verifyInput{
			mappingID: m.ID, read: read, keyIdx: keyIdx, sr: sr, compile: pl.NewProgram, plans: pl.Lookups(), writer: w, target: m.Where(),
			counts: final, rowsRead: processedBefore + rowsRead.Load(), col: col,
		}))
		vspan.End()
	}
	if allDone {
		tr.SetPhase(m.ID, progress.PhaseDone)
		e.event(ctx, "mapping-done", fmt.Sprintf("%s: %d written, %d skipped, %d failed", table, written, skipped, failed))
		log.Info("mapping done", "written", written, "skipped", skipped, "failed", failed)
	} else if e.fatal.Load() == nil {
		tr.SetPhase(m.ID, progress.PhaseQueued)
	}
}

// CodeMappingRestartedNoPK warns that a keyless table was re-read from the start on resume.
const CodeMappingRestartedNoPK = "MAPPING_RESTARTED_NO_PK"

// thousands formats n as 402,113.
func thousands(n int64) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

func columnNames(t *schema.Table) []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

// failedKeys returns the stored source keys of the original job's failures for this mapping
// (OC-026 retry), optionally only for some codes.
func (e *execution) failedKeys(ctx context.Context, mappingID string) ([][]any, error) {
	var out [][]any
	seen := map[string]bool{}
	cursor := ""
	for {
		page, next, err := e.r.d.Store.Jobs().ListErrors(ctx, e.job.WorkspaceID, e.opts.RetryOf, store.JobErrorFilter{MappingID: mappingID}, store.Page{Limit: 100, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, je := range page {
			if len(e.opts.OnlyCodes) > 0 && !contains(e.opts.OnlyCodes, je.Code) {
				continue
			}
			if len(je.SourceKey) == 0 || seen[string(je.SourceKey)] {
				continue
			}
			seen[string(je.SourceKey)] = true
			var k []any
			if err := json.Unmarshal(je.SourceKey, &k); err == nil {
				out = append(out, k)
			}
		}
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// errCode is the problem code carried by err, or def.
func errCode(err error, def string) string {
	var pr problem.Problemer
	var p problem.Problem
	switch {
	case errors.As(err, &p):
		return p.Code
	case errors.As(err, &pr):
		return pr.Problem().Code
	}
	return def
}

// errText prefers a problem's detail over its raw error string.
func errText(err error) string {
	var p problem.Problem
	if errors.As(err, &p) && p.Detail != "" {
		return p.Detail
	}
	var pr problem.Problemer
	if errors.As(err, &pr) && pr.Problem().Detail != "" {
		return pr.Problem().Detail
	}
	return err.Error()
}
