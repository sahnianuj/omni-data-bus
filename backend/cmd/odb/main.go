// Command odb (short for omni-data-bus) runs the Omni-Data-Bus server (API + worker) and admin subcommands.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"omnidatabus/internal/app"
	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/config"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/cbstore"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("odb", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the build information and exit")
	role := fs.String("role", "", "override OC_ROLE: all | api | worker")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprint(stdout, buildinfo.Get().Text())
		return 0
	}
	// Commands that don't use the server's configuration never read a .env: `odb run` runs on
	// customer machines and in containers, where a .env in the working directory isn't ours.
	if fs.NArg() > 0 {
		switch fs.Arg(0) {
		case "run":
			return runRun(fs.Args()[1:], stdout, stderr)
		case "bundle":
			return runBundle(fs.Args()[1:], stdout, stderr)
		case "doc":
			return runDoc(fs.Args()[1:], stdout, stderr)
		case "version":
			return runVersion(fs.Args()[1:], stdout, stderr)
		}
	}
	envFile, err := config.LoadDotEnv(config.DotEnvCandidates()...)
	if err != nil {
		fmt.Fprintln(stderr, "reading .env:", err)
		return 2
	}
	if fs.NArg() > 0 {
		switch fs.Arg(0) {
		case "admin":
			return runAdmin(fs.Args()[1:], stdout, stderr)
		case "healthcheck":
			return runHealthcheck(fs.Args()[1:], stdout, stderr)
		default:
			fmt.Fprintf(stderr, "unknown command %q (commands: admin, bundle, doc, healthcheck, run, version; flags: --version, --role)\n", fs.Arg(0))
			return 2
		}
	}

	cfg, err := config.Load(nil)
	if err != nil {
		slog.New(slog.NewJSONHandler(stderr, nil)).Error("startup failed", "error", err.Error())
		return 2
	}
	if *role != "" {
		cfg.Role = *role
	}
	logger := observability.NewLogger(stdout, cfg.LogFormat, cfg.LogLevel)
	slog.SetDefault(logger)
	if envFile != "" {
		logger.Info("loaded configuration file", "path", envFile)
	}
	if cfg.Dev {
		logger.Warn("development mode (OC_DEV=true): using local dev-stack defaults; never use in production")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg, logger, buildinfo.Get()); err != nil {
		logger.Error("server stopped with error", "error", err.Error())
		return 1
	}
	logger.Info("server stopped")
	return 0
}

// runVersion prints the build information: the text block, or with --json the API's shape
// without the node list (OC-058).
func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	info := buildinfo.Get()
	if !*asJSON {
		fmt.Fprint(stdout, info.Text())
		return 0
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(info); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

const adminUsage = `usage: odb admin <command>

commands:
  gen-key               print a new random master key for OC_MASTER_KEY
  provision [--print]   create/upgrade the metadata scope, collections and indexes
                        (--print lists them for manual creation, e.g. in the Capella UI)
  migrate               alias of provision (the Helm pre-upgrade hook runs it)
  rotate-secrets        re-wrap every stored credential with the active master key
                        (set OC_MASTER_KEYS="new:<b64>,old:<b64>" and OC_MASTER_KEY_ACTIVE=new)`

func runAdmin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, adminUsage)
		return 2
	}
	switch args[0] {
	case "gen-key":
		return adminGenKey(stdout, stderr)
	case "provision", "migrate":
		return adminProvision(args[1:], stdout, stderr)
	case "rotate-secrets":
		return adminRotateSecrets(stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown admin command %q\n\n%s\n", args[0], adminUsage)
		return 2
	}
}

func adminProvision(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("provision", flag.ContinueOnError)
	fs.SetOutput(stderr)
	printOnly := fs.Bool("print", false, "print what provisioning creates instead of creating it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	logger := observability.NewLogger(stderr, "text", cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := cbstore.Open(ctx, app.MetaConfig(cfg), store.SystemClock{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = st.Close() }()
	if *printOnly {
		for _, item := range st.Plan() {
			fmt.Fprintln(stdout, item)
		}
		return 0
	}
	res, err := st.Provision(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	logger.Info("provisioned", "from", res.From, "to", res.To, "applied", res.Applied)
	return 0
}

func adminGenKey(stdout, stderr io.Writer) int {
	key := make([]byte, crypto.KeySize)
	if _, err := rand.Read(key); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(key))
	return 0
}

func adminRotateSecrets(stdout, stderr io.Writer) int {
	cfg, err := config.Load(nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	kr, err := crypto.ParseKeyring(cfg.MasterKey, cfg.MasterKeys, cfg.MasterKeyActive)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := cbstore.Open(ctx, app.MetaConfig(cfg), store.SystemClock{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = st.Close() }()
	report, err := crypto.Rotate(ctx, st.Connections(), crypto.NewLocalSealer(kr), 500)
	fmt.Fprintf(stdout, "re-wrapped %d secret(s) with key %q\n", report.Rewrapped, kr.Active())
	for _, f := range report.Failed {
		fmt.Fprintf(stdout, "  FAILED %s: %v\n", f.ConnectionID, f.Err)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(report.Failed) > 0 {
		return 1
	}
	return 0
}

// runHealthcheck probes the local server's readiness; the image has no shell or curl, so
// compose healthchecks run `odb healthcheck`. Exit 0 when ready, 1 otherwise.
func runHealthcheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "", "URL to probe (default: /readyz on OC_HTTP_ADDR)")
	timeout := fs.Duration("timeout", 3*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *url == "" {
		*url = "http://" + localAddr(os.Getenv("OC_HTTP_ADDR")) + "/readyz"
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "unhealthy:", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "unhealthy: %s %s\n", resp.Status, strings.TrimSpace(string(body)))
		return 1
	}
	fmt.Fprintln(stdout, strings.TrimSpace(string(body)))
	return 0
}

// localAddr turns a listen address (":8080", "0.0.0.0:8080", "") into one to dial locally.
func localAddr(listen string) string {
	if listen == "" {
		listen = ":8080"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
