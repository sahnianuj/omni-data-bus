package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"omnidatabus/bench/internal/analyze"
	"omnidatabus/bench/internal/gate"
	"omnidatabus/bench/internal/report"
)

// gateCmd judges committed results against a release's gate file (OC-056 AC4, CI job
// bench-gate): exit 0 when every gate passed, 1 when one failed (or has no result, unless
// --allow-missing), 2 on a usage or read error.
func gateCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("odbbench gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	gates := fs.String("gates", "gates-2.0.0.json", "the release's gate file")
	results := fs.String("results", "results/2.0.0", "directory of results JSON (the latest run of each scenario counts)")
	allowMissing := fs.Bool("allow-missing", false, "pass gates without a result (pending) instead of failing them")
	summary := fs.String("summary", "", "also write the markdown summary to this file (e.g. $GITHUB_STEP_SUMMARY, appended)")
	referenceOnly := fs.Bool("reference-only", false, "judge only runs in the reference environment (--env reference=true); others count as missing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	file, err := gate.Load(*gates)
	if err != nil {
		fmt.Fprintln(stderr, "odbbench gate:", err)
		return 2
	}
	latest, err := gate.Latest(*results)
	if err != nil {
		fmt.Fprintln(stderr, "odbbench gate:", err)
		return 2
	}
	if *referenceOnly {
		latest = gate.Reference(latest)
	}
	outcomes := gate.Evaluate(file, latest, filepath.Dir(*gates))
	md := gate.Markdown(file, outcomes, *allowMissing)
	_, _ = io.WriteString(stdout, md)
	if *summary != "" {
		f, err := os.OpenFile(*summary, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // the operator's path
		if err != nil {
			fmt.Fprintln(stderr, "odbbench gate:", err)
			return 2
		}
		_, _ = io.WriteString(f, md)
		_ = f.Close()
	}
	if !gate.Passed(outcomes, *allowMissing) {
		return 1
	}
	return 0
}

// coldStartCmd times `migrate.sh preflight` of an exported bundle (OC-056, runner cold start):
// one untimed run first (the binary is downloaded or the image pulled), then --runs timed runs;
// the median is recorded as the coldstart scenario. The bundle's passphrase comes from the
// environment (ODB_BUNDLE_PASSPHRASE), as for any unattended run.
func coldStartCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("odbbench coldstart", flag.ContinueOnError)
	fs.SetOutput(stderr)
	script := fs.String("script", "", "the exported migrate.sh (required)")
	runs := fs.Int("runs", 5, "timed runs")
	out := fs.String("out", "results/2.0.0", "results directory")
	var env multiFlag
	fs.Var(&env, "env", "environment note for the report, key=value (repeatable)")
	if err := fs.Parse(args); err != nil || *script == "" || *runs < 1 {
		fmt.Fprintln(stderr, "usage: odbbench coldstart --script <migrate.sh> [--runs 5] [--out dir]")
		return 2
	}
	ctx := context.Background()
	preflight := func() (time.Duration, error) {
		cmd := exec.CommandContext(ctx, *script, "preflight") //nolint:gosec // the operator's script
		cmd.Dir = filepath.Dir(*script)
		cmd.Stdout, cmd.Stderr = io.Discard, stderr
		start := time.Now()
		err := cmd.Run()
		return time.Since(start), err
	}
	if _, err := preflight(); err != nil {
		fmt.Fprintln(stderr, "odbbench coldstart: warm-up preflight:", err)
		return 2
	}
	res := report.Result{Dataset: "coldstart", Scenario: "coldstart", Description: "migrate.sh preflight with the binary present",
		StartedAt: time.Now().UTC(), JobStatus: "completed",
		Environment: map[string]string{"harness": fmt.Sprintf("%s/%s, %d CPU", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())}}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			res.Environment[k] = v
		}
	}
	times := make([]float64, 0, *runs)
	for i := 0; i < *runs; i++ {
		d, err := preflight()
		if err != nil {
			fmt.Fprintln(stderr, "odbbench coldstart: preflight:", err)
			return 1
		}
		times = append(times, d.Seconds())
	}
	slices.Sort(times)
	res.FinishedAt = time.Now().UTC()
	res.Seconds = res.FinishedAt.Sub(res.StartedAt).Seconds()
	res.ColdStartSeconds = analyze.Median(times)
	jp, _, err := report.Write(*out, res)
	if err != nil {
		fmt.Fprintln(stderr, "odbbench coldstart:", err)
		return 2
	}
	fmt.Fprintf(stdout, "preflight median %.2f s over %d runs (min %.2f, max %.2f); wrote %s\n", res.ColdStartSeconds, *runs, times[0], times[len(times)-1], jp)
	return 0
}
