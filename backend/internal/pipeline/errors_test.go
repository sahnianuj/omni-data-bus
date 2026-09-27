package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/pipeline/flowctl"
	"omnidatabus/internal/store"
)

type fakeErrStore struct {
	mu      sync.Mutex
	batches [][]store.JobError
}

func (f *fakeErrStore) AddErrors(_ context.Context, errs []store.JobError, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, append([]store.JobError(nil), errs...))
	return nil
}

func (f *fakeErrStore) sizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for _, b := range f.batches {
		out = append(out, len(b))
	}
	return out
}

func TestErrorSink_AC1_BuildsRecord(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	rec := BuildRecord("j1", "ws", 5, RowError{
		MappingID: "m1", Stage: StageTransform, Code: "PARSE_FAILED", Message: "can't parse", FieldPath: "age",
		SourceKey: []any{int32(42)}, Row: []any{int32(42), "Ada", "x"},
	}, TableInfo{Columns: []string{"id", "name", "age"}}, at)
	require.Equal(t, store.JobError{
		JobID: "j1", WorkspaceID: "ws", Seq: 5, MappingID: "m1", Stage: "transform", Code: "PARSE_FAILED",
		Message: "can't parse", FieldPath: "age", SourceKey: json.RawMessage(`[42]`), Sample: rec.Sample, At: at,
	}, rec)
	require.JSONEq(t, `{"id":42,"name":"Ada","age":"x"}`, string(rec.Sample))
}

func TestErrorSink_AC1_SampleTruncatedAndMasked(t *testing.T) {
	big := strings.Repeat("y", 10_000)
	rec := BuildRecord("j1", "ws", 1, RowError{Row: []any{int64(7), "4111111111111111", big}},
		TableInfo{Columns: []string{"id", "card", "notes"}, Sensitive: map[string]bool{"card": true}}, time.Now())
	require.LessOrEqual(t, len(rec.Sample), MaxSampleBytes)
	var s map[string]any
	require.NoError(t, json.Unmarshal(rec.Sample, &s))
	require.Equal(t, masked, s["card"])
	require.Equal(t, true, s["_truncated"])
	require.EqualValues(t, 7, s["id"])
	require.NotContains(t, string(rec.Sample), "4111")

	// A write failure keeps the document itself as the sample.
	rec = BuildRecord("j1", "ws", 2, RowError{Stage: StageWrite, Row: []any{json.RawMessage(`{"a":1}`)}}, TableInfo{}, time.Now())
	require.JSONEq(t, `{"a":1}`, string(rec.Sample))
}

func errs(n int) []RowError {
	out := make([]RowError, n)
	for i := range out {
		out[i] = RowError{MappingID: "m", Stage: StageTransform, Code: "PARSE_FAILED", SourceKey: []any{int64(i)}}
	}
	return out
}

func TestErrorSink_AC2_FlushBySizeAndTime(t *testing.T) {
	fs := &fakeErrStore{}
	clock := flowctl.NewFakeClock()
	sink := NewErrorSink(fs, "j1", "ws", time.Hour, 0, 0, clock)
	sink.Add(context.Background(), errs(1200))
	require.Equal(t, []int{500, 500}, fs.sizes(), "flushed at 500 as they arrive")
	sink.Flush(context.Background()) // the 1 s tick
	require.Equal(t, []int{500, 500, 200}, fs.sizes())

	// Run flushes on the timer.
	ctx, cancel := context.WithCancel(context.Background())
	sink.Add(ctx, errs(3))
	done := make(chan struct{})
	go func() { sink.Run(ctx); close(done) }()
	require.Eventually(t, func() bool { return len(fs.sizes()) == 4 }, time.Second, time.Millisecond)
	cancel()
	<-done
	require.Equal(t, 3, fs.sizes()[3])
	require.EqualValues(t, 1203, sink.Seq(), "sequence numbers continue")
}

func TestErrorSink_AC2_StorageCap(t *testing.T) {
	fs := &fakeErrStore{}
	sink := NewErrorSink(fs, "j1", "ws", time.Hour, 1000, 0, flowctl.NewFakeClock())
	sink.Add(context.Background(), errs(1200))
	sink.Flush(context.Background())
	total, stored, truncated, _ := sink.Stats()
	require.EqualValues(t, 1200, total)
	require.EqualValues(t, 1000, stored)
	require.True(t, truncated)
	n := 0
	for _, s := range fs.sizes() {
		n += s
	}
	require.Equal(t, 1000, n)
}

func TestErrorPolicy_AC3_Absolute(t *testing.T) {
	p := Policy{MaxErrors: 1000, MaxErrorRatio: 0.5, MinRowsForRatio: 10_000_000}
	over, _ := p.Exceeded(5000, 1000)
	require.False(t, over)
	over, why := p.Exceeded(5001, 1001)
	require.True(t, over, "the 1,001st failure aborts")
	require.Contains(t, why, "1001 rows failed")
}

func TestErrorPolicy_AC3_RatioWithMinRows(t *testing.T) {
	p := Policy{MaxErrors: -1, MaxErrorRatio: 0.01, MinRowsForRatio: 10_000}
	over, _ := p.Exceeded(10_000, 150)
	require.True(t, over, "1.5% of 10k")
	over, _ = p.Exceeded(1_000, 50)
	require.False(t, over, "5% but below minRows")
	over, _ = p.Exceeded(10_000, 100)
	require.False(t, over, "exactly 1% is allowed")
}

func TestErrorPolicy_AC3_DisabledAbsolute(t *testing.T) {
	p := Policy{MaxErrors: -1, MaxErrorRatio: 0, MinRowsForRatio: 0}
	over, _ := p.Exceeded(1_000_000, 999_999)
	require.False(t, over)
}
