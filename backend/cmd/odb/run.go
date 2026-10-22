package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/export"
	"omnidatabus/internal/headless"
	"omnidatabus/internal/observability"
)

const runUsage = `usage: odb run [options]              migrate without the tool
       odb run preflight [options]    connect, check drift and the target; no documents written
       odb run status [options]       summarize a run's checkpoint or report
       odb run verify [options]       count each mapping's documents in the target
       odb run target-path [options]  print a directory target's folder (nothing for other targets)

Run in an export folder, odb finds config.json and mappings.json itself: in the current
directory, else beside odb (or the folder above a bin/ that holds odb).

options:
  --mappings FILE          what to migrate (else $ODB_MAPPINGS, else mappings.json as above;
                           --bundle FILE is the same, for older exports)
  --state-dir DIR          checkpoint, errors.jsonl and report.json (else $ODB_STATE_DIR, else
                           odb-state beside the mappings.json found, else ./odb-state)
  --mapping NAME           run only this mapping (repeatable)
  --target-path DIR        write a directory target here instead of config.json's path
                           (else $ODB_TARGET_PATH)
  --readers N --writers N  read partitions per table (1-8) and concurrent writes
  --throttle DOCS_PER_S    limit the write rate
  --resume | --restart     require a checkpoint to resume | archive the state and start over
  --allow-drift            continue when the source changed compatibly since the export
  --config FILE            source and target connection details (else $ODB_CONFIG, else
                           config.json as above); plain-text passwords in it are saved back
                           base64-encoded
  --passphrase-file FILE   sealed credentials' passphrase (else $ODB_BUNDLE_PASSPHRASE, else a prompt)
  --log-format json|text   stderr logs; text also prints progress as lines on a terminal
                           (default text when stderr is a terminal, else json)
  --progress-interval 5s   stdout progress events (0 = none); --quiet prints only the done event

The same commands without "run": odb preflight, odb resume (= odb run --resume), odb status,
odb verify. See odb help.

exit codes: 0 completed, 2 completed with errors, 3 failed, 4 preflight or configuration error,
            5 cancelled (odb resume continues; or run again with the same --state-dir)`

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// runRun implements `odb run` (OC-051).
func runRun(args []string, stdout, stderr io.Writer) int {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "", "preflight", "status", "verify", "target-path":
	case "help":
		fmt.Fprintln(stdout, runUsage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown run command %q\n\n%s\n", sub, runUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, runUsage) }
	var bundlePath string
	fs.StringVar(&bundlePath, "mappings", "", "mappings.json")
	fs.StringVar(&bundlePath, "bundle", "", "the same as --mappings (older exports)")
	stateDirFlag := fs.String("state-dir", "", "state directory")
	var maps stringList
	fs.Var(&maps, "mapping", "mapping name or ID (repeatable)")
	targetPath := fs.String("target-path", "", "directory target path")
	readers := fs.Int("readers", 0, "read partitions per table")
	writers := fs.Int("writers", 0, "concurrent writes")
	throttle := fs.Int("throttle", 0, "max documents per second")
	resume := fs.Bool("resume", false, "require a checkpoint")
	restart := fs.Bool("restart", false, "archive the state and start over")
	allowDrift := fs.Bool("allow-drift", false, "continue on compatible schema drift")
	configPath := fs.String("config", "", "run config file (config.json)")
	passFile := fs.String("passphrase-file", "", "file holding the passphrase")
	logFormat := fs.String("log-format", "", "json | text (default: text on a terminal, else json)")
	interval := fs.Duration("progress-interval", 5*time.Second, "progress event interval")
	quiet := fs.Bool("quiet", false, "only the done event")
	asJSON := fs.Bool("json", false, "preflight, verify: print JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *resume && *restart {
		fmt.Fprintln(stderr, "--resume and --restart can't be used together")
		return exitUsage
	}
	if *readers < 0 || *readers > 8 || *writers < 0 || *writers > 512 || *throttle < 0 {
		fmt.Fprintln(stderr, "--readers must be 1-8, --writers 1-512, --throttle at least 1")
		return exitUsage
	}
	// People at a terminal get text; pipelines and container logs keep JSON (OC-181).
	if *logFormat == "" {
		*logFormat = "json"
		if isTerminal(stderr) {
			*logFormat = "text"
		}
	}
	textOut := *logFormat == "text" && isTerminal(stdout)
	logger := observability.NewLogger(stderr, *logFormat, "info")
	// The files: flag, else ODB_*, else the export folder (OC-180).
	flagMappings, flagConfig := bundlePath, *configPath
	bundlePath, haveMappings := mappingsFile.resolve(flagMappings)
	*configPath, _ = configFile.resolve(flagConfig)
	if *targetPath == "" {
		*targetPath = os.Getenv("ODB_TARGET_PATH")
	}
	dir := headless.StateDir{Path: stateDir(*stateDirFlag, mappingsFile.exportDir(flagMappings, bundlePath))}
	if sub == "status" {
		return headless.Status(dir, stdout)
	}
	if sub == "target-path" {
		if *configPath == "" {
			fmt.Fprintln(stderr, configFile.missing())
			return exitUsage
		}
		return printTargetPath(*configPath, stdout, stderr)
	}
	if !haveMappings {
		fmt.Fprintln(stderr, mappingsFile.missing())
		return exitUsage
	}
	b, err := loadBundle(bundlePath)
	if err != nil {
		return reportBundleError(stderr, err)
	}
	var runConfig *export.RunConfig
	if *configPath != "" {
		if runConfig, err = headless.LoadRunConfig(*configPath, func(msg string) { logger.Info(msg) }); err != nil {
			printConfigError(stderr, asConfig(err))
			return exitPreflight
		}
	}

	// In a container, a directory target outside the mounted folder would vanish with it (OC-183).
	if msg := unmountedTarget(*configPath, *targetPath); msg != "" && (sub == "" || sub == "preflight" || sub == "verify") {
		printConfigError(stderr, &headless.ConfigError{Code: export.CodeRunConfigInvalid, Message: msg})
		return exitPreflight
	}
	hint := localhostHint(runConfig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drain, abort := make(chan struct{}), make(chan struct{})
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		n := 0
		for range sigs {
			n++
			if n == 1 {
				logger.Warn("stopping: finishing in-flight batches and saving the checkpoint (signal again to stop now)")
				close(drain)
				if sub != "" {
					cancel() // preflight and verify just stop
				}
				continue
			}
			logger.Warn("stopping now")
			close(abort)
			cancel()
			return
		}
	}()

	cfg := &headless.Config{
		Bundle: b, State: dir, Mappings: maps, TargetPath: *targetPath, Readers: *readers, Writers: *writers, Throttle: *throttle,
		Resume: *resume, Restart: *restart, AllowDrift: *allowDrift, ProgressInterval: *interval, Quiet: *quiet,
		Stdout: stdout, Log: logger, Build: buildinfo.Get(), Drain: drain, Abort: abort,
		Unlock: headless.Unlock{PassphraseFile: *passFile, Prompt: prompt(stderr)}, RunConfig: runConfig, RunConfigPath: *configPath,
		MappingsPath: absPath(bundlePath), RunConfigMissing: configFile.missing(), TextProgress: textOut,
	}
	switch sub {
	case "preflight":
		rep, code := headless.Preflight(ctx, cfg)
		printPreflight(stdout, rep, *asJSON, runConfig != nil)
		if rep.Error != nil {
			printConfigError(stderr, withConfigField(rep.Error, runConfig != nil))
		}
		if code != exitOK && hint != "" {
			fmt.Fprintln(stderr, "hint: "+hint)
		}
		return code
	case "verify":
		res, code, err := headless.Verify(ctx, cfg)
		if err != nil {
			printConfigError(stderr, asConfig(err))
			return exitPreflight
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(res)
			return code
		}
		for _, r := range res {
			mark := "OK  "
			if !r.OK {
				mark = "FAIL"
			}
			fmt.Fprintf(stdout, "%s %-20s %s: %d documents, expected %d", mark, r.Mapping, r.Target, r.Found, r.Expected)
			if r.Note != "" {
				fmt.Fprintf(stdout, " (%s)", r.Note)
			}
			fmt.Fprintln(stdout)
		}
		return code
	}
	_, code, err := headless.Run(ctx, cfg)
	if err != nil {
		ce := withConfigField(asConfig(err), runConfig != nil)
		printConfigError(stderr, ce)
		c := code
		done := headless.Event{TS: time.Now().UTC(), Event: headless.EventDone, Status: "failed", ExitCode: &c, Message: ce.Code + ": " + ce.Message}
		if textOut {
			fmt.Fprintln(stdout, done.Text())
		} else {
			_ = json.NewEncoder(stdout).Encode(done)
		}
	}
	if code == exitPreflight && hint != "" {
		fmt.Fprintln(stderr, "hint: "+hint)
	}
	if isTerminal(stderr) {
		fmt.Fprintln(stderr, "odb: "+exitSummary(code, dir.Path))
	}
	return code
}

// isTerminal reports whether w is a terminal (a variable for tests).
var isTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd())) //nolint:gosec // G115: a file descriptor fits in int
}

// exitSummary explains a run's exit code in a sentence, as migrate.sh did (OC-181).
func exitSummary(code int, state string) string {
	switch code {
	case 0:
		return "completed"
	case 2:
		return "completed with errors: see " + filepath.Join(state, "errors.jsonl") + " and report.json"
	case 3:
		return "failed: see " + filepath.Join(state, "report.json") + "; fix the cause, then run odb resume"
	case 4:
		return "not started: a preflight or configuration problem (above)"
	case 5:
		return "cancelled after saving the checkpoint: odb resume continues"
	}
	return fmt.Sprintf("odb exited with %d", code)
}

// runInspect is `odb inspect [FILE]`: what mappings.json (found as odb run finds it) migrates.
func runInspect(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, "usage: odb inspect [FILE]")
		return exitUsage
	}
	flagVal := ""
	if len(args) == 1 {
		flagVal = args[0]
	}
	p, ok := mappingsFile.resolve(flagVal)
	if !ok {
		fmt.Fprintln(stderr, mappingsFile.missing())
		return exitUsage
	}
	return runBundle([]string{"inspect", p}, stdout, stderr)
}

// absPath is p made absolute, for messages and the start event; p itself if that fails.
func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func asConfig(err error) *headless.ConfigError {
	var ce *headless.ConfigError
	if errors.As(err, &ce) {
		return ce
	}
	var be *export.Error
	if errors.As(err, &be) {
		return &headless.ConfigError{Code: be.Code, Message: be.Message, Details: be.Details}
	}
	return &headless.ConfigError{Code: headless.CodeConfig, Message: err.Error()}
}

func printConfigError(w io.Writer, ce *headless.ConfigError) {
	fmt.Fprintf(w, "%s: %s\n", ce.Code, ce.Message)
	for _, d := range ce.Details {
		fmt.Fprintf(w, "  - %s\n", d)
	}
	if ce.Hint != "" {
		fmt.Fprintf(w, "  → %s\n", ce.Hint)
	}
}

// withConfigField adds the config.json settings to check to a connection error's hint.
func withConfigField(ce *headless.ConfigError, haveConfig bool) *headless.ConfigError {
	if f := configField(ce.Code); haveConfig && f != "" {
		c := *ce
		c.Hint = strings.TrimSpace(c.Hint + " In config.json, check " + f + ".")
		return &c
	}
	return ce
}

// configField names the config.json settings behind a failed connection, by its problem code
// (SOURCE_DNS, TARGET_TIMEOUT, …), so the hint says where to make the fix (OC-190).
func configField(code string) string {
	role, kind, ok := strings.Cut(code, "_")
	if !ok || (role != "SOURCE" && role != "TARGET") {
		return ""
	}
	r := strings.ToLower(role)
	switch kind {
	case "DNS", "UNREACHABLE", "TIMEOUT":
		if role == "TARGET" {
			return "target.connectionString"
		}
		if kind == "DNS" {
			return "source.host"
		}
		return "source.host and source.port"
	case "AUTH_FAILED":
		return r + ".username and " + r + ".password"
	case "DB_NOT_FOUND":
		return "source.database"
	case "TLS":
		if role == "TARGET" {
			return "target.connectionString and target.caCertPem"
		}
		return "source.sslMode and source.caCertPem"
	}
	return ""
}

// checkMarks are the five-letter status column of odb preflight.
var checkMarks = map[string]string{"ok": "OK", "warn": "WARN", "fail": "FAIL", "skipped": "SKIP"}

func printPreflight(w io.Writer, rep headless.PreflightReport, asJSON, haveConfig bool) {
	if asJSON {
		_ = json.NewEncoder(w).Encode(rep)
		return
	}
	for _, d := range rep.Drift {
		fmt.Fprintf(w, "WARN  schema drift: %s\n", d.String())
	}
	for _, c := range rep.Checks {
		mark, ok := checkMarks[c.Status]
		if !ok {
			mark = strings.ToUpper(c.Status)
		}
		fmt.Fprintf(w, "%-5s %s", mark, c.Label)
		if c.Detail != "" {
			fmt.Fprintf(w, " — %s", c.Detail)
		}
		fmt.Fprintln(w)
		// What to do, under the line it's about (OC-190).
		if c.Status == "fail" || c.Status == "warn" {
			hint := c.Hint
			if f := configField(c.Code); haveConfig && f != "" && c.Status == "fail" {
				hint = strings.TrimSpace(hint + " In config.json, check " + f + ".")
			}
			if hint != "" {
				fmt.Fprintf(w, "      → %s\n", hint)
			}
		}
	}
	if rep.Error == nil {
		verdict := "ready to run"
		if !rep.OK {
			verdict = "NOT ready: fix the failed checks"
		}
		fmt.Fprintf(w, "%d mapping(s), about %d rows: %s\n", len(rep.Mappings), rep.TotalRows, verdict)
	}
}

// prompt asks for the passphrase without echo when stdin is a terminal; nil otherwise.
func prompt(stderr io.Writer) func() (string, error) {
	fd := int(os.Stdin.Fd()) //nolint:gosec // G115: a file descriptor fits in int
	if !term.IsTerminal(fd) {
		return nil
	}
	return func() (string, error) {
		fmt.Fprint(stderr, "Bundle passphrase: ")
		p, err := term.ReadPassword(fd)
		fmt.Fprintln(stderr)
		return string(p), err
	}
}

// printTargetPath prints the folder a directory target in the run config writes to, resolved like a
// run resolves it, so migrate.sh can mount it into docker (OC-159). Other targets print nothing.
func printTargetPath(configPath string, stdout, stderr io.Writer) int {
	if configPath == "" {
		fmt.Fprintln(stderr, "--config is required\n\n"+runUsage)
		return exitUsage
	}
	p, err := headless.DirectoryTargetPath(configPath)
	if err != nil {
		printConfigError(stderr, asConfig(err))
		return exitPreflight
	}
	if p != "" {
		fmt.Fprintln(stdout, p)
	}
	return exitOK
}
