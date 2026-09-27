package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"omnidatabus/internal/pipeline/flowctl"
	"omnidatabus/internal/store"
)

// Error sink limits (OC-026).
const (
	FlushEvery     = 500
	FlushInterval  = time.Second
	MaxSampleBytes = 4 << 10
	DefaultMaxRows = 100_000
	masked         = "••••"
)

// ErrorStore persists dead-lettered rows (store.Jobs implements it).
type ErrorStore interface {
	AddErrors(ctx context.Context, errs []store.JobError, retention time.Duration) error
}

// TableInfo lets the sink name a row's columns and mask sensitive ones.
type TableInfo struct {
	Columns   []string
	Sensitive map[string]bool
}

// ErrorSink buffers row errors and stores them in batches (every 500 errors or 1 s), up to a
// per-job cap; past the cap only counters grow and the job is marked truncated (OC-026).
type ErrorSink struct {
	store     ErrorStore
	jobID     string
	ws        store.WorkspaceID
	retention time.Duration
	maxRows   int64
	clock     flowctl.Clock

	mu        sync.Mutex
	tables    map[string]TableInfo // mapping ID → columns
	buf       []store.JobError
	seq       int64
	stored    int64
	total     int64
	truncated bool
	flushes   int
	err       error
}

// NewErrorSink returns a sink; seq continues from startSeq (a resumed job keeps its numbering).
func NewErrorSink(st ErrorStore, jobID string, ws store.WorkspaceID, retention time.Duration, maxRows int64, startSeq int64, clock flowctl.Clock) *ErrorSink {
	if maxRows <= 0 {
		maxRows = DefaultMaxRows
	}
	if clock == nil {
		clock = flowctl.Real
	}
	return &ErrorSink{
		store: st, jobID: jobID, ws: ws, retention: retention, maxRows: maxRows, clock: clock, seq: startSeq, stored: startSeq,
		tables: map[string]TableInfo{},
	}
}

// Describe registers a mapping's columns and sensitive columns.
func (s *ErrorSink) Describe(mappingID string, t TableInfo) {
	s.mu.Lock()
	s.tables[mappingID] = t
	s.mu.Unlock()
}

// Add buffers errors, flushing when 500 are waiting.
func (s *ErrorSink) Add(ctx context.Context, errs []RowError) {
	if len(errs) == 0 {
		return
	}
	s.mu.Lock()
	for _, e := range errs {
		s.total++
		if s.stored >= s.maxRows {
			s.truncated = true
			continue
		}
		s.stored++
		s.seq++
		s.buf = append(s.buf, BuildRecord(s.jobID, s.ws, s.seq, e, s.tables[e.MappingID], s.clock.Now()))
	}
	s.mu.Unlock()
	s.flush(ctx, FlushEvery) // full chunks now; the rest on the next tick
}

// Flush stores everything buffered, in chunks of at most 500.
func (s *ErrorSink) Flush(ctx context.Context) { s.flush(ctx, 1) }

// flush stores chunks while at least minChunk errors are waiting.
func (s *ErrorSink) flush(ctx context.Context, minChunk int) {
	for {
		s.mu.Lock()
		if len(s.buf) == 0 || len(s.buf) < minChunk {
			s.mu.Unlock()
			return
		}
		n := min(len(s.buf), FlushEvery)
		chunk := s.buf[:n]
		s.buf = append([]store.JobError(nil), s.buf[n:]...)
		s.flushes++
		s.mu.Unlock()
		if err := s.store.AddErrors(ctx, chunk, s.retention); err != nil {
			s.mu.Lock()
			s.err = err
			s.mu.Unlock()
		}
	}
}

// Run flushes every second until ctx ends.
func (s *ErrorSink) Run(ctx context.Context) {
	for {
		if err := s.clock.Sleep(ctx, FlushInterval); err != nil {
			return
		}
		s.Flush(ctx)
	}
}

// Stats reports the counters: total errors, stored rows, whether storage was capped, flushes.
func (s *ErrorSink) Stats() (total, stored int64, truncated bool, flushes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total, s.stored, s.truncated, s.flushes
}

// Seq is the last stored error's sequence number (for the checkpoint).
func (s *ErrorSink) Seq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// BuildRecord turns a row error into its stored form: the source key as JSON and a sample of
// the row (≤ 4 KB) with sensitive columns masked (OC-026 AC1).
func BuildRecord(jobID string, ws store.WorkspaceID, seq int64, e RowError, t TableInfo, at time.Time) store.JobError {
	rec := store.JobError{
		JobID: jobID, WorkspaceID: ws, Seq: seq, MappingID: e.MappingID, Stage: e.Stage, Code: e.Code,
		Message: e.Message, FieldPath: e.FieldPath, At: at,
	}
	if e.SourceKey != nil {
		rec.SourceKey, _ = json.Marshal(jsonValues(e.SourceKey))
	}
	rec.Sample = sample(e.Row, t)
	return rec
}

// jsonValues makes driver values JSON-friendly ([16]byte uuids as text, bytes as hex, …).
func jsonValues(vs []any) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		switch x := v.(type) {
		case [16]byte:
			out[i] = fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
		case []byte:
			out[i] = fmt.Sprintf("\\x%x", x)
		case time.Time:
			out[i] = x.Format(time.RFC3339Nano)
		default:
			out[i] = v
		}
	}
	return out
}

func sample(row []any, t TableInfo) json.RawMessage {
	if row == nil {
		return nil
	}
	// A write error carries the document itself.
	if len(row) == 1 {
		if doc, ok := row[0].(json.RawMessage); ok {
			if len(doc) <= MaxSampleBytes {
				return doc
			}
			b, _ := json.Marshal(map[string]any{"_truncated": true, "document": string(doc[:MaxSampleBytes-64])})
			return b
		}
	}
	obj := map[string]any{}
	names := make([]string, 0, len(row))
	for i, v := range row {
		name := fmt.Sprintf("col%d", i)
		if i < len(t.Columns) {
			name = t.Columns[i]
		}
		names = append(names, name)
		switch {
		case t.Sensitive[name] && v != nil:
			obj[name] = masked
		default:
			obj[name] = jsonValues([]any{v})[0]
		}
	}
	b, err := json.Marshal(obj)
	if err == nil && len(b) <= MaxSampleBytes {
		return b
	}
	// Too big: shorten long values until it fits.
	for _, n := range names {
		if s, ok := obj[n].(string); ok && len(s) > 64 {
			obj[n] = s[:64] + "…"
		}
	}
	obj["_truncated"] = true
	b, _ = json.Marshal(obj)
	if len(b) > MaxSampleBytes {
		b, _ = json.Marshal(map[string]any{"_truncated": true})
	}
	return b
}

// Policy aborts a job that fails too much (OC-026).
type Policy struct {
	MaxErrors       int64
	MaxErrorRatio   float64
	MinRowsForRatio int64
}

// Exceeded reports whether the failures cross a threshold: more than MaxErrors (unless it's
// -1), or a ratio above MaxErrorRatio once MinRowsForRatio rows were processed.
func (p Policy) Exceeded(processed, failed int64) (bool, string) {
	if p.MaxErrors >= 0 && failed > p.MaxErrors {
		return true, fmt.Sprintf("%d rows failed (the limit is %d)", failed, p.MaxErrors)
	}
	if processed >= p.MinRowsForRatio && processed > 0 && p.MaxErrorRatio > 0 && float64(failed)/float64(processed) > p.MaxErrorRatio {
		return true, fmt.Sprintf("%.2f%% of rows failed (the limit is %.2f%%)", 100*float64(failed)/float64(processed), 100*p.MaxErrorRatio)
	}
	return false, ""
}
