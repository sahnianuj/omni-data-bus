package app

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"omnidatabus/internal/audit"
	"omnidatabus/internal/auth"
	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/config"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/connectors/catalog"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/couchbase"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/discovery"
	"omnidatabus/internal/documents"
	"omnidatabus/internal/events"
	"omnidatabus/internal/export"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/keyspaces"
	"omnidatabus/internal/mappings"
	"omnidatabus/internal/projects"
	"omnidatabus/internal/relations"
	"omnidatabus/internal/store"
)

// Deps are the collaborators feature handlers need. Later stories add fields here.
type Deps struct {
	Store     store.Store
	Sealer    crypto.Sealer
	Auth      *auth.Service
	Cookies   auth.CookieSettings
	PublicURL string
	// Bundled is the all-in-one image's Couchbase node, offered as a target (OC-072).
	Bundled connections.Bundled
	Sources *source.Registry
	Targets *target.Registry
	Ready   map[string]httpapi.Pinger
	// Hub fans job progress out to SSE subscribers on this node (created on demand).
	Hub *events.Hub
	// Now is the clock for rate limits (tests); nil = time.Now.
	Now func() time.Time
	// Export configures exported migrations (OC-050..OC-055); Version is this build's.
	Export  config.ExportConfig
	Version string
	// RediscoverBefore makes reading a schema saved before it rescan the source first,
	// once per connection (OC_REDISCOVER_ON_START); zero = never.
	RediscoverBefore time.Time
}

// ExportScript is the script settings for exports: the configured image, download URL and
// release binaries, with this version's defaults.
func ExportScript(c config.ExportConfig, version string) export.ScriptConfig {
	sc := export.ScriptConfig{
		Image: c.Image, DownloadURL: c.DownloadURL, CosignIdentity: c.CosignIdentity, Build: buildinfo.Get().Build,
		Dist: export.Dist{Dir: c.DistDir, Version: version},
	}
	if sc.Image == "" {
		sc.Image = "ghcr.io/sahnianuj/omni-data-bus:" + version
	}
	if sc.DownloadURL == "" {
		sc.DownloadURL = "https://github.com/sahnianuj/omni-data-bus/releases/download/v" + version
	}
	// Any machine's odb, fetched once from the release and verified (OC-188).
	f := &export.Fetcher{SumsDir: c.SumsDir, CacheDir: c.CacheDir}
	if f.CacheDir == "" {
		f.CacheDir = filepath.Join(os.TempDir(), "odb-export-cache")
	}
	if c.Fetch {
		f.BaseURL = sc.DownloadURL
	}
	sc.Dist.Fetch = f
	return sc
}

func (d Deps) hub() *events.Hub {
	if d.Hub != nil {
		return d.Hub
	}
	return events.NewHub(d.Store.Jobs(), 0)
}

// Features returns every feature's route registrar.
func Features(d Deps) []httpapi.Registrar {
	if d.Auth == nil {
		return nil
	}
	guard := jobs.Guard{Store: d.Store}
	conns := connections.NewService(d.Store, d.Sealer, d.Sources, d.Targets, guard, nil).WithBundled(d.Bundled)
	disc := discovery.NewService(d.Store, conns.OpenSource, nil)
	disc.RefreshBefore = d.RediscoverBefore
	maps := mappings.NewService(d.Store, disc, conns.OpenSource, conns.OpenKeyspaces)
	maps.StatLocation = conns.StatLocation
	maps.CaseSensitive = conns.CaseSensitive
	pre := &jobs.Preflighter{
		Store: d.Store, Validate: maps.Validate, TestConn: conns.TestStored, OpenSource: conns.OpenSource,
		Target: conns.PreflightTarget, Latest: disc.Latest,
	}
	jobsSvc := jobs.NewService(d.Store, pre, nil)
	jobsSvc.PlanHash = maps.PlanHash
	version := d.Version
	if version == "" {
		version = buildinfo.Get().Version
	}
	exports := &export.Service{
		Builder: &export.Builder{
			Store: d.Store, Sealer: d.Sealer, Validate: maps.Validate, Latest: disc.Latest,
			IsBundled: conns.IsBundled, Version: version, Now: d.Now,
		},
		Store: d.Store, Audit: &audit.Recorder{Store: d.Store.Audit()}, Script: ExportScript(d.Export, version),
		Version: version, PlanHash: maps.PlanHash, Now: d.Now,
	}
	return []httpapi.Registrar{
		auth.Handlers{Svc: d.Auth, Cookies: d.Cookies},
		projects.Handlers{Svc: projects.NewService(d.Store, d.Sealer, guard)},
		catalog.Handlers{Sources: d.Sources, Targets: d.Targets},
		connections.Handlers{Svc: conns},
		discovery.Handlers{Svc: disc},
		keyspaces.Handlers{Open: conns.OpenKeyspaces, Audit: d.Store.Audit()},
		keyspaces.LocationHandlers{Svc: conns, Audit: d.Store.Audit()},
		mappings.Handlers{Svc: maps},
		relations.Handlers{Svc: relations.NewService(d.Store, disc, conns.OpenSource)},
		jobs.Handlers{Svc: jobsSvc, Audit: &audit.Recorder{Store: d.Store.Audit()}},
		events.Handlers{Store: d.Store, Hub: d.hub()},
		audit.Handlers{Store: d.Store.Audit()},
		documents.Handlers{Store: d.Store, Targets: conns},
		export.Handlers{Svc: exports},
	}
}

// APIMiddleware caps bodies, loads sessions, applies rate and concurrency limits (OC-033) and
// enforces CSRF on every /api/v1 feature route.
func APIMiddleware(d Deps) []func(http.Handler) http.Handler {
	if d.Auth == nil {
		return nil
	}
	mw := []func(http.Handler) http.Handler{httpapi.BodyLimit, auth.SessionMiddleware(d.Auth, d.Cookies)}
	mw = append(mw, limitMiddleware(d.Now)...)
	return append(mw, auth.CSRF(d.PublicURL))
}

// Registries returns the source and target connectors this build supports: PostgreSQL
// (OC-009), Couchbase (OC-012) and directories (OC-036/OC-038). directoryRoots are the areas a
// directory target may be in; nil means directory.DefaultRoots (the home folder and /data).
func Registries(directoryRoots []string, opts ...directory.Options) (*source.Registry, *target.Registry) {
	sources := source.NewRegistry()
	sources.Register(postgres.New())
	targets := target.NewRegistry()
	targets.Register(couchbase.New())
	targets.Register(directory.New(directoryRoots, opts...))
	return sources, targets
}
