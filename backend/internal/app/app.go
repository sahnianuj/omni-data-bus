package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/config"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/netguard"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/related"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/cbstore"
	"omnidatabus/internal/store/provision"
)

// BuildInfo identifies the running binary (internal/buildinfo, OC-058).
type BuildInfo = buildinfo.Info

// NewHandler builds the HTTP handler with every feature wired in. /metrics is on it unless
// OC_METRICS_ADDR gives metrics their own listener (MetricsServer).
func NewHandler(cfg config.Config, logger *slog.Logger, build BuildInfo, deps Deps) http.Handler {
	var metrics http.Handler
	if cfg.Observe.MetricsAddr == "" {
		metrics = observability.MetricsHandler()
	}
	return httpapi.NewRouter(httpapi.Options{
		Metrics:    metrics,
		HSTS:       strings.HasPrefix(cfg.PublicURL, "https://"),
		Logger:     logger,
		Ready:      deps.Ready,
		SPA:        httpapi.EmbeddedSPA(),
		Version:    versionHandler(cfg, logger, build, deps),
		Features:   Features(deps),
		Middleware: APIMiddleware(deps),
	})
}

// versionHandler serves /api/v1/version from the node registry when a store is wired (OC-058).
func versionHandler(cfg config.Config, logger *slog.Logger, build BuildInfo, deps Deps) http.Handler {
	h := &httpapi.VersionHandler{
		Info: build, Expose: cfg.ExposeBuildInfo, Role: cfg.Role, Logger: logger, BundledStore: cfg.Bundled.Store,
		Authenticated: func(r *http.Request) bool { _, ok := auth.PrincipalFrom(r.Context()); return ok },
	}
	if deps.Store != nil {
		h.Nodes = deps.Store.Nodes()
		if sv, ok := deps.Store.(interface {
			SchemaVersion(ctx context.Context) (int, error)
		}); ok {
			h.Schema = sv.SchemaVersion
		}
	}
	return h
}

// MetaConfig converts the OC_META_* settings into cbstore's configuration.
func MetaConfig(cfg config.Config) cbstore.Config {
	return cbstore.Config{
		ConnStr: cfg.Meta.ConnStr, Username: cfg.Meta.Username, Password: cfg.Meta.Password,
		Bucket: cfg.Meta.Bucket, Scope: cfg.Meta.Scope, Capella: cfg.Meta.Capella, TLSSkipVerify: cfg.Meta.TLSSkipVerify,
		AuditRetention: time.Duration(cfg.Guard.AuditRetentionDays) * 24 * time.Hour,
	}
}

// MetaStore is the metadata store plus provisioning (implemented by *cbstore.Store).
type MetaStore interface {
	store.Store
	Provision(ctx context.Context) (provision.Result, error)
}

// Opener connects to the metadata store; tests substitute a fake.
type Opener func(ctx context.Context, cfg cbstore.Config) (MetaStore, error)

// CouchbaseOpener opens the real Couchbase metadata store.
func CouchbaseOpener(ctx context.Context, cfg cbstore.Config) (MetaStore, error) {
	return cbstore.Open(ctx, cfg, store.SystemClock{})
}

// OpenMetadata connects to the Couchbase metadata bucket and, when enabled, provisions it.
func OpenMetadata(ctx context.Context, cfg config.Config, logger *slog.Logger, open Opener) (MetaStore, error) {
	logger.Info("connecting to metadata store", "connstr", cfg.Meta.ConnStr, "bucket", cfg.Meta.Bucket, "scope", cfg.Meta.Scope)
	st, err := open(ctx, MetaConfig(cfg))
	if err != nil {
		return nil, err
	}
	if !cfg.Meta.AutoProvision {
		return st, nil
	}
	res, err := st.Provision(ctx)
	var needs *provision.NeedsAdminError
	if errors.As(err, &needs) {
		_ = st.Close()
		return nil, fmt.Errorf("%w\nRun `odb admin provision --print` for the full list", err)
	}
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("provision metadata store: %w", err)
	}
	logger.Info("metadata store ready", "schema_from", res.From, "schema_to", res.To, "applied", res.Applied)
	return st, nil
}

// MetricsServer serves only /metrics on OC_METRICS_ADDR; nil when metrics share the public port.
func MetricsServer(cfg config.Config) *Server {
	if cfg.Observe.MetricsAddr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", observability.MetricsHandler())
	return &Server{
		HTTP:            &http.Server{Addr: cfg.Observe.MetricsAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		ShutdownTimeout: 5 * time.Second,
	}
}

// PprofServer serves net/http/pprof on OC_PPROF_ADDR (benchmark profiling, OC-034); nil when off.
func PprofServer(cfg config.Config) *Server {
	if cfg.Observe.PprofAddr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	return &Server{
		// No write timeout: a CPU profile streams for as long as ?seconds= asks.
		HTTP:            &http.Server{Addr: cfg.Observe.PprofAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		ShutdownTimeout: 5 * time.Second,
	}
}

// Run wires every component and serves until ctx is cancelled.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger, build BuildInfo) error {
	return run(ctx, cfg, logger, build, CouchbaseOpener)
}

// memoryLimit is the soft memory limit to set: OC_MEMORY_LIMIT_MB, unless it's 0 or the
// runtime's own GOMEMLIMIT is set.
func memoryLimit(mb int, getenv func(string) string) (int64, bool) {
	if mb <= 0 || getenv("GOMEMLIMIT") != "" {
		return 0, false
	}
	return int64(mb) << 20, true
}

// run is Run with the metadata store's opener (tests substitute a fake).
func run(ctx context.Context, cfg config.Config, logger *slog.Logger, build BuildInfo, open Opener) error {
	keyring, err := crypto.ParseKeyring(cfg.MasterKey, cfg.MasterKeys, cfg.MasterKeyActive)
	if err != nil {
		return err
	}
	policy, err := cfg.Guard.EgressPolicy()
	if err != nil {
		return err
	}
	netguard.SetDefault(netguard.NewDialer(policy))
	mapping.SetMaxRelationDepth(cfg.Worker.MaxRelationDepth)
	related.SetMemoryLimitMB(cfg.Worker.RelationMemoryMB)
	if limit, ok := memoryLimit(cfg.Worker.MemoryLimitMB, os.Getenv); ok {
		debug.SetMemoryLimit(limit)
		logger.Info("memory limit", "mb", cfg.Worker.MemoryLimitMB)
	}
	logger.Info("egress policy", "allow_private_networks", policy.AllowPrivateNetworks, "allow", len(policy.Allow), "deny", len(policy.Deny))
	flush, err := observability.SetupTracing(ctx, observability.TracingConfig{
		Endpoint: cfg.Observe.OTLPEndpoint, SampleRatio: cfg.Observe.SampleRatio, Version: build.Version,
	})
	if err != nil {
		return fmt.Errorf("tracing: %w", err)
	}
	defer func() {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = flush(fctx)
	}()
	if cfg.Observe.OTLPEndpoint != "" {
		logger.Info("tracing enabled", "endpoint", cfg.Observe.OTLPEndpoint, "sample_ratio", cfg.Observe.SampleRatio)
	}
	meta, err := OpenMetadata(ctx, cfg, logger, open)
	if err != nil {
		return err
	}
	defer func() { _ = meta.Close() }()
	go observability.RefreshJobGauge(ctx, jobCounts(meta), 15*time.Second, logger)
	go Heartbeat(ctx, meta.Nodes(), NodeBeat(cfg, build), NodeHeartbeatInterval, logger)
	authSvc, err := auth.NewService(meta, auth.Argon2Hasher{Params: auth.DefaultArgon2}, store.SystemClock{}, auth.Settings{
		IdleTTL: cfg.Session.IdleTTL, AbsoluteTTL: cfg.Session.AbsoluteTTL, SignupEnabled: cfg.SignupEnabled,
	})
	if err != nil {
		return err
	}
	sources, targets := Registries(nil, directory.Options{Shared: DirectoryShared(cfg), ContainerOnly: cfg.Directory.ContainerOnly})
	sealer := crypto.NewLocalSealer(keyring)
	// Workers claim and run migration jobs (OC-023); API-only nodes don't.
	workerDone := make(chan struct{})
	if cfg.Role != "api" {
		w := NewWorker(cfg, meta, sealer, sources, targets, logger)
		go func() { defer close(workerDone); _ = w.Run(ctx) }()
		logger.Info("worker started", "id", w.ID(), "max_jobs", cfg.Worker.MaxJobs)
	} else {
		close(workerDone)
	}
	defer func() { <-workerDone }()
	return Serve(ctx, cfg, logger, build, Deps{
		Store:     meta,
		Sealer:    sealer,
		Auth:      authSvc,
		Cookies:   auth.CookieSettings{Secure: cfg.Session.CookieSecure, MaxAge: cfg.Session.AbsoluteTTL},
		PublicURL: cfg.PublicURL,
		Bundled: connections.Bundled{
			ConnStr: cfg.Bundled.Target, Username: cfg.Bundled.TargetUsername, Password: cfg.Bundled.TargetPassword, Buckets: cfg.Bundled.TargetBuckets,
		},
		Sources: sources,
		Targets: targets,
		Ready:   map[string]httpapi.Pinger{"metadata": meta},
		Export:  cfg.Export,
		Version: build.Version,

		RediscoverBefore: rediscoverBefore(cfg, logger),
	})
}

// rediscoverBefore is this start's time when OC_REDISCOVER_ON_START asks for saved scans to
// be refreshed, else zero.
func rediscoverBefore(cfg config.Config, logger *slog.Logger) time.Time {
	if !cfg.RediscoverOnStart {
		return time.Time{}
	}
	logger.Info("schemas saved before this start are rescanned when next opened (OC_REDISCOVER_ON_START)")
	return time.Now()
}

// Serve runs the HTTP server with already-constructed dependencies.
func Serve(ctx context.Context, cfg config.Config, logger *slog.Logger, build BuildInfo, deps Deps) error {
	srv := &Server{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           NewHandler(cfg, logger, build, deps),
			ReadHeaderTimeout: 10 * time.Second,
		},
		ShutdownTimeout: cfg.ShutdownTimeout,
	}
	if ms := MetricsServer(cfg); ms != nil {
		go func() {
			if err := ms.Run(ctx, nil); err != nil {
				logger.Error("metrics server stopped", "addr", cfg.Observe.MetricsAddr, "error", err.Error())
			}
		}()
		logger.Info("serving metrics", "addr", cfg.Observe.MetricsAddr)
	}
	if ps := PprofServer(cfg); ps != nil {
		go func() {
			if err := ps.Run(ctx, nil); err != nil {
				logger.Error("pprof server stopped", "addr", cfg.Observe.PprofAddr, "error", err.Error())
			}
		}()
		logger.Warn("serving pprof: for benchmarks only, never expose it", "addr", cfg.Observe.PprofAddr)
	}
	observability.BuildInfo.WithLabelValues(build.Version, build.Commit, build.Channel, build.GoVersion).Set(1)
	logger.Info("listening", "addr", cfg.HTTPAddr, "role", cfg.Role, "version", build.Version, "commit", build.Commit,
		"channel", build.Channel, "build_time", build.BuildTime, "go_version", build.GoVersion)
	return srv.Run(ctx, nil)
}

// jobCounts counts unfinished jobs by status for the oc_jobs gauge.
func jobCounts(st store.Store) observability.JobLister {
	return func(ctx context.Context, statuses []string, limit int) (map[string]int, error) {
		jobs, err := st.Jobs().ListByStatus(ctx, statuses, limit)
		if err != nil {
			return nil, err
		}
		out := map[string]int{}
		for _, j := range jobs {
			out[j.Status]++
		}
		return out, nil
	}
}

// DirectoryShared parses OC_DIRECTORY_SHARED ("<container path>=<computer path>" pairs) for the
// directory connector (OC-080); a pair without "=" shares a folder under the same path.
func DirectoryShared(cfg config.Config) []directory.Shared {
	var out []directory.Shared
	for _, p := range cfg.Directory.Shared {
		in, host, found := strings.Cut(strings.TrimSpace(p), "=")
		if in == "" {
			continue
		}
		if !found || host == "" {
			host = in
		}
		out = append(out, directory.Shared{Path: in, HostPath: host})
	}
	return out
}
