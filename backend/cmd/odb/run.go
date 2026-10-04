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
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/export"
	"omnidatabus/internal/headless"
	"omnidatabus/internal/observability"
)

const runUsage = `usage: odb run --bundle FILE [options]          run a migration bundle without the tool
       odb run preflight --bundle FILE [options] connect, unlock, check drift and the target; no documents written
       odb run status --state-dir DIR            summarize a run's checkpoint or report
       odb run verify --bundle FILE [options]    count each mapping's documents in the target

options:
  --state-dir DIR          checkpoint, errors.jsonl and report.json (default ./odb-state)
  --mapping NAME           run only this mapping (repeatable)
  --target-path DIR        write a directory target here instead of the bundle's path
  --readers N --writers N  read partitions per table (1-8) and concurrent writes
  --throttle DOCS_PER_S    limit the write rate
  --resume | --restart     require a checkpoint to resume | archive the state and start over
  --allow-drift            continue when the source changed compatibly since the export
  --passphrase-file FILE   sealed credentials' passphrase (else $ODB_BUNDLE_PASSPHRASE, else a prompt)
  --log-format json|text   stderr logs (default json)
  --progress-interval 5s   stdout progress events (0 = none); --quiet prints only the done event

exit codes: 0 completed, 2 completed with errors, 3 failed, 4 preflight or configuration error,
            5 cancelled (resume by running again with the same --state-dir)`

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
	case "", "preflight", "status", "verify":
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
	bundlePath := fs.String("bundle", "", "the .odb.json bundle")
	stateDir := fs.String("state-dir", "./odb-state", "state directory")
	var maps stringList
	fs.Var(&maps, "mapping", "mapping name or ID (repeatable)")
	targetPath := fs.String("target-path", "", "directory target path")
	readers := fs.Int("readers", 0, "read partitions per table")
	writers := fs.Int("writers", 0, "concurrent writes")
	throttle := fs.Int("throttle", 0, "max documents per second")
	resume := fs.Bool("resume", false, "require a checkpoint")
	restart := fs.Bool("restart", false, "archive the state and start over")
	allowDrift := fs.Bool("allow-drift", false, "continue on compatible schema drift")
	passFile := fs.String("passphrase-file", "", "file holding the passphrase")
	logFormat := fs.String("log-format", "json", "json | text")
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
	logger := observability.NewLogger(stderr, *logFormat, "info")
	dir := headless.StateDir{Path: *stateDir}
	if sub == "status" {
		return headless.Status(dir, stdout)
	}
	if *bundlePath == "" {
		fmt.Fprintln(stderr, "--bundle is required\n\n"+runUsage)
		return exitUsage
	}
	b, err := loadBundle(*bundlePath)
	if err != nil {
		return reportBundleError(stderr, err)
	}

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
		Unlock: headless.Unlock{PassphraseFile: *passFile, Prompt: prompt(stderr)},
	}
	switch sub {
	case "preflight":
		rep, code := headless.Preflight(ctx, cfg)
		printPreflight(stdout, rep, *asJSON)
		if rep.Error != nil {
			printConfigError(stderr, rep.Error)
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
		ce := asConfig(err)
		printConfigError(stderr, ce)
		c := code
		_ = json.NewEncoder(stdout).Encode(headless.Event{
			TS: time.Now().UTC(), Event: headless.EventDone, Status: "failed", ExitCode: &c, Message: ce.Code + ": " + ce.Message,
		})
	}
	return code
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
}

func printPreflight(w io.Writer, rep headless.PreflightReport, asJSON bool) {
	if asJSON {
		_ = json.NewEncoder(w).Encode(rep)
		return
	}
	for _, d := range rep.Drift {
		fmt.Fprintf(w, "WARN  schema drift: %s\n", d.String())
	}
	for _, c := range rep.Checks {
		fmt.Fprintf(w, "%-5s %s", strings.ToUpper(c.Status), c.Label)
		if c.Detail != "" {
			fmt.Fprintf(w, " — %s", c.Detail)
		}
		fmt.Fprintln(w)
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
