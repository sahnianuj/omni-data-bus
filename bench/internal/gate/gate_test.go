package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omnidatabus/bench/internal/analyze"
	"omnidatabus/bench/internal/report"
)

func f(v float64) *float64 { return &v }

func write(t *testing.T, dir string, r report.Result) {
	t.Helper()
	b, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(dir, r.Name()+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(scenario string, at time.Time, docsPerSec, rssMiB float64) report.Result {
	return report.Result{Dataset: strings.Split(scenario, "-")[0], Scenario: scenario, StartedAt: at, JobStatus: "completed",
		Measured: analyze.Measured{Throughput: docsPerSec, PeakRSSBytes: rssMiB * (1 << 20), Written: 10, SourceRows: 10, Verified: "passed"}}
}

func TestGate_AC4_JudgesLatestRunOfEachScenario(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	write(t, dir, run("directory", t0, 9_000, 300))                 // older, would fail
	write(t, dir, run("directory", t0.Add(time.Hour), 16_000, 300)) // latest
	write(t, dir, run("skewed-sorted-merge", t0, 30_000, 700))
	write(t, dir, run("skewed-batched", t0, 10_000, 400))
	cold := report.Result{Dataset: "coldstart", StartedAt: t0, ColdStartSeconds: 1.2}
	write(t, dir, cold)
	base := t.TempDir()
	write(t, base, report.Result{Dataset: "standard", ServerVersion: "1.0.0", StartedAt: t0, JobStatus: "completed", Measured: analyze.Measured{Throughput: 100_000}})
	write(t, dir, run("standard", t0, 96_000, 200))
	baseline, _ := filepath.Rel(dir, filepath.Join(base, report.Result{Dataset: "standard", StartedAt: t0}.Name()+".json"))

	file := File{Release: "2.0.0", Gates: []Gate{
		{ID: "G1", Scenario: "directory", Metric: "throughput", Min: f(15_000)},
		{ID: "G2", Scenario: "skewed-sorted-merge", Metric: "ratio", Of: "skewed-batched", Min: f(1.5)},
		{ID: "G3", Scenario: "skewed-sorted-merge", Metric: "peakRssMB", Max: f(768)},
		{ID: "G4", Scenario: "coldstart", Metric: "coldStartSeconds", Max: f(5)},
		{ID: "G5", Scenario: "standard", Metric: "regression", Baseline: baseline, Tolerance: 0.05},
		{ID: "G6", Scenario: "nested", Metric: "throughput", Min: f(8_000)},
		{ID: "G7", Scenario: "directory", Metric: "noLoss", Min: f(1)},
	}}
	results, err := Latest(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Outcome{}
	for _, o := range Evaluate(file, results, dir) {
		got[o.Gate.ID] = o
	}
	want := map[string]string{"G1": Pass, "G2": Pass, "G3": Pass, "G4": Pass, "G5": Pass, "G6": Missing, "G7": Pass}
	for id, s := range want {
		if got[id].Status != s {
			t.Errorf("%s = %s (%v, %s), want %s", id, got[id].Status, got[id].Value, got[id].Detail, s)
		}
	}
	if got["G2"].Value != 3 || got["G5"].Value != 0.96 {
		t.Fatalf("ratio %v, regression %v", got["G2"].Value, got["G5"].Value)
	}
	outcomes := Evaluate(file, results, dir)
	if Passed(outcomes, false) || !Passed(outcomes, true) {
		t.Fatal("a missing result fails unless allowed")
	}
	md := Markdown(file, outcomes, true)
	if !strings.Contains(md, "| G6  | nested | — (no result for nested) | ≥ 8000 docs/s | ⏳ pending | — |") || !strings.Contains(md, "✅ PASS") {
		t.Fatal(md)
	}
}

func TestGate_AC4_FailsBelowBounds(t *testing.T) {
	results := map[string]report.Result{
		"nested":   run("nested", time.Now(), 7_000, 800),
		"standard": run("standard", time.Now(), 90_000, 200),
	}
	failed := report.Result{Dataset: "directory", JobStatus: "failed"}
	results["directory"] = failed
	file := File{Gates: []Gate{
		{ID: "a", Scenario: "nested", Metric: "throughput", Min: f(8_000)},
		{ID: "b", Scenario: "nested", Metric: "peakRssMB", Max: f(768)},
		{ID: "c", Scenario: "directory", Metric: "throughput", Min: f(1)},
	}}
	for _, o := range Evaluate(file, results, "") {
		if o.Status != Fail {
			t.Errorf("%s = %s", o.Gate.ID, o.Status)
		}
	}
}

func TestLoad_RejectsBadGates(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"metric":   `{"gates":[{"id":"x","scenario":"s","metric":"speed","min":1}]}`,
		"bound":    `{"gates":[{"id":"x","scenario":"s","metric":"throughput"}]}`,
		"ratio":    `{"gates":[{"id":"x","scenario":"s","metric":"ratio","min":1}]}`,
		"baseline": `{"gates":[{"id":"x","scenario":"s","metric":"regression"}]}`,
	} {
		p := filepath.Join(dir, name+".json")
		_ = os.WriteFile(p, []byte(body), 0o644)
		if _, err := Load(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestGateFile_2_0_0IsValid(t *testing.T) {
	file, err := Load("../../gates-2.0.0.json")
	if err != nil {
		t.Fatal(err)
	}
	if file.Release != "2.0.0" || len(file.Gates) < 6 {
		t.Fatalf("%+v", file)
	}
}

func TestReference_OC056_DropsLocalRuns(t *testing.T) {
	local := run("nested", time.Now(), 9_000, 300)
	ref := run("directory", time.Now(), 16_000, 300)
	ref.Environment = map[string]string{"reference": "true"}
	got := Reference(map[string]report.Result{"nested": local, "directory": ref})
	if len(got) != 1 || got["directory"].Measured.Throughput != 16_000 {
		t.Fatalf("%v", got)
	}
}

// OC-173: a quality gate bounds a named score of a correctness run.
func TestEvaluate_OC173_Quality(t *testing.T) {
	min, zero := 0.9, 0.0
	f := File{Release: "2.5.0", Gates: []Gate{
		{ID: "G10c", Scenario: "propose-quality", Metric: "quality", Key: "fields", Min: &min},
		{ID: "G10e", Scenario: "propose-quality", Metric: "quality", Key: "wrongHigh", Max: &zero},
		{ID: "G10x", Scenario: "propose-quality", Metric: "quality", Key: "nope", Min: &min},
	}}
	for _, g := range f.Gates {
		if err := g.check(); err != nil {
			t.Fatal(err)
		}
	}
	res := map[string]report.Result{"propose-quality": {Scenario: "propose-quality", JobStatus: "completed", Quality: map[string]float64{"fields": 0.97, "wrongHigh": 1}}}
	out := Evaluate(f, res, ".")
	if out[0].Status != Pass || out[1].Status != Fail || out[2].Status != Missing {
		t.Fatalf("got %+v", out)
	}
	res["propose-quality"].Quality["wrongHigh"] = 0
	if out := Evaluate(f, res, "."); out[1].Status != Pass {
		t.Fatalf("max 0 is inclusive: got %+v", out[1])
	}
	if err := (Gate{ID: "x", Scenario: "s", Metric: "quality", Min: &min}).check(); err == nil {
		t.Fatal("a quality gate needs a key")
	}
}
