package projects

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connections"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// RepoSummary is a repository as shown on project cards (no secrets).
type RepoSummary struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	Name           string     `json:"name"`
	LastTestStatus string     `json:"lastTestStatus"`
	LastTestedAt   *time.Time `json:"lastTestedAt,omitempty"`
}

// DTO is a project in API responses.
type DTO struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Description   string       `json:"description"`
	Status        Status       `json:"status"`
	Version       int64        `json:"version"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
	Source        *RepoSummary `json:"source"`
	Target        *RepoSummary `json:"target"`
	Mappings      int          `json:"mappings"`
	ReadyMappings int          `json:"readyMappings"`
	LatestJob     *JobRef      `json:"latestJob"`
}

// JobRef is the latest job on a project card.
type JobRef struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func repo(c *store.Connection) *RepoSummary {
	if c == nil {
		return nil
	}
	status := c.LastTestStatus
	if status == "" {
		status = "untested"
	}
	return &RepoSummary{ID: c.ID, Type: c.Type, Name: c.Name, LastTestStatus: status, LastTestedAt: c.LastTestedAt}
}

// ToDTO converts a summary for the API.
func ToDTO(s Summary) DTO {
	p := s.Project
	d := DTO{
		ID: p.ID, Name: p.Name, Description: p.Description, Status: s.Status, Version: p.Version,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Source: repo(s.Source), Target: repo(s.Target),
		Mappings: s.Mappings, ReadyMappings: s.ReadyMappings,
	}
	if s.LatestJob != nil {
		d.LatestJob = &JobRef{ID: s.LatestJob.ID, Status: s.LatestJob.Status}
	}
	return d
}

// Handlers exposes /projects and /dashboard.
type Handlers struct{ Svc *Service }

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/dashboard", h.dashboard)
		r.Get("/projects", h.list)
		r.Get("/projects/{id}", h.get)
		r.Get("/projects/{id}/connections", h.connections)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(store.RoleEditor))
			r.Post("/projects", h.create)
			r.Patch("/projects/{id}", h.update)
			r.Delete("/projects/{id}", h.remove)
		})
	})
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

type createRequest struct {
	Name        string `json:"name" validate:"required,min=1,max=100"`
	Description string `json:"description" validate:"max=500"`
}

type updateRequest struct {
	Name        *string `json:"name" validate:"omitempty,min=1,max=100"`
	Description *string `json:"description" validate:"omitempty,max=500"`
	Version     int64   `json:"version" validate:"required,min=1"`
}

func (h Handlers) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	sum, err := h.Svc.Create(r.Context(), p.WorkspaceID, p.UserID, req.Name, req.Description)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, ToDTO(sum))
}

func (h Handlers) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := h.Svc.List(r.Context(), principal(r).WorkspaceID, store.Page{Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	out := make([]DTO, 0, len(items))
	for _, s := range items {
		out = append(out, ToDTO(s))
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "nextCursor": next})
}

func (h Handlers) get(w http.ResponseWriter, r *http.Request) {
	sum, err := h.Svc.Get(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, ToDTO(sum))
}

func (h Handlers) update(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	sum, err := h.Svc.Update(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), req.Name, req.Description, req.Version)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, ToDTO(sum))
}

func (h Handlers) remove(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := h.Svc.Delete(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id")); err != nil {
		problem.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h Handlers) connections(w http.ResponseWriter, r *http.Request) {
	sum, err := h.Svc.Get(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	out := map[string]*connections.DTO{"source": nil, "target": nil}
	if sum.Source != nil {
		d := connections.ToDTO(*sum.Source)
		out["source"] = &d
	}
	if sum.Target != nil {
		d := connections.ToDTO(*sum.Target)
		out["target"] = &d
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (h Handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	stats, recent, err := h.Svc.Dashboard(r.Context(), principal(r).WorkspaceID)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	items := make([]DTO, 0, len(recent))
	for _, s := range recent {
		items = append(items, ToDTO(s))
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"stats": stats, "recent": items})
}
