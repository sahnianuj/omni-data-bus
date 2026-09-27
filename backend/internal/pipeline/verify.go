package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/related"
	"omnidatabus/internal/store"
	"omnidatabus/internal/transform"
	"omnidatabus/internal/verify"
)

// verifyTimeout bounds one mapping's verification; targetCountTimeout the collection count.
const (
	verifyTimeout      = 2 * time.Minute
	targetCountTimeout = 10 * time.Second
)

// collector gathers what verification needs while a mapping runs.
type collector struct {
	sample *verify.Reservoir
	keys   *verify.Keys // nil when the run didn't see every write (resumed)
	mu     sync.Mutex
	bytes  int64
	docs   int64
	start  time.Time
}

func (c *collector) add(a Ack) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.bytes += a.Bytes
	c.docs += int64(len(a.WrittenDocs))
	c.mu.Unlock()
	for _, d := range a.WrittenDocs {
		c.sample.Add(verify.Sample{SourceKey: d.SourceKey, DocKey: d.Key})
		if c.keys != nil {
			c.keys.Add(d.Key)
		}
	}
}

// verifyInput is a finished mapping's context.
type verifyInput struct {
	mappingID string
	read      source.TableRead
	keyIdx    []int
	sr        source.SnapshotReader
	compile   func() (*transform.Program, error)
	plans     []related.Plan
	writer    target.DocWriter
	target    store.MappingTarget
	counts    MappingCheckpoint
	rowsRead  int64
	col       *collector
}

// verifyMapping reconciles the counts and checks the sampled rows (OC-030). Problems while
// verifying are reported in the result; they never fail the job.
func (e *execution) verifyMapping(parent context.Context, in verifyInput) verify.MappingResult {
	d := e.r.d
	ctx, cancel := context.WithTimeout(parent, verifyTimeout)
	defer cancel()
	res := verify.MappingResult{MappingID: in.mappingID}
	c := in.col
	elapsed := d.Now().Sub(c.start).Seconds()
	res.DurationSec = elapsed
	if c.docs > 0 {
		res.AvgDocBytes = c.bytes / c.docs
	}
	if elapsed > 0 {
		res.ThroughputAvg = float64(c.docs) / elapsed
	}
	res.ThroughputPeak = e.tracker.Snapshot(store.JobRunning).PeakThroughput

	ci := verify.CountsInput{RowsRead: in.rowsRead, Written: in.counts.Written, Skipped: in.counts.Skipped, Failed: in.counts.Failed, Mode: e.opts.WriteMode}
	if c.keys != nil {
		ci.Distinct, ci.Writes = c.keys.Estimate()
	}
	res.Counts = verify.Reconcile(ci)
	if e.opts.TargetEmpty[in.mappingID] && d.CountTarget != nil {
		// A collection's count trails the writes by a little: poll until it reaches the
		// expected number or stops moving.
		if n, ok := e.settledCount(ctx, in.target, res.Counts.ExpectedDocs); ok {
			ci.TargetCount = &n
			res.Counts = verify.Reconcile(ci)
		}
	}

	samples := c.sample.Items()
	switch {
	case len(in.read.Key) == 0:
		res.Error = "The table has no primary key, so sampled rows can't be read back."
	case len(samples) > 0:
		sr, err := e.sampleCheck(ctx, in, samples)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Sample = &sr
		}
	}
	res.Decide()
	return res
}

// countPoll is how often settledCount re-reads the collection count.
var countPoll = 500 * time.Millisecond

func (e *execution) settledCount(ctx context.Context, t store.MappingTarget, want int64) (int64, bool) {
	ctx, cancel := context.WithTimeout(ctx, targetCountTimeout)
	defer cancel()
	conn := store.ConnectionID(e.job.ProjectID, store.RoleTarget)
	var last int64 = -1
	for {
		n, err := e.r.d.CountTarget(ctx, e.job.WorkspaceID, conn, t)
		switch {
		case err != nil:
			return last, last >= 0
		case n == want:
			return n, true
		case n == last && n > want:
			return n, true // more than expected and stable: report it
		}
		last = n
		select {
		case <-ctx.Done():
			return last, true
		case <-time.After(countPoll):
		}
	}
}

func (e *execution) sampleCheck(ctx context.Context, in verifyInput, samples []verify.Sample) (verify.SampleResult, error) {
	reader, ok := in.writer.(target.DocReader)
	if !ok {
		return verify.SampleResult{}, fmt.Errorf("this target can't read documents back")
	}
	// A new transaction: rows changed since the snapshot show as changed at the source.
	rs, err := in.sr.BeginRead(ctx, "omni-data-bus verify "+e.job.ID, false)
	if err != nil {
		return verify.SampleResult{}, err
	}
	defer func() { _ = rs.Close() }()
	prog, err := in.compile()
	if err != nil {
		return verify.SampleResult{}, err
	}
	var resolver *related.Resolver
	if len(in.plans) > 0 {
		rr, ok := rs.(source.RelatedReader)
		if !ok {
			return verify.SampleResult{}, fmt.Errorf("this source can't read embedded tables")
		}
		resolver = &related.Resolver{Plans: in.plans, Reader: rr, Concurrency: related.DefaultConcurrency}
	}
	ch := verify.Checker{
		Read: func(ctx context.Context, keys [][]any) ([][]any, error) {
			// ReadKeys takes keys as stored in JSON (like a retry's).
			var jk [][]any
			for _, k := range keys {
				raw, _ := json.Marshal(jsonValues(k))
				var v []any
				_ = json.Unmarshal(raw, &v)
				jk = append(jk, v)
			}
			return rs.ReadKeys(ctx, in.read, jk)
		},
		KeyOf: func(row []any) []any { return keyOf(row, in.keyIdx) },
		Transform: func(ctx context.Context, rows [][]any) ([]verify.Built, error) {
			var rel map[string][][][]any
			if resolver != nil {
				var err error
				if rel, err = resolver.Resolve(ctx, rows); err != nil {
					return nil, err
				}
			}
			out := make([]verify.Built, len(rows))
			for i, row := range rows {
				key, doc, _, rerr := prog.ApplyRelated(row, related.Row(rel, i), nil)
				out[i] = verify.Built{Key: key, Doc: append([]byte(nil), doc...)}
				if rerr != nil {
					out[i].Err = rerr
				}
			}
			return out, nil
		},
		Target: reader.GetDocs,
	}
	return ch.Check(ctx, samples)
}

// saveVerification stores a mapping's result in the job's report document.
func (e *execution) saveVerification(ctx context.Context, r verify.MappingResult) {
	e.reportMu.Lock()
	defer e.reportMu.Unlock()
	e.report.Mappings[r.MappingID] = r
	raw, _ := json.Marshal(e.report)
	if err := e.r.d.Store.Jobs().PutReport(context.WithoutCancel(ctx), e.job.ID, raw); err != nil {
		e.log.Warn("store verification", "err", err)
	}
	msg := fmt.Sprintf("Verification %s", r.Result)
	if r.Sample != nil {
		msg += fmt.Sprintf(": %d/%d samples match", r.Sample.Matched, r.Sample.Checked)
	}
	if !r.Counts.Reconciled {
		msg += "; counts don't reconcile"
	}
	e.event(ctx, "verified", msg)
}
