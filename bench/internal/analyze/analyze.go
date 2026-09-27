// Package analyze turns the benchmark's 1 s samples into the release-gate numbers (OC-034):
// sustained throughput, peak worker memory and progress latency, and judges them against the
// gates and against earlier runs.
package analyze

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"
)

// Sample is one 1 s observation of a running job.
type Sample struct {
	At      time.Time `json:"at"`
	Percent float64   `json:"percent"`
	Written int64     `json:"written"`
	// RSSBytes is the worker's resident memory (process_resident_memory_bytes); 0 = unknown.
	RSSBytes float64 `json:"rssBytes,omitempty"`
}

// Window is the progress range whose samples count as "sustained": ramp-up and the tail are left out.
const (
	WindowLow  = 10.0
	WindowHigh = 90.0
)

// ErrTooFewSamples means the window held fewer than two intervals (the job was too short).
var ErrTooFewSamples = errors.New("too few samples between 10% and 90% progress")

// SustainedThroughput is the median docs/s of the 1 s intervals whose both ends lie between 10%
// and 90% progress. Rates come from the written counts, not the job's own smoothed rate.
func SustainedThroughput(samples []Sample) (float64, error) {
	s := slices.Clone(samples)
	sort.SliceStable(s, func(i, j int) bool { return s[i].At.Before(s[j].At) })
	var rates []float64
	for i := 1; i < len(s); i++ {
		a, b := s[i-1], s[i]
		if a.Percent < WindowLow || b.Percent > WindowHigh {
			continue
		}
		dt := b.At.Sub(a.At).Seconds()
		if dt <= 0 {
			continue
		}
		rates = append(rates, float64(b.Written-a.Written)/dt)
	}
	if len(rates) < 2 {
		return 0, ErrTooFewSamples
	}
	return Median(rates), nil
}

// Median of xs (0 for none); xs isn't modified.
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// Percentile is the nearest-rank p-th percentile (0 < p ≤ 100) of ds; 0 for none.
func Percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	rank := int(math.Ceil(p / 100 * float64(len(s))))
	return s[min(max(rank, 1), len(s))-1]
}

// PeakRSS is the largest resident memory seen (0 when the worker didn't report it).
func PeakRSS(samples []Sample) float64 {
	var peak float64
	for _, s := range samples {
		peak = max(peak, s.RSSBytes)
	}
	return peak
}

// Measured is what one run produced.
type Measured struct {
	Throughput   float64       `json:"sustainedDocsPerSec"`
	PeakRSSBytes float64       `json:"peakRssBytes"`
	LatencyP95   time.Duration `json:"progressLatencyP95Ns"`
	LatencyCount int           `json:"progressEvents"`
	SourceRows   int64         `json:"sourceRows"`
	Written      int64         `json:"written"`
	Verified     string        `json:"verification"` // passed, passed_with_warnings, failed, skipped or ""
}

// Gates are the release thresholds; a zero value turns that gate off.
type Gates struct {
	MinThroughput   float64       `json:"minDocsPerSec"`
	MaxRSSBytes     float64       `json:"maxRssBytes"`
	MaxLatencyP95   time.Duration `json:"maxProgressLatencyP95Ns"`
	RequireNoLoss   bool          `json:"requireNoLoss"`
	RequireVerified bool          `json:"requireVerified"`
}

// ReleaseGates are architecture §9's: ≥ 20,000 docs/s sustained on the standard dataset, worker
// RSS < 512 MB, progress latency ≤ 2 s, and zero data loss (counts match, sample verify passes).
func ReleaseGates() Gates {
	return Gates{MinThroughput: 20_000, MaxRSSBytes: 512 << 20, MaxLatencyP95: 2 * time.Second, RequireNoLoss: true, RequireVerified: true}
}

// Evaluate returns why m fails g; none means every gate passed.
func Evaluate(m Measured, g Gates) []string {
	var why []string
	if g.MinThroughput > 0 && m.Throughput < g.MinThroughput {
		why = append(why, fmt.Sprintf("sustained throughput %.0f docs/s is below %.0f", m.Throughput, g.MinThroughput))
	}
	if g.MaxRSSBytes > 0 {
		switch {
		case m.PeakRSSBytes == 0:
			why = append(why, "worker RSS wasn't reported (process_resident_memory_bytes is Linux-only)")
		case m.PeakRSSBytes >= g.MaxRSSBytes:
			why = append(why, fmt.Sprintf("worker RSS peaked at %s, the limit is %s", MiB(m.PeakRSSBytes), MiB(g.MaxRSSBytes)))
		}
	}
	if g.MaxLatencyP95 > 0 {
		switch {
		case m.LatencyCount == 0:
			why = append(why, "no progress events were received")
		case m.LatencyP95 > g.MaxLatencyP95:
			why = append(why, fmt.Sprintf("progress latency p95 %s is above %s", m.LatencyP95.Round(time.Millisecond), g.MaxLatencyP95))
		}
	}
	if g.RequireNoLoss && m.Written != m.SourceRows {
		why = append(why, fmt.Sprintf("data loss: %d source rows, %d documents written", m.SourceRows, m.Written))
	}
	if g.RequireVerified && m.Verified != "passed" && m.Verified != "passed_with_warnings" {
		v := m.Verified
		if v == "" {
			v = "missing"
		}
		why = append(why, "verification "+v)
	}
	return why
}

// Regression compares a run with the rolling median of the last window earlier runs (oldest
// first): it regresses when it is more than tolerance (e.g. 0.2) below that median. With no
// history there is nothing to compare, so it passes.
func Regression(current float64, history []float64, window int, tolerance float64) (regressed bool, baseline float64) {
	if window > 0 && len(history) > window {
		history = history[len(history)-window:]
	}
	if len(history) == 0 {
		return false, 0
	}
	baseline = Median(history)
	return current < baseline*(1-tolerance), baseline
}

// MiB formats bytes as mebibytes.
func MiB(b float64) string { return fmt.Sprintf("%.0f MiB", b/(1<<20)) }
