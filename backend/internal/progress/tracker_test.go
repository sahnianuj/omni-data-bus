package progress

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

func newClock() *clock { return &clock{now: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)} }

func tracker(c *clock, totals ...int64) *Tracker {
	var ms []MappingProgress
	for i, n := range totals {
		ms = append(ms, MappingProgress{MappingID: fmt.Sprintf("m%d", i), Table: fmt.Sprintf("t%d", i), Total: n, TotalKind: TotalExact})
	}
	return NewTracker("j1", ms, c.Now)
}

func TestSnapshot_AC1_Percent(t *testing.T) {
	c := newClock()
	tr := tracker(c, 2000)
	tr.Acked("m0", 900, 50, 50, 1024)
	s := tr.Snapshot("running")
	require.EqualValues(t, 1000, s.Processed)
	require.Equal(t, 50.0, s.Percent)
	require.Equal(t, 50.0, s.Mappings[0].Percent)
	require.EqualValues(t, 1024, s.BytesWritten)
	require.Equal(t, TotalExact, s.TotalKind)
}

func TestSnapshot_AC1_CapBeforeFinal(t *testing.T) {
	c := newClock()
	tr := tracker(c, 1000)
	tr.Acked("m0", 1000, 0, 0, 0)
	require.Equal(t, 99.9, tr.Snapshot("running").Percent, "not 100 until final")
	tr.Finish()
	require.Equal(t, 100.0, tr.Snapshot("running").Percent)
}

func TestSnapshot_AC1_TotalRaisedWhenExceeded(t *testing.T) {
	c := newClock()
	tr := NewTracker("j1", []MappingProgress{{MappingID: "m0", Total: 1000, TotalKind: TotalEstimate}}, c.Now)
	tr.Acked("m0", 1500, 0, 0, 0)
	s := tr.Snapshot("running")
	require.EqualValues(t, 1500, s.Total, "a stale estimate is raised to processed")
	require.Equal(t, TotalEstimate, s.TotalKind)
	require.Equal(t, 99.9, s.Percent)

	// The exact count replaces the estimate.
	tr.SetTotal("m0", 3000, true)
	s = tr.Snapshot("running")
	require.EqualValues(t, 3000, s.Total)
	require.Equal(t, TotalExact, s.TotalKind)
	require.Equal(t, 50.0, s.Percent)
}

func TestTracker_AC1_ConcurrentIncrementsRace(t *testing.T) {
	c := newClock()
	tr := tracker(c, 1_000_000)
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				tr.Read("m0", 1)
				tr.Acked("m0", 1, 0, 0, 10)
				if i%100 == 0 {
					_ = tr.Snapshot("running")
				}
			}
		}()
	}
	wg.Wait()
	s := tr.Snapshot("running")
	require.EqualValues(t, 64_000, s.Written)
	require.EqualValues(t, 64_000, s.RowsRead)
	require.EqualValues(t, 640_000, s.BytesWritten)
}

func TestSnapshot_AC4_PayloadBounded(t *testing.T) {
	c := newClock()
	tr := tracker(c, 1_000_000, 2_000_000, 3_000_000, 4_000_000)
	for i := 0; i < 30; i++ {
		for n := 0; n <= i; n++ {
			tr.Error(ErrorSample{At: c.Now(), MappingID: "m1", Stage: "transform", Code: fmt.Sprintf("CODE_NUMBER_%02d", i), Message: "a fairly long message about what went wrong in this row"})
		}
	}
	s := tr.Snapshot("running")
	require.Len(t, s.ErrorsByCode, 5, "top five codes only")
	require.Equal(t, "CODE_NUMBER_29", s.ErrorsByCode[0].Code)
	require.LessOrEqual(t, len(s.ErrorSamples), 5)
	raw, err := json.Marshal(s)
	require.NoError(t, err)
	require.Less(t, len(raw), 6<<10, "%d bytes", len(raw))
	require.Empty(t, tr.Snapshot("running").ErrorSamples, "samples are sent once")
}

func TestTracker_ResumeContinuesCounts(t *testing.T) {
	c := newClock()
	tr := tracker(c, 5000)
	tr.Resume(Snapshot{Written: 999, ElapsedSeconds: 30, ErrorsByCode: []CodeCount{{Code: "PARSE_FAILED", Count: 2}}})
	tr.ResumeMapping("m0", 1000, 0, 2, false)
	c.add(10 * time.Second)
	tr.Acked("m0", 500, 0, 0, 0)
	s := tr.Snapshot("running")
	require.EqualValues(t, 1500, s.Written, "the checkpoint's counts win over the old snapshot")
	require.EqualValues(t, 2, s.Failed)
	require.EqualValues(t, 40, s.ElapsedSeconds)
	require.Equal(t, []CodeCount{{Code: "PARSE_FAILED", Count: 2}}, s.ErrorsByCode)
	tr.ResetMapping("m0")
	require.Zero(t, tr.Snapshot("running").Written)
}
