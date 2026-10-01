package keyspaces

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// LocationService is the part of connections.Service the location endpoints use.
type LocationService interface {
	ListLocations(ctx context.Context, ws store.WorkspaceID, id, parentPath string) ([]target.LocationNode, connections.Target, error)
	CreateLocation(ctx context.Context, ws store.WorkspaceID, id string, loc store.Location) (connections.Target, error)
}

// LocationHandlers serves /connections/{id}/locations for every target kind (OC-036). They
// replace the Couchbase-only /buckets… endpoints for new clients; those stay as aliases.
type LocationHandlers struct {
	Svc   LocationService
	Audit store.Audit
}

// Routes implements httpapi.Registrar.
func (h LocationHandlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/connections/{id}/locations", h.list)
		r.With(auth.RequireRole(store.RoleEditor)).Post("/connections/{id}/locations", h.create)
	})
}

// LocationList is GET …/locations.
type LocationList struct {
	Kind   string                `json:"kind"`
	Parent string                `json:"parent"`
	Nodes  []target.LocationNode `json:"nodes"`
}

func (h LocationHandlers) list(w http.ResponseWriter, r *http.Request) {
	parent := r.URL.Query().Get("parent")
	nodes, t, err := h.Svc.ListLocations(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), parent)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, LocationList{Kind: t.Kind(), Parent: parent, Nodes: nodes})
}

// LocationCreated is POST …/locations: 201 ready, or 202 pending while the target makes it
// available.
type LocationCreated struct {
	Status   string         `json:"status"`
	Location store.Location `json:"location"`
}

func (h LocationHandlers) create(w http.ResponseWriter, r *http.Request) {
	var loc store.Location
	if err := httpapi.DecodeJSON(w, r, &loc); err != nil {
		problem.Write(w, r, err)
		return
	}
	t, err := h.Svc.CreateLocation(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), loc)
	loc.Kind = t.Kind()
	res := LocationCreated{Status: "ready", Location: loc}
	status := http.StatusCreated
	switch {
	case errors.Is(err, target.ErrPending):
		res.Status, status = "pending", http.StatusAccepted
	case err != nil:
		problem.Write(w, r, err)
		return
	}
	h.audit(r, t.Stored, res)
	httpapi.WriteJSON(w, status, res)
}

// audit records location.create (best effort).
func (h LocationHandlers) audit(r *http.Request, conn store.Connection, res LocationCreated) {
	if h.Audit == nil {
		return
	}
	meta, _ := json.Marshal(map[string]string{
		"projectId": conn.ProjectID, "kind": res.Location.Kind, "location": res.Location.String(), "status": res.Status,
	})
	p := principal(r)
	if err := h.Audit.Record(r.Context(), store.AuditEvent{
		WorkspaceID: p.WorkspaceID, UserID: p.UserID, Action: "location.create",
		EntityType: "connection", EntityID: conn.ID, Metadata: meta,
	}); err != nil {
		slog.WarnContext(r.Context(), "audit record failed", "action", "location.create", "error", err.Error())
	}
}
