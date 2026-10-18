// Command odbbench runs the Omni-Data-Bus performance benchmark (OC-034): it generates a
// dataset in the source PostgreSQL, sets up a project through the API, runs the migration,
// samples progress and worker memory every second, measures progress latency over SSE, and
// writes results/<date>-<commit>-<dataset>.{json,md}. With --gate it exits 1 when a release gate
// fails; with --regression-window it exits 1 when throughput regressed against earlier runs.
//
//	cd bench && go run ./cmd/odbbench --dataset smoke --gate integrity
//	cd bench && go run ./cmd/odbbench --dataset standard --gate release   # release checklist (AC10)
//
// 2.0 (OC-056) adds the directory, nested and skewed datasets, `coldstart` (times `odb preflight`
// in an export folder; 2.5, OC-184) and `gate`, which judges the committed results against
// gates-<release>.json:
//
//	cd bench && go run ./cmd/odbbench --dataset directory --directory /data/odb-out/bench --gate integrity --out results/2.0.0
//	cd bench && go run ./cmd/odbbench coldstart --dir ./export --odb ./export/bin/odb --out results/2.5.0
//	cd bench && go run ./cmd/odbbench gate --gates gates-2.5.0.json --results results/2.5.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"omnidatabus/bench/internal/analyze"
	"omnidatabus/bench/internal/api"
	"omnidatabus/bench/internal/dataset"
	"omnidatabus/bench/internal/promtext"
	"omnidatabus/bench/internal/report"
)

type options struct {
	api, metrics, pprof, psql       string
	datasetName, generate, gate     string
	rows                            int64
	email, password                 string
	src                             api.Source
	tgt                             api.Target
	jobOptions                      string
	out                             string
	regressionWindow                int
	regressionTolerance             float64
	minDocsPerSec, maxRSSMB         float64
	maxLatency, timeout, profileFor time.Duration
	env                             multiFlag
	strategy                        string
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "gate":
			os.Exit(gateCmd(os.Args[2:], os.Stdout, os.Stderr))
		case "coldstart":
			os.Exit(coldStartCmd(os.Args[2:], os.Stdout, os.Stderr))
		}
	}
	o := parseFlags(os.Args[1:])
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	res, err := run(ctx, o, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "odbbench:", err)
		os.Exit(2)
	}
	if !res.Passed() {
		os.Exit(1)
	}
}

func parseFlags(args []string) options {
	var o options
	fs := flag.NewFlagSet("odbbench", flag.ExitOnError)
	fs.StringVar(&o.api, "api", "http://localhost:18081", "Omni-Data-Bus base URL (its OC_PUBLIC_URL)")
	fs.StringVar(&o.metrics, "metrics", "http://localhost:19092/metrics", "the worker's Prometheus /metrics URL (for RSS); empty = skip")
	fs.StringVar(&o.pprof, "pprof", "", "the worker's pprof base URL (OC_PPROF_ADDR), e.g. http://localhost:16060; captures CPU and heap profiles at 50%")
	fs.StringVar(&o.psql, "psql", "docker exec -i omni-connect-dev-source-pg-1 psql -U shop_owner -d shop", "command that runs psql against the source (SQL on stdin)")
	fs.StringVar(&o.datasetName, "dataset", "smoke", "narrow | standard | wide | embed | smoke; 2.0: directory | nested | skewed")
	fs.Int64Var(&o.rows, "rows", 0, "override the dataset's row count (0 = the dataset's own)")
	fs.StringVar(&o.generate, "generate", "auto", "auto (when the table is missing or has another row count) | always | never")
	fs.StringVar(&o.gate, "gate", "none", "release (every gate) | integrity (no loss + verification) | none")
	fs.Float64Var(&o.minDocsPerSec, "min-docs-per-sec", 20_000, "release gate: sustained throughput")
	fs.Float64Var(&o.maxRSSMB, "max-rss-mb", 512, "release gate: worker RSS")
	fs.DurationVar(&o.maxLatency, "max-latency", 2*time.Second, "release gate: progress latency p95")
	fs.IntVar(&o.regressionWindow, "regression-window", 0, "compare with the median of the last N runs in --out (0 = off)")
	fs.Float64Var(&o.regressionTolerance, "regression-tolerance", 0.2, "fail when throughput is this fraction below that median")
	fs.StringVar(&o.email, "email", "bench@example.com", "account to sign in with (signed up if missing)")
	fs.StringVar(&o.password, "password", "bench-Correct-Horse-Battery-9", "its password")
	fs.StringVar(&o.src.Host, "source-host", "source-pg", "PostgreSQL host as the app reaches it")
	fs.IntVar(&o.src.Port, "source-port", 5432, "PostgreSQL port as the app reaches it")
	fs.StringVar(&o.src.Database, "source-db", "shop", "source database")
	fs.StringVar(&o.src.Username, "source-user", "shop_owner", "source user")
	fs.StringVar(&o.src.Password, "source-password", "shop_owner", "source password")
	fs.StringVar(&o.tgt.ConnectionString, "target", "couchbase://couchbase", "Couchbase connection string as the app reaches it")
	fs.StringVar(&o.tgt.Username, "target-user", "Administrator", "Couchbase user")
	fs.StringVar(&o.tgt.Password, "target-password", "password", "Couchbase password")
	fs.StringVar(&o.tgt.Bucket, "bucket", "sandbox", "target bucket (a bench_<time> scope is created in it)")
	fs.BoolVar(&o.tgt.Capella, "capella", false, "the target is Couchbase Capella")
	fs.StringVar(&o.tgt.Directory, "directory", "", "write to a directory target at this path, as the worker sees it (the directory dataset; a bench_<time> folder is added)")
	fs.StringVar(&o.tgt.Durability, "durability", "file", "the directory target's durability: none | file | file+dir")
	fs.StringVar(&o.strategy, "strategy", "", "force the embed's strategy: batched | sorted-merge (the skewed dataset; empty = auto)")
	fs.StringVar(&o.jobOptions, "job-options", `{}`, "extra job options as JSON, e.g. {\"batchSize\":2000,\"writeConcurrency\":128}")
	fs.StringVar(&o.out, "out", "results", "results directory")
	fs.DurationVar(&o.timeout, "timeout", 3*time.Hour, "give up after this long")
	fs.DurationVar(&o.profileFor, "profile-seconds", 30*time.Second, "CPU profile length")
	fs.Var(&o.env, "env", "environment note for the report, key=value (repeatable), e.g. --env worker='8 vCPU / 16 GB'")
	_ = fs.Parse(args)
	return o
}

func logf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, "%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func run(ctx context.Context, o options, log io.Writer) (report.Result, error) {
	d, err := dataset.Get(o.datasetName, o.rows)
	if err != nil {
		return report.Result{}, err
	}
	if d.Release != "" && o.gate == "release" {
		return report.Result{}, fmt.Errorf("%s is a %s scenario: run it with --gate integrity, then judge it with `odbbench gate`", d.Name, d.Release)
	}
	if d.Directory != (o.tgt.Directory != "") {
		return report.Result{}, fmt.Errorf("--directory is for (and required by) the directory dataset")
	}
	if o.strategy != "" && d.Embed == nil {
		return report.Result{}, fmt.Errorf("--strategy needs a dataset with an embed")
	}
	gates, err := gatesFor(o)
	if err != nil {
		return report.Result{}, err
	}
	var jobOpts map[string]any
	if err := json.Unmarshal([]byte(o.jobOptions), &jobOpts); err != nil {
		return report.Result{}, fmt.Errorf("--job-options: %w", err)
	}
	if err := prepare(ctx, o, d, log); err != nil {
		return report.Result{}, err
	}

	c := api.New(o.api)
	if err := c.SignIn(ctx, o.email, o.password); err != nil {
		return report.Result{}, fmt.Errorf("sign in: %w", err)
	}
	ver, err := c.Version(ctx)
	if err != nil {
		return report.Result{}, err
	}
	stamp := time.Now().UTC()
	tgt := o.tgt
	if tgt.Directory != "" {
		tgt.Directory = strings.TrimRight(tgt.Directory, "/") + fmt.Sprintf("/bench_%d", stamp.Unix())
	}
	logf(log, "server %s (%s); setting up project", ver.Version, ver.Commit)
	projectID, err := c.Project(ctx, fmt.Sprintf("bench %s %s", d.Name, stamp.Format(time.RFC3339)), o.src, tgt)
	if err != nil {
		return report.Result{}, fmt.Errorf("project: %w", err)
	}
	scope := fmt.Sprintf("bench_%d", stamp.Unix())
	mappingID, err := c.Mapping(ctx, projectID, dataset.Schema, d.Table, tgt.Location(scope, d.Table))
	if err != nil {
		return report.Result{}, err
	}
	if d.Embed != nil {
		if err := c.AddEmbed(ctx, projectID, mappingID, d.EmbedRequest(o.strategy)); err != nil {
			return report.Result{}, fmt.Errorf("embed: %w", err)
		}
	}

	res := report.Result{
		Dataset: d.Name, Scenario: scenario(d, o.strategy), Target: "couchbase", Description: d.Description, Rows: d.Rows, ChildRows: d.ChildRows(),
		ServerVersion: ver.Version, ServerCommit: ver.Commit, Options: jobOpts, Environment: environment(o),
	}
	if d.Directory {
		res.Target = "directory"
	}
	job, err := c.StartJob(ctx, projectID, jobOpts)
	if err != nil {
		return res, fmt.Errorf("start job: %w", err)
	}
	res.JobID, res.StartedAt = job.ID, time.Now().UTC()
	if tgt.Directory != "" {
		logf(log, "job %s started → %s/%s", job.ID, tgt.Directory, d.Table)
	} else {
		logf(log, "job %s started → %s.%s.%s", job.ID, tgt.Bucket, scope, d.Table)
	}

	// Progress latency, over the job's SSE stream (the UI's view).
	var latencies []time.Duration
	var latErr error
	latDone := make(chan struct{})
	latCtx, stopLat := context.WithCancel(ctx)
	defer stopLat()
	go func() {
		defer close(latDone)
		latencies, latErr = c.Latency(latCtx, job.ID, time.Now)
	}()

	var profiles []string
	var profMu sync.Mutex
	var profWG sync.WaitGroup
	profiled := false
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastLog := time.Time{}
	for !job.Terminal() {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-tick.C:
		}
		if job, err = c.Job(ctx, job.ID); err != nil {
			return res, err
		}
		s := analyze.Sample{At: time.Now().UTC(), Percent: job.Progress.Percent, Written: job.Progress.Written}
		s.RSSBytes = rss(ctx, o.metrics)
		res.Samples = append(res.Samples, s)
		if time.Since(lastLog) >= 10*time.Second || job.Terminal() {
			logf(log, "%-9s %5.1f%%  %d written  %.0f docs/s  rss %s", job.Status, job.Progress.Percent, job.Progress.Written, job.Progress.Throughput, analyze.MiB(s.RSSBytes))
			lastLog = time.Now()
		}
		if o.pprof != "" && !profiled && job.Progress.Percent >= 50 {
			profiled = true
			profWG.Add(1)
			go func() {
				defer profWG.Done()
				got := captureProfiles(ctx, o, res, log)
				profMu.Lock()
				profiles = append(profiles, got...)
				profMu.Unlock()
			}()
		}
	}
	res.FinishedAt = time.Now().UTC()
	res.JobStatus = job.Status
	res.Seconds = res.FinishedAt.Sub(res.StartedAt).Seconds()
	res.AverageDocsPerSec = float64(job.Progress.Written) / max(res.Seconds, 1)

	select {
	case <-latDone:
	case <-time.After(10 * time.Second):
		stopLat()
		<-latDone
	}
	if latErr != nil {
		logf(log, "progress stream: %v", latErr)
	}
	profWG.Wait()
	res.Profiles = profiles

	m := analyze.Measured{PeakRSSBytes: analyze.PeakRSS(res.Samples), LatencyCount: len(latencies),
		LatencyP95: analyze.Percentile(latencies, 95), SourceRows: d.Rows, Written: job.Progress.Written}
	if m.Throughput, err = analyze.SustainedThroughput(res.Samples); err != nil {
		logf(log, "sustained throughput: %v (use a larger dataset)", err)
	}
	if rep, err := c.Report(ctx, job.ID); err != nil {
		logf(log, "report: %v", err)
	} else {
		m.Verified = verification(rep)
	}
	res.Measured = m

	if gates != nil {
		res.Gates = gates
		res.GateFailures = analyze.Evaluate(m, *gates)
		if job.Status != "completed" {
			res.GateFailures = append(res.GateFailures, "job "+job.Status+" "+job.Reason)
		}
	}
	if o.regressionWindow > 0 {
		hist, err := report.History(o.out, d.Name)
		if err != nil {
			return res, err
		}
		regressed, base := analyze.Regression(m.Throughput, hist, o.regressionWindow, o.regressionTolerance)
		res.Regression = &report.Regression{Runs: len(hist), Window: o.regressionWindow, Tolerance: o.regressionTolerance, Baseline: base, Regressed: regressed}
	}
	jp, mp, err := report.Write(o.out, res)
	if err != nil {
		return res, err
	}
	logf(log, "sustained %.0f docs/s · avg %.0f docs/s · rss %s · latency p95 %s · %s", m.Throughput, res.AverageDocsPerSec,
		analyze.MiB(m.PeakRSSBytes), m.LatencyP95.Round(time.Millisecond), m.Verified)
	for _, f := range res.GateFailures {
		logf(log, "GATE FAILED: %s", f)
	}
	if res.Regression != nil && res.Regression.Regressed {
		logf(log, "REGRESSION: %.0f docs/s vs baseline %.0f", m.Throughput, res.Regression.Baseline)
	}
	logf(log, "wrote %s and %s", jp, mp)
	return res, nil
}

// scenario is the run's name in gate files: the dataset, plus the forced strategy.
func scenario(d dataset.Dataset, strategy string) string {
	if strategy == "" {
		return ""
	}
	return d.Name + "-" + strategy
}

func gatesFor(o options) (*analyze.Gates, error) {
	switch o.gate {
	case "none", "":
		return nil, nil
	case "integrity":
		return &analyze.Gates{RequireNoLoss: true, RequireVerified: true}, nil
	case "release":
		g := analyze.ReleaseGates()
		g.MinThroughput, g.MaxRSSBytes, g.MaxLatencyP95 = o.minDocsPerSec, o.maxRSSMB*(1<<20), o.maxLatency
		return &g, nil
	}
	return nil, fmt.Errorf("--gate must be release, integrity or none, not %q", o.gate)
}

// verification is the worst result across the report's mappings.
func verification(r api.Report) string {
	rank := map[string]int{"passed": 0, "passed_with_warnings": 1, "skipped": 2, "": 3, "failed": 4}
	worst := ""
	if len(r.Mappings) > 0 {
		worst = "passed"
	}
	for _, m := range r.Mappings {
		v := ""
		if m.Verification != nil {
			v = m.Verification.Result
		}
		if rank[v] > rank[worst] {
			worst = v
		}
	}
	return worst
}

func environment(o options) map[string]string {
	env := map[string]string{"harness": fmt.Sprintf("%s/%s, %d CPU", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())}
	for _, kv := range o.env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}

// rss reads the worker's resident memory; 0 when unavailable (non-Linux, or no URL).
func rss(ctx context.Context, url string) float64 {
	if url == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = res.Body.Close() }()
	v, _, _ := promtext.Value(res.Body, "process_resident_memory_bytes")
	return v
}

func captureProfiles(ctx context.Context, o options, r report.Result, log io.Writer) []string {
	stem := filepath.Join(o.out, fmt.Sprintf("%s-%s", r.StartedAt.Format("2006-01-02T150405Z"), r.Dataset))
	var out []string
	for _, p := range []struct{ path, file string }{
		{"/debug/pprof/profile?seconds=" + strconv.Itoa(int(o.profileFor.Seconds())), stem + "-cpu.pprof"},
		{"/debug/pprof/heap", stem + "-heap.pprof"},
	} {
		if err := download(ctx, strings.TrimRight(o.pprof, "/")+p.path, p.file, o.profileFor+30*time.Second); err != nil {
			logf(log, "profile %s: %v", p.path, err)
			continue
		}
		out = append(out, p.file)
	}
	return out
}

func download(ctx context.Context, url, file string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", res.Status)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	f, err := os.Create(file) //nolint:gosec // under the operator's results directory
	if err != nil {
		return err
	}
	_, err = io.Copy(f, res.Body)
	return errors.Join(err, f.Close())
}

// prepare generates the dataset in the source when needed.
func prepare(ctx context.Context, o options, d dataset.Dataset, log io.Writer) error {
	switch o.generate {
	case "never":
		return nil
	case "auto":
		out, err := psql(ctx, o.psql, d.CountSQL())
		if err == nil && d.Generated(out) {
			logf(log, "dataset %s already has %d rows; not regenerating", d.Name, d.Rows)
			return nil
		}
	case "always":
	default:
		return fmt.Errorf("--generate must be auto, always or never, not %q", o.generate)
	}
	logf(log, "generating dataset %s (%d rows%s)…", d.Name, d.Rows, childNote(d))
	start := time.Now()
	if _, err := psql(ctx, o.psql, strings.Join(d.SQL(), ";\n")+";"); err != nil {
		return fmt.Errorf("generate %s: %w", d.Name, err)
	}
	logf(log, "generated in %s", time.Since(start).Round(time.Second))
	return nil
}

func childNote(d dataset.Dataset) string {
	if d.Embed == nil {
		return ""
	}
	return fmt.Sprintf(" + %d %s", d.ChildRows(), d.Embed.Table)
}

// psql runs sql through the --psql command (stdin), unaligned and quiet, stopping on errors.
func psql(ctx context.Context, command, sql string) (string, error) {
	args := strings.Fields(command)
	if len(args) == 0 {
		return "", errors.New("--psql is empty")
	}
	args = append(args, "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1")
	cmd := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // the operator's own command
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
