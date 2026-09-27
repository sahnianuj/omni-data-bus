// Package report writes a benchmark run's results (JSON plus a markdown summary) and reads
// earlier runs back for regression detection.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"omnidatabus/bench/internal/analyze"
)

// Result is one benchmark run.
type Result struct {
	Dataset     string    `json:"dataset"`
	Description string    `json:"description"`
	Rows        int64     `json:"rows"`
	ChildRows   int64     `json:"childRows,omitempty"`
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`
	Seconds     float64   `json:"durationSeconds"`
	// ServerVersion and ServerCommit identify the build under test.
	ServerVersion string            `json:"serverVersion"`
	ServerCommit  string            `json:"serverCommit"`
	JobID         string            `json:"jobId"`
	JobStatus     string            `json:"jobStatus"`
	Options       map[string]any    `json:"jobOptions"`
	Environment   map[string]string `json:"environment,omitempty"`
	// AverageDocsPerSec is written / duration (including ramp-up), for reference.
	AverageDocsPerSec float64          `json:"averageDocsPerSec"`
	Measured          analyze.Measured `json:"measured"`
	Gates             *analyze.Gates   `json:"gates,omitempty"`
	GateFailures      []string         `json:"gateFailures,omitempty"`
	Regression        *Regression      `json:"regression,omitempty"`
	Profiles          []string         `json:"profiles,omitempty"`
	Samples           []analyze.Sample `json:"samples"`
}

// Regression is the comparison with earlier runs of the same dataset.
type Regression struct {
	Runs      int     `json:"runs"`
	Window    int     `json:"window"`
	Tolerance float64 `json:"tolerance"`
	Baseline  float64 `json:"baselineDocsPerSec"`
	Regressed bool    `json:"regressed"`
}

// Passed reports whether every evaluated check passed.
func (r Result) Passed() bool {
	return len(r.GateFailures) == 0 && (r.Regression == nil || !r.Regression.Regressed)
}

// Name is the file stem: <date>-<commit>-<dataset>.
func (r Result) Name() string {
	commit := r.ServerCommit
	if commit == "" || commit == "none" {
		commit = "dev"
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return fmt.Sprintf("%s-%s-%s", r.StartedAt.UTC().Format("2006-01-02T150405Z"), commit, r.Dataset)
}

// Write stores <dir>/<name>.json and <dir>/<name>.md and returns their paths.
func Write(dir string, r Result) (jsonPath, mdPath string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", "", err
	}
	jsonPath = filepath.Join(dir, r.Name()+".json")
	mdPath = filepath.Join(dir, r.Name()+".md")
	if err := os.WriteFile(jsonPath, append(b, '\n'), 0o644); err != nil {
		return "", "", err
	}
	return jsonPath, mdPath, os.WriteFile(mdPath, []byte(Markdown(r)), 0o644)
}

// Markdown is the human summary of a run.
func Markdown(r Result) string {
	var b strings.Builder
	verdict := "✅ PASS"
	if !r.Passed() {
		verdict = "❌ FAIL"
	}
	if r.Gates == nil && r.Regression == nil {
		verdict = "ℹ️ measured (no gates evaluated)"
	}
	fmt.Fprintf(&b, "# Benchmark — %s — %s\n\n", r.Dataset, verdict)
	fmt.Fprintf(&b, "%s\n\n", r.Description)
	fmt.Fprintf(&b, "| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| Build | %s (%s) |\n", r.ServerVersion, r.ServerCommit)
	fmt.Fprintf(&b, "| Started | %s |\n", r.StartedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "| Rows | %d", r.Rows)
	if r.ChildRows > 0 {
		fmt.Fprintf(&b, " (+ %d embedded)", r.ChildRows)
	}
	fmt.Fprintf(&b, " |\n| Job | %s — %s in %.0f s |\n", r.JobID, r.JobStatus, r.Seconds)
	for _, k := range sortedKeys(r.Environment) {
		fmt.Fprintf(&b, "| %s | %s |\n", k, r.Environment[k])
	}
	m := r.Measured
	fmt.Fprintf(&b, "\n## Results\n\n| Metric | Value | Gate |\n|---|---|---|\n")
	gate := func(on bool, s string) string {
		if !on || r.Gates == nil {
			return "—"
		}
		return s
	}
	g := analyze.Gates{}
	if r.Gates != nil {
		g = *r.Gates
	}
	fmt.Fprintf(&b, "| Sustained throughput (median, 10–90%%) | %.0f docs/s | %s |\n", m.Throughput, gate(g.MinThroughput > 0, fmt.Sprintf("≥ %.0f", g.MinThroughput)))
	fmt.Fprintf(&b, "| Average throughput | %.0f docs/s | — |\n", r.AverageDocsPerSec)
	rss := "not reported"
	if m.PeakRSSBytes > 0 {
		rss = analyze.MiB(m.PeakRSSBytes)
	}
	fmt.Fprintf(&b, "| Worker RSS (peak) | %s | %s |\n", rss, gate(g.MaxRSSBytes > 0, "< "+analyze.MiB(g.MaxRSSBytes)))
	fmt.Fprintf(&b, "| Progress latency p95 | %s (%d events) | %s |\n", m.LatencyP95.Round(time.Millisecond), m.LatencyCount, gate(g.MaxLatencyP95 > 0, "≤ "+g.MaxLatencyP95.String()))
	fmt.Fprintf(&b, "| Documents written / source rows | %d / %d | %s |\n", m.Written, m.SourceRows, gate(g.RequireNoLoss, "equal"))
	fmt.Fprintf(&b, "| Verification | %s | %s |\n", orDash(m.Verified), gate(g.RequireVerified, "passed"))
	if r.Regression != nil {
		reg := r.Regression
		state := "ok"
		if reg.Regressed {
			state = "REGRESSED"
		}
		fmt.Fprintf(&b, "| Regression vs median of last %d runs (%d available) | baseline %.0f docs/s: %s | within %.0f%% |\n",
			reg.Window, reg.Runs, reg.Baseline, state, reg.Tolerance*100)
	}
	if len(r.GateFailures) > 0 {
		b.WriteString("\n## Failed gates\n\n")
		for _, f := range r.GateFailures {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	if len(r.Profiles) > 0 {
		b.WriteString("\n## Profiles (captured at 50% progress)\n\n")
		for _, p := range r.Profiles {
			fmt.Fprintf(&b, "- `%s` (`go tool pprof -http=: %s`)\n", filepath.Base(p), filepath.Base(p))
		}
	}
	return b.String()
}

// History returns the sustained throughput of earlier completed runs of dataset in dir,
// oldest first.
func History(dir, dataset string) ([]float64, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*-"+dataset+".json"))
	if err != nil {
		return nil, err
	}
	var runs []Result
	for _, p := range paths {
		b, err := os.ReadFile(p) //nolint:gosec // the operator's results directory
		if err != nil {
			return nil, err
		}
		var r Result
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if r.Dataset == dataset && r.JobStatus == "completed" && r.Measured.Throughput > 0 {
			runs = append(runs, r)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt.Before(runs[j].StartedAt) })
	out := make([]float64, len(runs))
	for i, r := range runs {
		out[i] = r.Measured.Throughput
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
