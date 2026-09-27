package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omnidatabus/bench/internal/analyze"
)

func result(started time.Time, dataset string, docsPerSec float64) Result {
	return Result{
		Dataset: dataset, Description: "test", Rows: 1000, StartedAt: started, ServerVersion: "1.0.0", ServerCommit: "abcdef1234567890",
		JobID: "j1", JobStatus: "completed", Seconds: 10, AverageDocsPerSec: 100,
		Measured:    analyze.Measured{Throughput: docsPerSec, Written: 1000, SourceRows: 1000, Verified: "passed", LatencyCount: 3, LatencyP95: time.Second},
		Environment: map[string]string{"worker": "8 vCPU"},
	}
}

func TestWriteAndHistory(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	for i, v := range []float64{120, 100, 110} {
		if _, _, err := Write(dir, result(t0.Add(time.Duration(i)*time.Hour), "smoke", v)); err != nil {
			t.Fatal(err)
		}
	}
	failed := result(t0.Add(5*time.Hour), "smoke", 5)
	failed.JobStatus = "failed"
	jp, mp, err := Write(dir, failed)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(jp) != "2026-09-27T150000Z-abcdef123456-smoke.json" || !strings.HasSuffix(mp, ".md") {
		t.Fatalf("paths %s %s", jp, mp)
	}
	_, _, _ = Write(dir, result(t0, "standard", 999))
	h, err := History(dir, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 3 || h[0] != 120 || h[2] != 110 {
		t.Fatalf("history = %v (oldest first, completed runs of this dataset only)", h)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad-smoke.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := History(dir, "smoke"); err == nil {
		t.Fatal("a corrupt result is an error")
	}
}

func TestMarkdown_GatesAndVerdict(t *testing.T) {
	r := result(time.Now(), "standard", 23_000)
	g := analyze.ReleaseGates()
	r.Gates = &g
	r.Measured.PeakRSSBytes = 300 << 20
	md := Markdown(r)
	for _, want := range []string{"# Benchmark — standard — ✅ PASS", "| 23000 docs/s | ≥ 20000 |", "| Worker RSS (peak) | 300 MiB | < 512 MiB |", "| worker | 8 vCPU |"} {
		if !strings.Contains(md, want) {
			t.Fatalf("missing %q in\n%s", want, md)
		}
	}
	r.GateFailures = []string{"sustained throughput 100 docs/s is below 20000"}
	r.Regression = &Regression{Runs: 9, Window: 7, Tolerance: 0.2, Baseline: 30_000, Regressed: true}
	r.Profiles = []string{"/tmp/x-cpu.pprof"}
	md = Markdown(r)
	for _, want := range []string{"❌ FAIL", "## Failed gates", "baseline 30000 docs/s: REGRESSED", "`x-cpu.pprof`"} {
		if !strings.Contains(md, want) {
			t.Fatalf("missing %q in\n%s", want, md)
		}
	}
	plain := result(time.Now(), "wide", 1)
	plain.ChildRows = 10
	plain.ServerCommit = "none"
	plain.Measured.Verified = ""
	if md := Markdown(plain); !strings.Contains(md, "no gates evaluated") || !strings.Contains(md, "(+ 10 embedded)") || !strings.Contains(md, "| not reported | — |") {
		t.Fatalf("plain:\n%s", md)
	}
	if !strings.Contains(plain.Name(), "-dev-wide") {
		t.Fatal(plain.Name())
	}
}
