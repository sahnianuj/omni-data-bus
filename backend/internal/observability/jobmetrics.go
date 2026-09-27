package observability

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// JobMetrics feeds one run of a job into the job metrics: the docs counters grow by what the
// run processed, and the per-job gauges exist only while it runs (OC-031 AC2).
type JobMetrics struct {
	jobID string

	mu                       sync.Mutex
	written, skipped, failed int64
	ended                    bool
}

// StartJob begins a run whose totals are already at the given counts (a resumed run starts
// from its checkpoint; those documents were counted by the earlier run).
func StartJob(jobID string, written, skipped, failed int64) *JobMetrics {
	return &JobMetrics{jobID: jobID, written: written, skipped: skipped, failed: failed}
}

// Observe records the run's current totals and throughput. Totals only move forward; a
// smaller total (a keyless table restarted from the beginning) moves the baseline down.
func (j *JobMetrics) Observe(written, skipped, failed int64, throughput float64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.ended {
		return
	}
	add := func(outcome string, now int64, last *int64) {
		if d := now - *last; d > 0 {
			JobDocs.WithLabelValues(outcome).Add(float64(d))
		}
		*last = now
	}
	add("written", written, &j.written)
	add("skipped", skipped, &j.skipped)
	add("failed", failed, &j.failed)
	JobThroughput.WithLabelValues(j.jobID).Set(throughput)
}

// SetConcurrency records the writer's configured and effective KV concurrency.
func (j *JobMetrics) SetConcurrency(configured, effective int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.ended {
		return
	}
	WriterConfiguredConcurrency.WithLabelValues(j.jobID).Set(float64(configured))
	WriterEffectiveConcurrency.WithLabelValues(j.jobID).Set(float64(effective))
}

// End deletes the job's gauges so finished jobs don't leave series behind.
func (j *JobMetrics) End() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.ended = true
	JobThroughput.DeleteLabelValues(j.jobID)
	WriterConfiguredConcurrency.DeleteLabelValues(j.jobID)
	WriterEffectiveConcurrency.DeleteLabelValues(j.jobID)
}

// JobLister lists jobs by status (store.Jobs().ListByStatus).
type JobLister func(ctx context.Context, statuses []string, limit int) (count map[string]int, err error)

// ActiveStatuses are the statuses the oc_jobs gauge reports; finished jobs only grow in number,
// so they're counted by oc_job_docs_total and the job table instead.
var ActiveStatuses = []string{"queued", "running", "pausing", "paused", "cancelling"}

// RefreshJobGauge sets oc_jobs from the metadata store now and then every interval until ctx
// ends. Every node reports the same cluster-wide numbers; dashboards take max().
func RefreshJobGauge(ctx context.Context, list JobLister, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		counts, err := list(ctx, ActiveStatuses, 10_000)
		if err == nil {
			for _, s := range ActiveStatuses {
				Jobs.WithLabelValues(s).Set(float64(counts[s]))
			}
		} else if ctx.Err() == nil {
			log.Debug("refresh job gauge", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
