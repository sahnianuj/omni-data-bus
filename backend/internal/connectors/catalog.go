package connectors

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
)

// CatalogSource lists connector descriptors (implemented by both registries).
type CatalogSource interface{ Descriptors() []Descriptor }

// CatalogHandlers serves GET /connectors for the database picker (OC-008 AC2).
type CatalogHandlers struct {
	Sources CatalogSource
	Targets CatalogSource
}

// Routes implements httpapi.Registrar.
func (h CatalogHandlers) Routes(r chi.Router) {
	r.With(auth.RequireAuth).Get("/connectors", func(w http.ResponseWriter, _ *http.Request) {
		httpapi.WriteJSON(w, http.StatusOK, map[string][]Descriptor{
			"sources": h.Sources.Descriptors(),
			"targets": h.Targets.Descriptors(),
		})
	})
}
