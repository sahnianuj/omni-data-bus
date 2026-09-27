package app

import (
	"net/http"
	"time"

	"omnidatabus/internal/audit"
	"omnidatabus/internal/auth"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/couchbase"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/discovery"
	"omnidatabus/internal/events"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/keyspaces"
	"omnidatabus/internal/mappings"
	"omnidatabus/internal/projects"
	"omnidatabus/internal/store"
)

// Deps are the collaborators feature handlers need. Later stories add fields here.
type Deps struct {
	Store     store.Store
	Sealer    crypto.Sealer
	Auth      *auth.Service
	Cookies   auth.CookieSettings
	PublicURL string
	Sources   *source.Registry
	Targets   *target.Registry
	Ready     map[string]httpapi.Pinger
	// Hub fans job progress out to SSE subscribers on this node (created on demand).
	Hub *events.Hub
	// Now is the clock for rate limits (tests); nil = time.Now.
	Now func() time.Time
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
	conns := connections.NewService(d.Store, d.Sealer, d.Sources, d.Targets, guard, nil)
	disc := discovery.NewService(d.Store, conns.OpenSource, nil)
	maps := mappings.NewService(d.Store, disc, conns.OpenSource, conns.OpenKeyspaces)
	pre := &jobs.Preflighter{
		Store: d.Store, Validate: maps.Validate, TestConn: conns.TestStored, OpenSource: conns.OpenSource,
		OpenKeyspaces: conns.OpenKeyspaces, Latest: disc.Latest,
	}
	return []httpapi.Registrar{
		auth.Handlers{Svc: d.Auth, Cookies: d.Cookies},
		projects.Handlers{Svc: projects.NewService(d.Store, d.Sealer, guard)},
		connectors.CatalogHandlers{Sources: d.Sources, Targets: d.Targets},
		connections.Handlers{Svc: conns},
		discovery.Handlers{Svc: disc},
		keyspaces.Handlers{Open: conns.OpenKeyspaces, Audit: d.Store.Audit()},
		mappings.Handlers{Svc: maps},
		jobs.Handlers{Svc: jobs.NewService(d.Store, pre, nil), Audit: &audit.Recorder{Store: d.Store.Audit()}},
		events.Handlers{Store: d.Store, Hub: d.hub()},
		audit.Handlers{Store: d.Store.Audit()},
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

// Registries returns the source and target connectors this build supports.
// PostgreSQL (OC-009) and Couchbase (OC-012) register here.
func Registries() (*source.Registry, *target.Registry) {
	sources := source.NewRegistry()
	sources.Register(postgres.New())
	targets := target.NewRegistry()
	targets.Register(couchbase.New())
	return sources, targets
}
