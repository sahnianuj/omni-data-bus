package observability

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func docs(outcome string) float64 { return testutil.ToFloat64(JobDocs.WithLabelValues(outcome)) }

func hasSeries(v interface{ DeleteLabelValues(...string) bool }, jobID string) bool {
	// Deleting reports whether the series existed; put nothing back.
	return v.DeleteLabelValues(jobID)
}

func TestJobMetrics_AC2_CountersAndGaugeCleanup(t *testing.T) {
	w0, s0, f0 := docs("written"), docs("skipped"), docs("failed")

	// A resumed run starts at its checkpoint: those 100 documents were counted before.
	j := StartJob("job-ac2", 100, 0, 0)
	j.Observe(400, 5, 1, 950.5)
	require.InDelta(t, 950.5, testutil.ToFloat64(JobThroughput.WithLabelValues("job-ac2")), 0)
	j.SetConcurrency(64, 16)
	j.Observe(1_100, 10, 3, 1_200)
	j.End()
	j.Observe(5_000, 0, 0, 1) // after End: ignored

	require.InDelta(t, 1_000, docs("written")-w0, 0)
	require.InDelta(t, 10, docs("skipped")-s0, 0)
	require.InDelta(t, 3, docs("failed")-f0, 0)
	require.False(t, hasSeries(JobThroughput, "job-ac2"), "the per-job throughput gauge is deleted at the end")
	require.False(t, hasSeries(WriterEffectiveConcurrency, "job-ac2"))
	require.False(t, hasSeries(WriterConfiguredConcurrency, "job-ac2"))
}

func TestJobMetrics_RestartedTableMovesBaselineDown(t *testing.T) {
	w0 := docs("written")
	j := StartJob("job-restart", 0, 0, 0)
	j.Observe(500, 0, 0, 1)
	j.Observe(0, 0, 0, 1) // a keyless table restarted from the beginning
	j.Observe(200, 0, 0, 1)
	j.End()
	require.InDelta(t, 700, docs("written")-w0, 0, "rewritten documents are processed again")
}

func TestRefreshJobGauge_SetsEveryActiveStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		RefreshJobGauge(ctx, func(_ context.Context, statuses []string, _ int) (map[string]int, error) {
			require.Equal(t, ActiveStatuses, statuses)
			calls++
			cancel()
			return map[string]int{"running": 2, "queued": 1}, nil
		}, time.Hour, slog.New(slog.DiscardHandler))
	}()
	<-done
	require.Equal(t, 1, calls)
	require.InDelta(t, 2, testutil.ToFloat64(Jobs.WithLabelValues("running")), 0)
	require.InDelta(t, 1, testutil.ToFloat64(Jobs.WithLabelValues("queued")), 0)
	require.InDelta(t, 0, testutil.ToFloat64(Jobs.WithLabelValues("paused")), 0, "statuses with no jobs report 0")
}
