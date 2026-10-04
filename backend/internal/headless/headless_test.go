package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/sourcetest"
	"omnidatabus/internal/export"
	"omnidatabus/internal/export/exporttest"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

const (
	pgSecret   = "pg-Secr3t-runner-pw"
	passphrase = "correct-horse-battery-staple-orbit"
)

func ordersSnapshot() *schema.Snapshot {
	n := int64(3)
	return &schema.Snapshot{Database: "shop", Schemas: []schema.Schema{{Name: "public", Tables: []schema.Table{{
		Name: "orders", PrimaryKey: []string{"id"}, RowEstimate: &n, Readable: true, Columns: []schema.Column{
			{Name: "id", Ordinal: 1, UDTName: "int4", DataType: "integer", JSONType: schema.TypeInteger, IsPrimaryKey: true},
			{Name: "customer_id", Ordinal: 2, UDTName: "int4", DataType: "integer", JSONType: schema.TypeInteger},
			{Name: "status", Ordinal: 3, UDTName: "text", DataType: "text", JSONType: schema.TypeString},
		},
	}}}}}
}

// fakeConfig runs b against a fake PostgreSQL whose sessions can't stream snapshots: setup and
// preflight pass, and the pipeline then fails fatally (SOURCE_READ_UNSUPPORTED).
func fakeConfig(t *testing.T, b export.Bundle, stdout, stderr *bytes.Buffer) *Config {
	t.Helper()
	sources := source.NewRegistry()
	sources.Register(&sourcetest.Fake{Type: "postgres", Snapshot: ordersSnapshot(), Result: connectors.TestResult{OK: true}})
	bb := b
	return &Config{
		Bundle: &bb, State: StateDir{Path: filepath.Join(t.TempDir(), "state")}, Sources: sources, Stdout: stdout,
		Log: slog.New(slog.NewJSONHandler(stderr, nil)), Build: buildinfo.Info{Version: "2.0.0", Commit: "abc1234"},
		ProgressInterval: 10 * time.Millisecond,
		Unlock: Unlock{Getenv: func(k string) string {
			if k == PassphraseEnv {
				return passphrase
			}
			return ""
		}},
	}
}

func sealedBundle(t *testing.T) export.Bundle {
	return exporttest.Bundle(t, exporttest.Options{
		SourceSecrets: export.Secrets{"password": pgSecret}, Passphrase: passphrase, TargetPath: t.TempDir(),
	})
}

func TestRun_AC3_PlanMismatch(t *testing.T) {
	dir := StateDir{Path: t.TempDir()}
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	// No state: a new run; --resume requires a checkpoint.
	_, resumed, _, err := dir.Plan("plan-a", false, false, now)
	require.NoError(t, err)
	require.False(t, resumed)
	_, _, _, err = dir.Plan("plan-a", true, false, now)
	require.ErrorContains(t, err, CodeNothingToResume)

	require.NoError(t, dir.Save(State{Version: 1, RunID: "r1", PlanHash: "plan-a", Checkpoint: json.RawMessage(`{"v":1}`)}))
	require.NoError(t, os.WriteFile(filepath.Join(dir.Path, ErrorsFile), []byte("{}\n"), 0o600))
	st, resumed, _, err := dir.Plan("plan-a", false, false, now)
	require.NoError(t, err)
	require.True(t, resumed)
	require.Equal(t, "r1", st.RunID)

	// Another bundle: refused, naming both plans.
	_, _, _, err = dir.Plan("plan-b", false, false, now)
	var ce *ConfigError
	require.ErrorAs(t, err, &ce)
	require.Equal(t, CodeStatePlanMismatch, ce.Code)
	require.Contains(t, ce.Message, "--restart")

	// --restart archives the state and starts over.
	_, resumed, archived, err := dir.Plan("plan-b", false, true, now)
	require.NoError(t, err)
	require.False(t, resumed)
	require.Equal(t, filepath.Join(dir.Path, "archive", "20261002T090000Z"), archived)
	require.FileExists(t, filepath.Join(archived, CheckpointFile))
	require.FileExists(t, filepath.Join(archived, ErrorsFile))
	require.NoFileExists(t, filepath.Join(dir.Path, CheckpointFile))
}

func TestRun_AC7_StateLock(t *testing.T) {
	dir := StateDir{Path: t.TempDir()}
	l, err := dir.Acquire()
	require.NoError(t, err)
	_, err = dir.Acquire()
	var ce *ConfigError
	require.ErrorAs(t, err, &ce)
	require.Equal(t, CodeStateLocked, ce.Code)

	// A runner on the locked directory exits 4 STATE_LOCKED.
	var stdout, stderr bytes.Buffer
	cfg := fakeConfig(t, sealedBundle(t), &stdout, &stderr)
	cfg.State = dir
	_, code, err := Run(context.Background(), cfg)
	require.Equal(t, ExitPreflight, code)
	require.ErrorAs(t, err, &ce)
	require.Equal(t, CodeStateLocked, ce.Code)

	l.Release()
	l, err = dir.Acquire()
	require.NoError(t, err)
	l.Release()
}

func TestResolveRefs_AC5_EnvAndFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "pw")
	require.NoError(t, os.WriteFile(file, []byte("from-file\n"), 0o600))
	env := map[string]string{"PG_PASSWORD": "from-env"}
	u := Unlock{Getenv: func(k string) string { return env[k] }}
	got, err := ResolveRefs(map[string]string{"password": "env:PG_PASSWORD", "caCertPem": "file:" + file}, u)
	require.NoError(t, err)
	require.Equal(t, export.Secrets{"password": "from-env", "caCertPem": "from-file"}, got)

	delete(env, "PG_PASSWORD")
	_, err = ResolveRefs(map[string]string{"password": "env:PG_PASSWORD"}, u)
	var ce *ConfigError
	require.ErrorAs(t, err, &ce)
	require.Equal(t, export.CodeRefUnresolved, ce.Code)
	require.Contains(t, ce.Message, "PG_PASSWORD")
	_, err = ResolveRefs(map[string]string{"password": "file:" + filepath.Join(dir, "missing")}, u)
	require.ErrorAs(t, err, &ce)
	require.Contains(t, ce.Message, "missing")
}

func TestCredentials_PassphraseSources(t *testing.T) {
	b := sealedBundle(t)
	// The file wins over the environment; a wrong one is BUNDLE_PASSPHRASE_INVALID.
	file := filepath.Join(t.TempDir(), "pass")
	require.NoError(t, os.WriteFile(file, []byte(passphrase+"\n"), 0o600))
	src, _, err := Credentials(&b, Unlock{PassphraseFile: file, Getenv: func(string) string { return "wrong" }}, func(string) {})
	require.NoError(t, err)
	require.Equal(t, pgSecret, src["password"])
	_, _, err = Credentials(&b, Unlock{Getenv: func(string) string { return "wrong" }}, func(string) {})
	var ce *ConfigError
	require.ErrorAs(t, err, &ce)
	require.Equal(t, export.CodePassphraseInvalid, ce.Code)
	// Without file or env, the prompt is asked once.
	asked := 0
	_, _, err = Credentials(&b, Unlock{Getenv: func(string) string { return "" }, Prompt: func() (string, error) { asked++; return passphrase, nil }}, func(string) {})
	require.NoError(t, err)
	require.Equal(t, 1, asked)
	// No way to get it: a clear error.
	_, _, err = Credentials(&b, Unlock{Getenv: func(string) string { return "" }}, func(string) {})
	require.ErrorContains(t, err, PassphraseEnv)

	// Plaintext credentials warn.
	p := exporttest.Bundle(t, exporttest.Options{SourceSecrets: export.Secrets{"password": "x"}})
	var warned []string
	_, _, err = Credentials(&p, Unlock{}, func(m string) { warned = append(warned, m) })
	require.NoError(t, err)
	require.Len(t, warned, 1)
	require.Contains(t, warned[0], "plaintext")
}

// TestRun_AC4_FatalAndPreflightExitCodes: a fatal pipeline error exits 3 with a report; an
// unreachable source or drift exits 4 before anything runs.
func TestRun_AC4_FatalAndPreflightExitCodes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := fakeConfig(t, sealedBundle(t), &stdout, &stderr)
	rep, code, err := Run(context.Background(), cfg)
	require.NoError(t, err, stderr.String())
	require.Equal(t, ExitFailed, code)
	require.Equal(t, StatusFailed, rep.Status)
	require.Equal(t, "SOURCE_READ_UNSUPPORTED", rep.Reason)
	_, err = cfg.State.ReadReport()
	require.NoError(t, err)

	// Unreachable source: 4.
	sources := source.NewRegistry()
	sources.Register(&sourcetest.Fake{Type: "postgres", OpenErr: errors.New("connection refused")})
	cfg = fakeConfig(t, sealedBundle(t), &stdout, &stderr)
	cfg.Sources = sources
	_, code, err = Run(context.Background(), cfg)
	require.Equal(t, ExitPreflight, code)
	require.ErrorContains(t, err, "connection refused")

	// Drift: 4 SCHEMA_DRIFT naming the column; --allow-drift continues (text → int coerces).
	fp := export.ComputeFingerprint(ordersSnapshot(), export.References{"public.orders": {"id": true, "customer_id": true, "status": true}})
	b := exporttest.Bundle(t, exporttest.Options{TargetPath: t.TempDir(), Fingerprint: fp})
	snap := ordersSnapshot()
	snap.Table("public", "orders").Column("customer_id").UDTName = "text"
	sources = source.NewRegistry()
	sources.Register(&sourcetest.Fake{Type: "postgres", Snapshot: snap, Result: connectors.TestResult{OK: true}})
	cfg = fakeConfig(t, b, &stdout, &stderr)
	cfg.Sources = sources
	_, code, err = Run(context.Background(), cfg)
	require.Equal(t, ExitPreflight, code)
	var ce *ConfigError
	require.ErrorAs(t, err, &ce)
	require.Equal(t, export.CodeSchemaDrift, ce.Code)
	require.Equal(t, []string{"public.orders: changed customer_id: int4 → text"}, ce.Details)
	cfg.AllowDrift = true
	stdout.Reset()
	_, code, err = Run(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, ExitFailed, code, "past preflight: the fake source then can't stream")
	require.Contains(t, stdout.String(), `"event":"warning"`)
	require.Contains(t, stdout.String(), "schema drift allowed")
}

func TestRun_AC4_ExitCodeClassification(t *testing.T) {
	cases := []struct {
		name    string
		o       jobs.Outcome
		drained bool
		status  string
		code    int
	}{
		{"complete", jobs.Outcome{Finished: true}, false, StatusCompleted, ExitOK},
		{"errors below threshold", jobs.Outcome{Finished: true, Failed: 3}, false, StatusCompletedWithErrors, ExitWithErrors},
		{"threshold", jobs.Outcome{Fatal: &jobs.Fatal{Code: jobs.ReasonErrorThreshold}, Failed: 2000}, false, StatusFailed, ExitFailed},
		{"fatal write", jobs.Outcome{Fatal: &jobs.Fatal{Code: "TARGET_AUTH_FAILED"}}, false, StatusFailed, ExitFailed},
		{"cancelled", jobs.Outcome{Finished: false}, true, StatusCancelled, ExitCancelled},
		{"stopped otherwise", jobs.Outcome{Finished: false}, false, StatusFailed, ExitFailed},
	}
	for _, c := range cases {
		status, code, _, _ := classify(c.o, c.drained)
		require.Equal(t, c.status, status, c.name)
		require.Equal(t, c.code, code, c.name)
	}
}

// TestRun_AC5_StdoutContract: every stdout line is a JSON event with the documented fields,
// and logs only go to stderr.
func TestRun_AC5_StdoutContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, _, err := Run(context.Background(), fakeConfig(t, sealedBundle(t), &stdout, &stderr))
	require.NoError(t, err)
	var kinds []string
	for _, line := range bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n")) {
		var m map[string]any
		require.NoError(t, json.Unmarshal(line, &m), string(line))
		for _, f := range []string{"ts", "event", "jobId", "read", "written", "failed", "docsPerSec", "etaSec"} {
			require.Contains(t, m, f, string(line))
		}
		kinds = append(kinds, m["event"].(string))
		require.NotContains(t, m, "level", "no log line on stdout")
	}
	require.Equal(t, EventStart, kinds[0])
	require.Equal(t, EventDone, kinds[len(kinds)-1])
	require.Contains(t, stderr.String(), `"level"`, "logs are on stderr")

	// The sink: mapping_done once per mapping, progress at the interval, quiet keeps only done.
	var out bytes.Buffer
	now := time.Unix(0, 0)
	sink := NewProgressSink(&out, "run-1", time.Second, false, map[string]string{"m1": "orders"}, func() time.Time { return now })
	snap := progress.Snapshot{Processed: 10, Written: 9, Failed: 1, Mappings: []progress.MappingProgress{{MappingID: "m1", Phase: progress.PhaseDone, Processed: 10, Written: 9, Failed: 1}}}
	sink.Snapshot(snap)
	sink.Snapshot(snap)
	now = now.Add(2 * time.Second)
	sink.Snapshot(snap)
	got := out.String()
	require.Equal(t, 1, bytes.Count([]byte(got), []byte(`"event":"mapping_done"`)))
	require.Contains(t, got, `"mapping":"orders"`)
	require.Equal(t, 2, bytes.Count([]byte(got), []byte(`"event":"progress"`)))
	out.Reset()
	quiet := NewProgressSink(&out, "run-1", time.Second, true, nil, time.Now)
	quiet.Snapshot(snap)
	quiet.Emit(Event{Event: EventStart})
	quiet.Emit(Event{Event: EventDone})
	require.Equal(t, 1, bytes.Count(out.Bytes(), []byte("\n")))
}

func TestRun_AC5_NoSecretsInOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := fakeConfig(t, sealedBundle(t), &stdout, &stderr)
	_, _, err := Run(context.Background(), cfg)
	require.NoError(t, err)
	report, err := os.ReadFile(filepath.Join(cfg.State.Path, ReportFile))
	require.NoError(t, err)
	checkpoint, err := os.ReadFile(filepath.Join(cfg.State.Path, CheckpointFile))
	require.NoError(t, err)
	for name, data := range map[string][]byte{"stdout": stdout.Bytes(), "stderr": stderr.Bytes(), "report": report, "checkpoint": checkpoint} {
		require.Empty(t, export.ScanForSecrets(data, pgSecret, passphrase), name)
	}
}

func TestRun_AC7_BuildInfoInStartAndReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rep, _, err := Run(context.Background(), fakeConfig(t, sealedBundle(t), &stdout, &stderr))
	require.NoError(t, err)
	require.Equal(t, ReportBuild{Version: "2.0.0", Commit: "abc1234"}, rep.Odb)
	first := bytes.SplitN(stdout.Bytes(), []byte("\n"), 2)[0]
	var start Event
	require.NoError(t, json.Unmarshal(first, &start))
	require.Equal(t, EventStart, start.Event)
	require.Equal(t, "2.0.0", start.Version)
	require.Equal(t, "abc1234", start.Commit)
	require.Equal(t, []string{"orders"}, start.Mappings)
	require.Equal(t, rep.RunID, start.JobID)
}

func TestErrorLog_RotatesAndSummarizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), ErrorsFile)
	l, err := openErrorLog(path)
	require.NoError(t, err)
	l.size = maxErrorsFileBytes // as if full
	require.NoError(t, l.append(nil))
	require.NoError(t, l.append(errorsOf(3, "write", "DOC_TOO_LARGE")))
	l.close()
	require.FileExists(t, path+".1")
	// Reopened (a resumed run), it counts what's there.
	l, err = openErrorLog(path)
	require.NoError(t, err)
	require.NoError(t, l.append(errorsOf(2, "transform", "TYPE_MISMATCH")))
	s := l.summary()
	l.close()
	require.EqualValues(t, 5, s.Total)
	require.Equal(t, []ErrorCount{{Stage: "write", Code: "DOC_TOO_LARGE", Count: 3}, {Stage: "transform", Code: "TYPE_MISMATCH", Count: 2}}, s.ByCode)
	require.Len(t, s.Samples, 5)
}

func errorsOf(n int, stage, code string) []store.JobError {
	out := make([]store.JobError, n)
	for i := range out {
		out[i] = store.JobError{JobID: "j", Seq: int64(i), Stage: stage, Code: code, Message: "m"}
	}
	return out
}

func TestAsConfigError(t *testing.T) {
	p := problem.New(422, "MAPPING_INVALID", "Mappings need fixing", "")
	p.FieldErrors = []problem.FieldError{{Field: "key", Message: "bad"}}
	ce := asConfigError(p)
	require.Equal(t, "MAPPING_INVALID", ce.Code)
	require.Equal(t, "Mappings need fixing", ce.Message, "the title when there's no detail")
	require.Equal(t, []string{"key: bad"}, ce.Details)
	require.Equal(t, export.CodeIntegrity, asConfigError(&export.Error{Code: export.CodeIntegrity, Message: "m"}).Code)
	require.Equal(t, CodeConfig, asConfigError(errors.New("boom")).Code)
	orig := &ConfigError{Code: CodeStateLocked, Message: "x"}
	require.Same(t, orig, asConfigError(orig))
	require.Equal(t, "STATE_LOCKED: x", orig.Error())
}

func TestWatch_AbortAndDrain(t *testing.T) {
	// An abort before any drain closes both.
	abortIn := make(chan struct{})
	drain, abort := make(chan struct{}), make(chan struct{})
	stop := watch(nil, abortIn, drain, abort)
	close(abortIn)
	<-drain
	<-abort
	stop()
	// A drain, then an abort.
	drainIn, abortIn := make(chan struct{}), make(chan struct{})
	drain, abort = make(chan struct{}), make(chan struct{})
	stop = watch(drainIn, abortIn, drain, abort)
	close(drainIn)
	<-drain
	require.False(t, closed(abort))
	close(abortIn)
	<-abort
	stop()
}

func TestStateJobs_WarningsGoToStdout(t *testing.T) {
	var out bytes.Buffer
	sink := NewProgressSink(&out, "run-1", 0, false, nil, time.Now)
	mem := memstore.New(nil)
	j := &stateJobs{Jobs: mem.Jobs(), sink: sink, log: slog.New(slog.DiscardHandler)}
	require.NoError(t, j.AppendEvent(context.Background(), "job", store.JobEvent{Kind: "warning", Message: "careful"}))
	require.NoError(t, j.AppendEvent(context.Background(), "job", store.JobEvent{Kind: "created", Message: "quiet"}))
	require.Contains(t, out.String(), `"event":"warning","jobId":"run-1","read":0,"written":0,"skipped":0,"failed":0,"docsPerSec":0,"etaSec":null,"message":"careful"`)
	require.NotContains(t, out.String(), "quiet")
}
