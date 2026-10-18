// Package gate checks committed benchmark results against a release's gate file (OC-056): it
// reads every results JSON in a directory, takes the latest run of each scenario, and judges
// each gate: a metric's bound, a ratio between two scenarios, or a regression against an
// earlier release's result.
package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"omnidatabus/bench/internal/report"
)

// File is a release's gates (bench/gates-<release>.json).
type File struct {
	Release string `json:"release"`
	Gates   []Gate `json:"gates"`
}

// Gate is one release gate.
type Gate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Scenario is the results' scenario the gate reads (a dataset, or dataset-strategy).
	Scenario string `json:"scenario"`
	// Metric is throughput (docs/s), peakRssMB, coldStartSeconds, noLoss (1 when written = rows
	// and verification passed), ratio (throughput of Scenario / throughput of Of) or regression
	// (throughput relative to Baseline's).
	Metric string   `json:"metric"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
	Of     string   `json:"of,omitempty"`
	// Baseline is a results JSON of an earlier release, relative to the gate file; Tolerance the
	// fraction the throughput may fall below it.
	Baseline  string  `json:"baseline,omitempty"`
	Tolerance float64 `json:"tolerance,omitempty"`
	// Key names the quality score a quality gate reads (OC-173).
	Key string `json:"key,omitempty"`
}

// Status of a judged gate.
const (
	Pass    = "pass"
	Fail    = "fail"
	Missing = "missing" // no result for the scenario (or the baseline)
)

// Outcome is one judged gate.
type Outcome struct {
	Gate   Gate
	Status string
	Value  float64
	Detail string
	// Result is the run the gate read (empty when Missing).
	Result string
}

// Load reads a gate file.
func Load(path string) (File, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator's gate file
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	for _, g := range f.Gates {
		if err := g.check(); err != nil {
			return File{}, fmt.Errorf("%s: gate %s: %w", path, g.ID, err)
		}
	}
	return f, nil
}

func (g Gate) check() error {
	switch {
	case g.ID == "" || g.Scenario == "":
		return errors.New("id and scenario are required")
	case g.Metric == "ratio" && g.Of == "":
		return errors.New("a ratio needs of")
	case g.Metric == "regression" && g.Baseline == "":
		return errors.New("a regression needs baseline")
	case g.Metric != "regression" && g.Min == nil && g.Max == nil:
		return errors.New("min or max is required")
	case g.Metric == "quality" && g.Key == "":
		return errors.New("a quality gate needs key")
	}
	switch g.Metric {
	case "throughput", "peakRssMB", "coldStartSeconds", "noLoss", "ratio", "regression", "quality":
		return nil
	}
	return fmt.Errorf("unknown metric %q", g.Metric)
}

// Latest reads every *.json result in dir and returns the latest run of each scenario.
func Latest(dir string) (map[string]report.Result, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := map[string]report.Result{}
	for _, p := range paths {
		r, err := read(p)
		if err != nil {
			return nil, err
		}
		s := r.ScenarioName()
		if cur, ok := out[s]; !ok || r.StartedAt.After(cur.StartedAt) {
			out[s] = r
		}
	}
	return out, nil
}

// Reference keeps the runs made in the reference environment (environment reference=true):
// the release gates are judged on those only; local runs are indicative.
func Reference(results map[string]report.Result) map[string]report.Result {
	out := map[string]report.Result{}
	for s, r := range results {
		if r.Environment["reference"] == "true" {
			out[s] = r
		}
	}
	return out
}

func read(path string) (report.Result, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator's results directory
	if err != nil {
		return report.Result{}, err
	}
	var r report.Result
	if err := json.Unmarshal(b, &r); err != nil {
		return report.Result{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// Evaluate judges every gate of f against results; base is the gate file's directory (for
// baselines).
func Evaluate(f File, results map[string]report.Result, base string) []Outcome {
	out := make([]Outcome, 0, len(f.Gates))
	for _, g := range f.Gates {
		out = append(out, evaluate(g, results, base))
	}
	return out
}

func evaluate(g Gate, results map[string]report.Result, base string) Outcome {
	o := Outcome{Gate: g}
	r, ok := results[g.Scenario]
	if !ok {
		o.Status, o.Detail = Missing, "no result for "+g.Scenario
		return o
	}
	o.Result = r.Name()
	if r.JobStatus != "" && r.JobStatus != "completed" {
		o.Status, o.Detail = Fail, "the run ended "+r.JobStatus
		return o
	}
	switch g.Metric {
	case "throughput":
		o.Value = r.Measured.Throughput
	case "peakRssMB":
		if r.Measured.PeakRSSBytes == 0 {
			o.Status, o.Detail = Missing, "worker RSS wasn't reported (Linux only)"
			return o
		}
		o.Value = r.Measured.PeakRSSBytes / (1 << 20)
	case "coldStartSeconds":
		o.Value = r.ColdStartSeconds
	case "quality":
		v, ok := r.Quality[g.Key]
		if !ok {
			o.Status, o.Detail = Missing, "no "+g.Key+" in "+g.Scenario
			return o
		}
		o.Value = v
	case "noLoss":
		m := r.Measured
		if m.Written == m.SourceRows && (m.Verified == "passed" || m.Verified == "passed_with_warnings") {
			o.Value = 1
		}
		o.Detail = fmt.Sprintf("%d / %d written, verification %s", m.Written, m.SourceRows, m.Verified)
	case "ratio":
		of, ok := results[g.Of]
		if !ok || of.Measured.Throughput == 0 {
			o.Status, o.Detail = Missing, "no result for "+g.Of
			return o
		}
		o.Value = r.Measured.Throughput / of.Measured.Throughput
		o.Detail = fmt.Sprintf("%.0f vs %.0f docs/s", r.Measured.Throughput, of.Measured.Throughput)
	case "regression":
		b, err := read(filepath.Join(base, g.Baseline))
		if err != nil || b.Measured.Throughput == 0 {
			o.Status, o.Detail = Missing, "no baseline "+g.Baseline
			return o
		}
		o.Value = r.Measured.Throughput / b.Measured.Throughput
		o.Detail = fmt.Sprintf("%.0f vs %.0f docs/s in %s", r.Measured.Throughput, b.Measured.Throughput, b.ServerVersion)
		if o.Value < 1-g.Tolerance {
			o.Status = Fail
			return o
		}
		o.Status = Pass
		return o
	}
	o.Status = Pass
	// A quality maximum is inclusive (max 0: none); the other metrics' are exclusive.
	over := g.Max != nil && (o.Value > *g.Max || (g.Metric != "quality" && o.Value >= *g.Max))
	if (g.Min != nil && o.Value < *g.Min) || over {
		o.Status = Fail
	}
	return o
}

// Passed reports whether every gate passed; with allowMissing, gates without a result are
// tolerated (reported as pending).
func Passed(outcomes []Outcome, allowMissing bool) bool {
	for _, o := range outcomes {
		if o.Status == Fail || (o.Status == Missing && !allowMissing) {
			return false
		}
	}
	return true
}

// Markdown is the gate summary.
func Markdown(f File, outcomes []Outcome, allowMissing bool) string {
	var b strings.Builder
	verdict := "✅ PASS"
	if !Passed(outcomes, allowMissing) {
		verdict = "❌ FAIL"
	}
	fmt.Fprintf(&b, "# Release gates %s — %s\n\n| Gate | Scenario | Value | Target | Result | Run |\n|---|---|---|---|---|---|\n", f.Release, verdict)
	sorted := append([]Outcome(nil), outcomes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Gate.ID < sorted[j].Gate.ID })
	for _, o := range sorted {
		status := map[string]string{Pass: "✅ pass", Fail: "❌ fail", Missing: "⏳ pending"}[o.Status]
		if o.Status == Missing && !allowMissing {
			status = "❌ missing"
		}
		value := "—"
		if o.Status != Missing {
			value = o.Gate.format(o.Value)
		}
		if o.Detail != "" {
			value += " (" + o.Detail + ")"
		}
		run := o.Result
		if run == "" {
			run = "—"
		}
		fmt.Fprintf(&b, "| %s %s | %s | %s | %s | %s | %s |\n", o.Gate.ID, o.Gate.Name, o.Gate.Scenario, value, o.Gate.target(), status, run)
	}
	return b.String()
}

func (g Gate) format(v float64) string {
	switch g.Metric {
	case "throughput":
		return fmt.Sprintf("%.0f docs/s", v)
	case "peakRssMB":
		return fmt.Sprintf("%.0f MiB", v)
	case "coldStartSeconds":
		return fmt.Sprintf("%.2f s", v)
	case "noLoss":
		if v == 1 {
			return "yes"
		}
		return "no"
	case "quality":
		if g.Max != nil && *g.Max == 0 {
			return fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprintf("%.0f %%", 100*v)
	}
	return fmt.Sprintf("%.2f×", v)
}

func (g Gate) target() string {
	switch {
	case g.Metric == "regression":
		return fmt.Sprintf("≥ %.0f%% of baseline", (1-g.Tolerance)*100)
	case g.Metric == "noLoss":
		return "yes"
	case g.Min != nil:
		return "≥ " + g.format(*g.Min)
	}
	if g.Metric == "quality" {
		return "≤ " + g.format(*g.Max)
	}
	return "< " + g.format(*g.Max)
}
