package httpapi

import (
	"io/fs"
	"log/slog"
	"net/http"
	"time"

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
	Logger     *slog.Logger
	Ready      map[string]Pinger
	SPA        fs.FS // nil when the web app is served by Vite (development)
	Version    string
	Commit     string
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
		api.Get("/version", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"version": o.Version, "commit": o.Commit})
		})
		api.Group(func(g chi.Router) {
			for _, m := range o.Middleware {
				g.Use(m)
			}
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
