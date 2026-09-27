package pipeline

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/jobs"
	"omnidatabus/internal/observability"
)

// A run feeds the job metrics (OC-031 AC2): outcome counters grow by what it processed, and the
// per-job gauges are gone once it ends.
func TestRun_FeedsJobMetricsAndCleansUp(t *testing.T) {
	docs := func(o string) float64 { return testutil.ToFloat64(observability.JobDocs.WithLabelValues(o)) }
	w0, f0 := docs("written"), docs("failed")
	rows0 := testutil.ToFloat64(observability.ReaderRows)
	ops0 := testutil.ToFloat64(observability.WriterOps.WithLabelValues("written", ""))

	f := newRunFixture(t, peopleRows(1, 2500, 7, 1500), 2500, opts(100))
	require.True(t, f.run(jobs.Control{}).Finished)

	require.InDelta(t, 2498, docs("written")-w0, 0)
	require.InDelta(t, 2, docs("failed")-f0, 0)
	require.InDelta(t, 2500, testutil.ToFloat64(observability.ReaderRows)-rows0, 0)
	require.InDelta(t, 2498, testutil.ToFloat64(observability.WriterOps.WithLabelValues("written", ""))-ops0, 0)
	require.Zero(t, testutil.ToFloat64(observability.WriterInflight))
	require.False(t, observability.JobThroughput.DeleteLabelValues(f.job.ID), "no throughput series is left for a finished job")
	require.False(t, observability.WriterEffectiveConcurrency.DeleteLabelValues(f.job.ID))
}
