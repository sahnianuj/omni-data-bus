package analyze

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

// run builds 1 s samples from cumulative written counts over a job of total rows.
func run(total int64, written ...int64) []Sample {
	out := make([]Sample, len(written))
	for i, w := range written {
		out[i] = Sample{At: t0.Add(time.Duration(i) * time.Second), Written: w, Percent: 100 * float64(w) / float64(total)}
	}
	return out
}

func TestSustainedThroughput_AC9_WindowMedian(t *testing.T) {
	// 1000 rows. Ramp-up (0→50→100) and the tail (900→1000) are outside 10–90%; inside, the
	// intervals are 100, 300, 100, 200, 100 docs/s → median 100.
	s := run(1000, 0, 50, 100, 200, 500, 600, 800, 900, 1000)
	got, err := SustainedThroughput(s)
	if err != nil || got != 100 {
		t.Fatalf("got %v, %v; want 100", got, err)
	}

	// Order doesn't matter, and a duplicate timestamp (dt 0) is ignored.
	shuffled := []Sample{s[4], s[1], s[8], s[0], s[6], s[3], s[5], s[2], s[7], s[7]}
	if got2, _ := SustainedThroughput(shuffled); got2 != got {
		t.Fatalf("shuffled %v != %v", got2, got)
	}

	// Even count: median of two middle values. Intervals 200, 400 → 300.
	if got, _ := SustainedThroughput(run(1000, 100, 300, 700)); got != 300 {
		t.Fatalf("even median = %v", got)
	}

	// A job that jumps straight past the window has nothing sustained to measure.
	if _, err := SustainedThroughput(run(1000, 0, 950, 1000)); err != ErrTooFewSamples {
		t.Fatalf("err = %v", err)
	}
}

func TestPercentileAndPeakRSS_AC9(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i)*10*time.Millisecond)
	}
	if p := Percentile(ds, 95); p != 950*time.Millisecond {
		t.Fatalf("p95 = %v", p)
	}
	if p := Percentile([]time.Duration{3 * time.Second}, 95); p != 3*time.Second {
		t.Fatalf("single p95 = %v", p)
	}
	if Percentile(nil, 95) != 0 || Median(nil) != 0 {
		t.Fatal("empty inputs are 0")
	}
	s := []Sample{{RSSBytes: 100}, {RSSBytes: 300}, {}, {RSSBytes: 200}}
	if PeakRSS(s) != 300 {
		t.Fatalf("peak = %v", PeakRSS(s))
	}
}

func TestGates_AC9_PassFail(t *testing.T) {
	good := Measured{Throughput: 24_000, PeakRSSBytes: 300 << 20, LatencyP95: 1200 * time.Millisecond, LatencyCount: 40,
		SourceRows: 5_000_000, Written: 5_000_000, Verified: "passed"}
	if why := Evaluate(good, ReleaseGates()); len(why) != 0 {
		t.Fatalf("expected pass, got %v", why)
	}
	warn := good
	warn.Verified = "passed_with_warnings"
	if why := Evaluate(warn, ReleaseGates()); len(why) != 0 {
		t.Fatalf("warnings still pass: %v", why)
	}

	bad := Measured{Throughput: 12_345, PeakRSSBytes: 600 << 20, LatencyP95: 3 * time.Second, LatencyCount: 10,
		SourceRows: 1000, Written: 999, Verified: "failed"}
	want := []string{
		"sustained throughput 12345 docs/s is below 20000",
		"worker RSS peaked at 600 MiB, the limit is 512 MiB",
		"progress latency p95 3s is above 2s",
		"data loss: 1000 source rows, 999 documents written",
		"verification failed",
	}
	assertReasons(t, Evaluate(bad, ReleaseGates()), want)

	// Missing measurements fail the gates that need them rather than passing silently.
	assertReasons(t, Evaluate(Measured{Throughput: 30_000}, ReleaseGates()), []string{
		"worker RSS wasn't reported (process_resident_memory_bytes is Linux-only)",
		"no progress events were received",
		"verification missing",
	})

	// A zero gate is off.
	if why := Evaluate(bad, Gates{}); len(why) != 0 {
		t.Fatalf("no gates, no reasons: %v", why)
	}
}

func assertReasons(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("reasons:\n got %q\nwant %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reason %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}

func TestRegression_AC9_RelativeToRollingMedian(t *testing.T) {
	// The last 7 of these are used: median of 100,104,98,102,99,101,100 = 100.
	history := []float64{10, 10, 100, 104, 98, 102, 99, 101, 100}
	if r, base := Regression(81, history, 7, 0.2); r || base != 100 {
		t.Fatalf("81 is within 20%% of 100: regressed=%v base=%v", r, base)
	}
	if r, _ := Regression(79, history, 7, 0.2); !r {
		t.Fatal("79 is more than 20% below 100")
	}
	// Old slow runs drag an unbounded median down; the window compares with recent runs only.
	mixed := []float64{50, 50, 50, 50, 50, 50, 50, 50, 100, 100, 100, 100, 100, 100, 100}
	if r, base := Regression(79, mixed, 0, 0.2); r || base != 50 {
		t.Fatalf("all 15 runs: median 50, 79 is fine (regressed=%v base=%v)", r, base)
	}
	if r, base := Regression(79, mixed, 7, 0.2); !r || base != 100 {
		t.Fatalf("last 7 runs: median 100, 79 regressed (regressed=%v base=%v)", r, base)
	}
	if r, base := Regression(1, nil, 7, 0.2); r || base != 0 {
		t.Fatal("no history: nothing to regress against")
	}
}
