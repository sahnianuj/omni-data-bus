// Package catalog serves the connector catalog. It lives apart from internal/connectors so the
// connectors, and the pipeline that uses them, don't depend on the HTTP API (OC-051).
package catalog

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/httpapi"
)

// Source lists connector descriptors (implemented by both registries).
type Source interface {
	Descriptors() []connectors.Descriptor
}

// Handlers serves GET /connectors for the database picker (OC-008 AC2).
type Handlers struct {
	Sources Source
	Targets Source
}

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.With(auth.RequireAuth).Get("/connectors", func(w http.ResponseWriter, _ *http.Request) {
		httpapi.WriteJSON(w, http.StatusOK, map[string][]connectors.Descriptor{
			"sources": h.Sources.Descriptors(),
			"targets": h.Targets.Descriptors(),
		})
	})
}
