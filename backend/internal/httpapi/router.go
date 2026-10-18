package httpapi

import (
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/problem"

	"github.com/go-chi/chi/v5"
)

// Registrar lets a feature package add its routes under /api/v1.
type Registrar interface {
	Routes(r chi.Router)
}

// RegistrarFunc adapts a function to Registrar.
type RegistrarFunc func(r chi.Router)

// Routes implements Registrar.
func (f RegistrarFunc) Routes(r chi.Router) { f(r) }

// Options configures the router.
type Options struct {
	Logger *slog.Logger
	Ready  map[string]Pinger
	SPA    fs.FS // nil when the web app is served by Vite (development)
	// Version serves GET /api/v1/version (OC-058); it runs after Middleware so it can tell
	// signed-in callers from anonymous ones. Nil serves the anonymous view of buildinfo.Get().
	Version    http.Handler
	Features   []Registrar
	Middleware []func(http.Handler) http.Handler // applied to /api/v1 (auth, CSRF)
	// Metrics serves /metrics on this router; nil when it has its own listener (OC_METRICS_ADDR).
	Metrics http.Handler
	// HSTS adds Strict-Transport-Security (the public URL is https).
	HSTS bool
}

// NewRouter builds the full HTTP handler.
func NewRouter(o Options) chi.Router {
	r := chi.NewRouter()
	r.Use(observability.Tracing, RequestID(o.Logger), observability.HTTPMetrics, Recoverer, AccessLog, SecurityHeaders(o.HSTS), Timeout(60*time.Second))

	r.Get("/healthz", Healthz)
	r.Get("/readyz", Readyz(o.Ready))
	r.Get("/licenses.txt", Licenses) // OC-189
	if o.Metrics != nil {
		r.Method(http.MethodGet, "/metrics", o.Metrics)
	} else {
		r.Get("/metrics", http.NotFound) // served on OC_METRICS_ADDR; not the SPA's index.html
	}

	r.Route("/api/v1", func(api chi.Router) {
		api.NotFound(func(w http.ResponseWriter, req *http.Request) {
			problem.Write(w, req, problem.New(http.StatusNotFound, problem.CodeNotFound, "Not found", "No such API route."))
		})
		api.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
			problem.Write(w, req, problem.New(http.StatusMethodNotAllowed, problem.CodeBadRequest, "Method not allowed", ""))
		})
		api.Group(func(g chi.Router) {
			for _, m := range o.Middleware {
				g.Use(m)
			}
			version := o.Version
			if version == nil {
				version = &VersionHandler{Info: buildinfo.Get(), Expose: true}
			}
			g.Method(http.MethodGet, "/version", version)
			for _, f := range o.Features {
				f.Routes(g)
			}
		})
	})

	if o.SPA != nil {
		r.Handle("/*", SPA(o.SPA))
	}
	return r
}
