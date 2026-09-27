package pipeline

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/jobs"
)

// maxDocsPerSecond limits the whole job: tables migrating in parallel share one budget.
func TestMaxDocsPerSecond_IsJobWideAcrossParallelTables(t *testing.T) {
	o := opts(100)
	rate := 1000
	o.MaxDocsPerSecond, o.TableConcurrency = &rate, 3
	f := newRunFixture(t, peopleRows(1, 600), 600, o)
	f.addMapping(t, "people_a")
	f.addMapping(t, "people_b")
	o.MappingIDs = f.job.MappingIDs
	f.job.Options, _ = json.Marshal(o)

	start := time.Now()
	out := f.run(jobs.Control{})
	elapsed := time.Since(start)
	require.True(t, out.Finished)
	require.Len(t, f.writeOpts, 3, "all three tables ran")

	// 1,800 rows at 1,000/s with a 1,000-row burst: the last 800 wait ≥ 0.8 s. Per-table buckets
	// (each with its own 1,000-row burst) would have let every table through at once.
	require.GreaterOrEqual(t, elapsed, 700*time.Millisecond, "the tables shared one rate limit")
	require.Less(t, elapsed, 5*time.Second)
}
