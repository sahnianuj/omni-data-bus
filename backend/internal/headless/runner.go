package headless

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/couchbase"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/discovery"
	"omnidatabus/internal/export"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/mappings"
	"omnidatabus/internal/pipeline"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

// ws and user are the in-memory store's tenant: a headless run has no accounts.
const (
	ws   store.WorkspaceID = "headless"
	user string            = "odb-run"
)

// Config is one `odb run` invocation.
type Config struct {
	Bundle *export.Bundle
	State  StateDir
	// Mappings limits the run to these mapping names or IDs (default: all).
	Mappings []string
	// TargetPath replaces a directory target's path (per environment).
	TargetPath string
	// Readers, Writers and Throttle override the bundle's options (0 = keep).
	Readers, Writers, Throttle int
	Resume, Restart            bool
	AllowDrift                 bool
	Unlock                     Unlock
	// RunConfig replaces the bundle's connection details and secrets (--config, OC-155);
	// RunConfigPath names it in messages.
	RunConfig        *export.RunConfig
	RunConfigPath    string
	ProgressInterval time.Duration
	Quiet            bool
	Stdout           io.Writer
	Log              *slog.Logger
	Build            buildinfo.Info
	// Drain stops reading and checkpoints (first SIGINT/SIGTERM); Abort stops at once (second).
	Drain, Abort <-chan struct{}
	// Sources and Targets override the connector registries (tests); nil = the real ones.
	Sources *source.Registry
	Targets *target.Registry
	Now     func() time.Time
	// PersistEvery is the checkpoint cadence (default 1 s).
	PersistEvery time.Duration
}

func (c *Config) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// env is the in-memory tool a bundle runs in: the same services the server wires, over a
// memstore that holds only this bundle.
type env struct {
	cfg     *Config
	st      *stateStore
	project store.Project
	conns   *connections.Service
	disc    *discovery.Service
	maps    *mappings.Service
	pre     *jobs.Preflighter
	ids     []string          // the mappings to run
	names   map[string]string // mapping ID → name
	drift   []export.Drift
}

// warnf logs a warning to stderr.
func (c *Config) warn(msg string) { c.Log.Warn(msg) }

// setup loads the bundle into a fresh in-memory store, unlocks the credentials, discovers the
// source and checks it against the bundle's fingerprint.
func setup(ctx context.Context, cfg *Config) (*env, error) {
	b := cfg.Bundle
	src, tgt, err := Credentials(b, cfg.Unlock, cfg.warn)
	if err != nil {
		return nil, err
	}
	// The run config's connection details win over the bundle's; the bundle, its hashes and the
	// plan stay as exported, so a checkpoint survives a password change.
	srcCfg, src, err := cfg.connection("source", b.Source, src)
	if err != nil {
		return nil, err
	}
	tgtCfg, tgt, err := cfg.connection("target", b.Target, tgt)
	if err != nil {
		return nil, err
	}
	key := make([]byte, crypto.KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	kr, err := crypto.ParseKeyring(base64.StdEncoding.EncodeToString(key), "", "")
	if err != nil {
		return nil, err
	}
	sealer := crypto.NewLocalSealer(kr) // ephemeral: the credentials only live for this process
	mem := memstore.New(store.SystemClock{})
	e := &env{cfg: cfg, st: &stateStore{Store: mem}, names: map[string]string{}}
	e.st.jobs = &stateJobs{Jobs: mem.Jobs(), dir: cfg.State, log: cfg.Log}
	if e.project, err = mem.Projects().Create(ctx, ws, store.NewProject{Name: b.Project.Name, CreatedBy: user}); err != nil {
		return nil, err
	}

	var roots []string
	if b.Target.Type == store.KindDirectory {
		dc, err := directory.ParseConfig(tgtCfg)
		if err != nil {
			return nil, &ConfigError{Code: CodeConfig, Message: "The bundle's directory target is invalid: " + err.Error()}
		}
		if cfg.TargetPath != "" {
			if !strings.HasPrefix(cfg.TargetPath, "/") {
				return nil, &ConfigError{Code: CodeConfig, Message: "--target-path must be an absolute path."}
			}
			dc.Path = cfg.TargetPath
		}
		dc.CreateIfMissing = true
		tgtCfg = dc.JSON()
		roots = []string{dc.Path} // the runner writes exactly where the bundle (or operator) says
	} else if cfg.TargetPath != "" {
		return nil, &ConfigError{Code: CodeConfig, Message: "--target-path only applies to a directory target; this bundle writes to " + b.Target.Type + "."}
	}
	sources, targets := cfg.Sources, cfg.Targets
	if sources == nil {
		sources = source.NewRegistry()
		sources.Register(postgres.New())
	}
	if targets == nil {
		targets = target.NewRegistry()
		targets.Register(couchbase.New())
		targets.Register(directory.New(roots))
	}
	for _, c := range []struct {
		role    store.Role
		conn    export.Connection
		config  json.RawMessage
		secrets export.Secrets
	}{{store.RoleSource, b.Source, srcCfg, src}, {store.RoleTarget, b.Target, tgtCfg, tgt}} {
		id := store.ConnectionID(e.project.ID, c.role)
		sec := export.ToConnectionSecrets(c.secrets)
		sealed := store.Sealed{}
		if len(c.secrets) > 0 {
			if sealed, err = connections.Seal(sealer, ws, e.project.ID, id, sec); err != nil {
				return nil, err
			}
		}
		if _, err := mem.Connections().Upsert(ctx, ws, e.project.ID, c.role, store.ConnectionInput{
			Type: c.conn.Type, Name: c.conn.Name, Config: c.config, Secret: sealed, SecretMeta: connections.Meta(sec), LastTestStatus: "ok", CreatedBy: user,
		}); err != nil {
			return nil, err
		}
	}
	e.conns = connections.NewService(e.st, sealer, sources, targets, nil, nil)
	e.disc = discovery.NewService(e.st, e.conns.OpenSource, nil)
	e.maps = mappings.NewService(e.st, e.disc, e.conns.OpenSource, e.conns.OpenKeyspaces)
	e.maps.StatLocation = e.conns.StatLocation
	e.maps.CaseSensitive = e.conns.CaseSensitive

	for _, r := range b.Relationships {
		if _, err := mem.Relationships().Create(ctx, ws, store.Relationship{
			ID: r.ID, ProjectID: e.project.ID, Name: r.Name, From: r.From, To: r.To, Cardinality: r.Cardinality, CreatedBy: user,
		}); err != nil {
			return nil, err
		}
	}
	for _, m := range b.Mappings {
		if len(cfg.Mappings) > 0 && !slices.Contains(cfg.Mappings, m.Name) && !slices.Contains(cfg.Mappings, m.ID) {
			continue
		}
		sm := store.Mapping{ID: m.ID, ProjectID: e.project.ID, SourceSchema: m.Source.Schema, SourceTable: m.Source.Table, Spec: m.Spec, Status: mapping.StatusReady, CreatedBy: user}
		loc := m.Location
		if loc.Kind == store.KindDirectory {
			loc.CreateIfMissing = true // a fresh runner machine doesn't have the folders yet
		}
		sm.SetLocation(loc)
		if _, err := mem.Mappings().Create(ctx, ws, sm); err != nil {
			return nil, err
		}
		e.ids = append(e.ids, m.ID)
		e.names[m.ID] = m.Name
	}
	if len(e.ids) == 0 {
		return nil, &ConfigError{Code: CodeConfig, Message: "No mapping matches --mapping " + strings.Join(cfg.Mappings, ", ") + "."}
	}

	// Discover the source tables the bundle reads, then compare with the export (OC-050).
	if err := e.discover(ctx); err != nil {
		return nil, err
	}
	e.pre = &jobs.Preflighter{
		Store: e.st, Validate: e.maps.Validate, TestConn: e.conns.TestStored, OpenSource: e.conns.OpenSource,
		Target: e.conns.PreflightTarget, Latest: e.disc.Latest, Timeout: 2 * time.Minute,
	}
	return e, nil
}

// discover reads the referenced schemas into the store's latest snapshot and checks drift.
func (e *env) discover(ctx context.Context) error {
	srcID := store.ConnectionID(e.project.ID, store.RoleSource)
	sess, conn, err := e.conns.OpenSource(ctx, ws, srcID)
	if err != nil {
		return &ConfigError{Code: errCode(err, "SOURCE_UNREACHABLE"), Message: "Can't connect to the source: " + errText(err)}
	}
	defer func() { _ = sess.Close() }()
	schemas := map[string]bool{}
	for k := range e.cfg.Bundle.SourceFingerprint.Tables {
		s, _, _ := strings.Cut(k, ".")
		schemas[s] = true
	}
	for _, m := range e.cfg.Bundle.Mappings {
		schemas[m.Source.Schema] = true
	}
	opts := source.DiscoverOptions{}
	for s := range schemas {
		opts.Schemas = append(opts.Schemas, s)
	}
	sort.Strings(opts.Schemas)
	snap, err := sess.Discover(ctx, opts)
	if err != nil {
		return &ConfigError{Code: errCode(err, "SOURCE_UNKNOWN"), Message: "Can't read the source schema: " + errText(err)}
	}
	payload, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if err := e.st.Snapshots().Save(ctx, ws, store.DiscoverySnapshot{
		ID: uuid.Must(uuid.NewV7()).String(), ConnectionID: conn.ID, CapturedAt: e.cfg.now(), TableCount: snap.TableCount(), Payload: payload,
	}, 1); err != nil {
		return err
	}
	return e.checkDrift(snap)
}

// checkDrift refuses a changed source (SCHEMA_DRIFT) unless --allow-drift and every change is
// compatible.
func (e *env) checkDrift(snap *schema.Snapshot) error {
	e.drift = export.CompareFingerprint(e.cfg.Bundle.SourceFingerprint, snap)
	if len(e.drift) == 0 {
		return nil
	}
	details := make([]string, len(e.drift))
	incompatible := false
	for i, d := range e.drift {
		details[i] = d.String()
		incompatible = incompatible || d.Incompatible
	}
	switch {
	case !e.cfg.AllowDrift:
		return &ConfigError{Code: export.CodeSchemaDrift, Message: "The source schema changed since the export. Re-export the migration, or run with --allow-drift if the changes are compatible.", Details: details}
	case incompatible:
		return &ConfigError{Code: export.CodeSchemaDrift, Message: "The source schema changed in a way the mappings can't handle (a column or table is gone, or changed between scalar and array). Re-export the migration.", Details: details}
	}
	e.cfg.warn("source schema drift allowed (--allow-drift): " + export.DriftMessage(e.drift))
	return nil
}

func (e *env) options() jobs.Options {
	o := e.cfg.Bundle.JobOptions.Jobs()
	if n := e.cfg.Readers; n > 0 {
		o.ReadParallelism = strconv.Itoa(min(n, pipeline.MaxPartitions))
	}
	if n := e.cfg.Writers; n > 0 {
		o.WriteConcurrency = n
	}
	if n := e.cfg.Throttle; n > 0 {
		o.MaxDocsPerSecond = &n
	}
	o.MappingIDs = e.ids
	return o
}

// PreflightReport is what `odb run preflight` prints with --json.
type PreflightReport struct {
	OK        bool                   `json:"ok"`
	Checks    []jobs.Check           `json:"checks"`
	Mappings  []jobs.PlannedMapping  `json:"mappings"`
	TotalRows int64                  `json:"totalRows"`
	Drift     []export.Drift         `json:"drift,omitempty"`
	Error     *ConfigError           `json:"error,omitempty"`
	Bundle    map[string]interface{} `json:"bundle"`
}

// Preflight connects, unlocks the credentials, checks drift and runs the tool's pre-flight
// checks (source reachable, mappings valid, target writable with room to spare). The checks
// may create a missing folder or collection the mapping allows creating, as in the tool.
func Preflight(ctx context.Context, cfg *Config) (PreflightReport, int) {
	rep := PreflightReport{Bundle: map[string]interface{}{"sha256": cfg.Bundle.SHA256(), "project": cfg.Bundle.Project.Name}}
	e, err := setup(ctx, cfg)
	if err != nil {
		rep.Error = asConfigError(err)
		return rep, ExitPreflight
	}
	rep.Drift = e.drift
	res, err := e.pre.Run(ctx, ws, user, e.project.ID, e.options())
	if err != nil {
		rep.Error = asConfigError(err)
		return rep, ExitPreflight
	}
	rep.OK, rep.Checks, rep.Mappings, rep.TotalRows = res.OK, res.Checks, res.Mappings, res.TotalRows
	if !res.OK {
		return rep, ExitPreflight
	}
	return rep, ExitOK
}

func asConfigError(err error) *ConfigError {
	var ce *ConfigError
	if errors.As(err, &ce) {
		return ce
	}
	var be *export.Error
	if errors.As(err, &be) {
		return &ConfigError{Code: be.Code, Message: be.Message, Details: be.Details}
	}
	var p problem.Problem
	if errors.As(err, &p) {
		ce := &ConfigError{Code: p.Code, Message: p.Detail}
		for _, f := range p.FieldErrors {
			ce.Details = append(ce.Details, f.Field+": "+f.Message)
		}
		if ce.Message == "" {
			ce.Message = p.Title
		}
		return ce
	}
	return &ConfigError{Code: CodeConfig, Message: err.Error()}
}

func errCode(err error, fallback string) string {
	var p problem.Problem
	if errors.As(err, &p) && p.Code != "" {
		return p.Code
	}
	return fallback
}

func errText(err error) string {
	var p problem.Problem
	if errors.As(err, &p) && p.Detail != "" {
		return p.Detail
	}
	return err.Error()
}

// Run executes the bundle and returns the exit code (architecture §6.4). Preflight and
// configuration problems are returned as errors with exit code 4.
func Run(ctx context.Context, cfg *Config) (Report, int, error) {
	b := cfg.Bundle
	if err := cfg.State.Ensure(); err != nil {
		return Report{}, ExitPreflight, &ConfigError{Code: CodeConfig, Message: "Can't create the state directory: " + err.Error()}
	}
	lock, err := cfg.State.Acquire()
	if err != nil {
		return Report{}, ExitPreflight, err
	}
	defer lock.Release()
	planHash := b.PlanHash()
	prev, resumed, archived, err := cfg.State.Plan(planHash, cfg.Resume, cfg.Restart, cfg.now())
	if err != nil {
		return Report{}, ExitPreflight, err
	}
	if archived != "" {
		cfg.Log.Info("previous state archived (--restart)", "path", archived)
	}
	e, err := setup(ctx, cfg)
	if err != nil {
		return Report{}, ExitPreflight, err
	}
	pre, err := e.pre.Run(ctx, ws, user, e.project.ID, e.options())
	if err != nil {
		return Report{}, ExitPreflight, asConfigError(err)
	}
	if !pre.OK {
		ce := &ConfigError{Code: CodePreflightFailed, Message: "Pre-flight checks failed."}
		for _, c := range pre.Checks {
			if c.Status == jobs.CheckFail {
				ce.Details = append(ce.Details, c.Label+": "+c.Detail)
			}
		}
		return Report{}, ExitPreflight, ce
	}

	// The job, created as the tool creates one (frozen specs, plan hashes, target emptiness).
	svc := jobs.NewService(e.st, e.pre, nil)
	svc.PlanHash = e.maps.PlanHash
	view, _, err := svc.Create(ctx, ws, user, e.project.ID, e.options())
	if err != nil {
		return Report{}, ExitPreflight, asConfigError(err)
	}
	job, err := e.st.Jobs().GetAny(ctx, view.ID)
	if err != nil {
		return Report{}, ExitFailed, err
	}
	opts, _ := jobs.ParseOptions(job.Options)
	state := prev
	if !resumed {
		state = State{Version: 1, RunID: uuid.Must(uuid.NewV7()).String(), PlanHash: planHash, StartedAt: cfg.now(), TargetEmpty: opts.TargetEmpty}
	}
	state.BundleSHA256 = b.SHA256()
	e.st.jobs.state = state
	if resumed {
		// The first attempt decided which targets were empty; this one's output is there now.
		opts.TargetEmpty = state.TargetEmpty
		raw, _ := json.Marshal(opts)
		job.Options, job.Checkpoint = raw, state.Checkpoint
		if job, err = e.st.Jobs().Update(ctx, job); err != nil {
			return Report{}, ExitFailed, err
		}
	}
	if err := cfg.State.Save(state); err != nil {
		return Report{}, ExitPreflight, &ConfigError{Code: CodeConfig, Message: "Can't write to the state directory: " + err.Error()}
	}
	errs, err := openErrorLog(cfg.State.file(ErrorsFile))
	if err != nil {
		return Report{}, ExitPreflight, &ConfigError{Code: CodeConfig, Message: "Can't write to the state directory: " + err.Error()}
	}
	defer errs.close()
	e.st.jobs.errs = errs
	sink := NewProgressSink(cfg.Stdout, state.RunID, cfg.ProgressInterval, cfg.Quiet, e.names, cfg.now)
	e.st.jobs.sink = sink

	names := make([]string, 0, len(e.ids))
	for _, id := range e.ids {
		names = append(names, e.names[id])
	}
	sink.Emit(Event{Event: EventStart, Version: cfg.Build.Version, Commit: cfg.Build.Commit, BundleSHA256: b.SHA256(), Mappings: names, Resumed: resumed})
	for _, d := range e.drift {
		sink.Emit(Event{Event: EventWarning, Message: "schema drift allowed: " + d.String()})
	}
	cfg.Log.Info("run started", "run_id", state.RunID, "bundle_sha256", b.SHA256(), "mappings", len(e.ids), "resumed", resumed)

	persist := cfg.PersistEvery
	if persist == 0 {
		persist = time.Second
	}
	runner := pipeline.NewRunner(pipeline.Deps{
		Store: e.st, OpenSource: e.conns.OpenSource, OpenWriter: e.conns.OpenWriter, Latest: e.disc.Latest,
		Log: cfg.Log, CountTarget: e.conns.CountDocuments, PersistEvery: persist,
	})
	drain, abort := make(chan struct{}), make(chan struct{})
	stop := watch(cfg.Drain, cfg.Abort, drain, abort)
	defer stop()
	started := cfg.now()
	out := runner.Start(ctx, job, jobs.Control{Drain: drain, Abort: abort}).Wait()

	// The final checkpoint, even after a cancel: a rerun resumes from it.
	if len(out.Checkpoint) > 0 {
		e.st.jobs.mu.Lock()
		st := e.st.jobs.state
		st.Checkpoint, st.UpdatedAt = out.Checkpoint, cfg.now()
		e.st.jobs.state = st
		e.st.jobs.mu.Unlock()
		if err := cfg.State.Save(st); err != nil {
			cfg.Log.Error("checkpoint not saved", "error", err.Error())
		}
	}
	status, code, reason, detail := classify(out, closed(drain))
	snap := progress.Parse(job.ID, out.Progress)
	last, verified := e.st.jobs.snapshot()
	if len(snap.Mappings) == 0 {
		snap = last
	}
	rep := Report{
		ReportVersion: ReportVersion, RunID: state.RunID, BundleSHA256: b.SHA256(), PlanHash: planHash, Project: b.Project,
		Odb: ReportBuild{Version: cfg.Build.Version, Commit: cfg.Build.Commit}, Status: status, ExitCode: code, Reason: reason, ReasonDetail: detail,
		StartedAt: state.StartedAt, FinishedAt: cfg.now(),
		Totals:   ReportTotals{Read: snap.Processed, Written: snap.Written, Skipped: snap.Skipped, Failed: snap.Failed},
		Mappings: []ReportMapping{}, Errors: errs.summary(), Verification: verified,
	}
	rep.DurationSec = rep.FinishedAt.Sub(started).Seconds()
	if rep.DurationSec > 0 {
		rep.DocsPerSec = float64(snap.Written) / rep.DurationSec
	}
	for _, m := range snap.Mappings {
		rep.Mappings = append(rep.Mappings, ReportMapping{
			ID: m.MappingID, Name: e.names[m.MappingID], Table: m.Table, Target: m.Target, Phase: m.Phase,
			Read: m.Processed, Written: m.Written, Skipped: m.Skipped, Failed: m.Failed,
		})
	}
	if err := cfg.State.WriteReport(rep); err != nil {
		cfg.Log.Error("report.json not written", "error", err.Error())
	}
	sink.MappingsDone(snap)
	done := Totals(EventDone, snap)
	done.Status, done.ExitCode = status, &code
	if reason != "" {
		done.Message = reason + ": " + detail
	}
	sink.Emit(done)
	cfg.Log.Info("run ended", "status", status, "exit_code", code, "written", snap.Written, "failed", snap.Failed)
	return rep, code, nil
}

// classify maps a run's outcome to its status and exit code.
func classify(o jobs.Outcome, drained bool) (status string, code int, reason, detail string) {
	switch {
	case o.Fatal != nil:
		return StatusFailed, ExitFailed, o.Fatal.Code, o.Fatal.Message
	case !o.Finished && drained:
		return StatusCancelled, ExitCancelled, jobs.ReasonCancelled, "Stopped by a signal after checkpointing; run again to resume."
	case !o.Finished:
		return StatusFailed, ExitFailed, "INCOMPLETE", "The run stopped before every mapping finished."
	case o.Failed > 0:
		return StatusCompletedWithErrors, ExitWithErrors, "", ""
	}
	return StatusCompleted, ExitOK, "", ""
}

func closed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// watch forwards the caller's drain and abort signals to the run's channels.
func watch(drainIn, abortIn <-chan struct{}, drain, abort chan struct{}) func() {
	quit := make(chan struct{})
	go func() {
		select {
		case <-drainIn:
			close(drain)
		case <-abortIn:
			close(drain)
			close(abort)
			return
		case <-quit:
			return
		}
		select {
		case <-abortIn:
			close(abort)
		case <-quit:
		}
	}()
	return func() { close(quit) }
}

// Status summarizes a state directory: the report when the run ended, else the checkpoint.
func Status(dir StateDir, w io.Writer) int {
	if rep, err := dir.ReadReport(); err == nil {
		fmt.Fprintf(w, "Run %s: %s (exit %d)\n", rep.RunID, rep.Status, rep.ExitCode)
		fmt.Fprintf(w, "  finished %s after %.0fs: %d written, %d skipped, %d failed (%.0f docs/s)\n",
			rep.FinishedAt.Format(time.RFC3339), rep.DurationSec, rep.Totals.Written, rep.Totals.Skipped, rep.Totals.Failed, rep.DocsPerSec)
		for _, m := range rep.Mappings {
			fmt.Fprintf(w, "  %-20s %-10s %d written, %d failed\n", m.Name, m.Phase, m.Written, m.Failed)
		}
		if rep.Reason != "" {
			fmt.Fprintf(w, "  reason: %s — %s\n", rep.Reason, rep.ReasonDetail)
		}
		return ExitOK
	}
	st, ok, err := dir.Load()
	if err != nil || !ok {
		fmt.Fprintf(w, "No run in %s.\n", dir.Path)
		return ExitPreflight
	}
	cp, _ := pipeline.ParseCheckpoint(st.Checkpoint)
	fmt.Fprintf(w, "Run %s: in progress or interrupted (checkpoint %s)\n", st.RunID, st.UpdatedAt.Format(time.RFC3339))
	ids := make([]string, 0, len(cp.Mappings))
	for id := range cp.Mappings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := cp.Mappings[id]
		state := "running"
		if m.Done {
			state = "done"
		}
		fmt.Fprintf(w, "  %-36s %-8s %d written, %d failed\n", id, state, m.Written, m.Failed)
	}
	return ExitOK
}

// VerifyResult is one mapping's check by `odb run verify`.
type VerifyResult struct {
	Mapping  string `json:"mapping"`
	Target   string `json:"target"`
	Expected int64  `json:"expected"`
	Found    int64  `json:"found"`
	OK       bool   `json:"ok"`
	Note     string `json:"note,omitempty"`
}

// Verify counts each mapping's documents in the target and compares them with what the run
// wrote (written + skipped rows of the report). Exit 0 when every count matches, 2 otherwise.
func Verify(ctx context.Context, cfg *Config) ([]VerifyResult, int, error) {
	rep, err := cfg.State.ReadReport()
	if err != nil {
		return nil, ExitPreflight, &ConfigError{Code: CodeNothingToResume, Message: "No finished run in " + cfg.State.Path + " to verify (report.json is missing)."}
	}
	if rep.BundleSHA256 != cfg.Bundle.SHA256() && rep.PlanHash != cfg.Bundle.PlanHash() {
		return nil, ExitPreflight, &ConfigError{Code: CodeStatePlanMismatch, Message: "The state directory's run is of another bundle."}
	}
	e, err := setup(ctx, cfg)
	if err != nil {
		return nil, ExitPreflight, err
	}
	st, _, _ := cfg.State.Load()
	byID := map[string]ReportMapping{}
	for _, m := range rep.Mappings {
		byID[m.ID] = m
	}
	code := ExitOK
	var out []VerifyResult
	for _, m := range cfg.Bundle.Mappings {
		if !slices.Contains(e.ids, m.ID) {
			continue
		}
		rm := byID[m.ID]
		res := VerifyResult{Mapping: m.Name, Target: m.Location.String(), Expected: rm.Written + rm.Skipped}
		n, err := e.conns.CountDocuments(ctx, ws, store.ConnectionID(e.project.ID, store.RoleTarget), m.Location)
		switch {
		case err != nil:
			res.Note = "can't count: " + errText(err)
			code = ExitWithErrors
		case !st.TargetEmpty[m.ID]:
			res.Found, res.OK = n, n >= res.Expected
			res.Note = "the target held documents before the run, so only a lower bound is checked"
		default:
			res.Found, res.OK = n, n == res.Expected
		}
		if !res.OK && err == nil {
			code = ExitWithErrors
		}
		out = append(out, res)
	}
	return out, code, nil
}

func readFile(path string) ([]byte, error) { return os.ReadFile(path) } //nolint:gosec // G304: state directory files
