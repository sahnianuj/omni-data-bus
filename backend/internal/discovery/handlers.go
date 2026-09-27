package discovery

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Handlers exposes schema discovery on a project's source repository.
type Handlers struct{ Svc *Service }

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/connections/{id}/schema", h.schema)
		r.Get("/connections/{id}/schema/{schema}/{table}", h.table)
		r.Get("/connections/{id}/discoveries/{did}", h.status)
		r.Get("/connections/{id}/tables/{schema}/{table}/preview", h.preview)
		r.With(auth.RequireRole(store.RoleEditor)).Post("/connections/{id}/discover", h.discover)
	})
}

type discoverRequest struct {
	Schemas []string `json:"schemas" validate:"omitempty,max=100,dive,min=1,max=63"`
	Exclude []string `json:"exclude" validate:"omitempty,max=100,dive,min=1,max=200"`
}

// SchemaResponse is the latest snapshot with its metadata. Schemas holds full tables, or
// table summaries when ?summary=true.
type SchemaResponse struct {
	SnapshotID    string    `json:"snapshotId"`
	CapturedAt    time.Time `json:"capturedAt"`
	DurationMs    int64     `json:"durationMs"`
	TableCount    int       `json:"tableCount"`
	Database      string    `json:"database"`
	ServerVersion string    `json:"serverVersion"`
	Schemas       any       `json:"schemas"`
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func (h Handlers) discover(w http.ResponseWriter, r *http.Request) {
	var req discoverRequest
	if r.ContentLength != 0 { // the body (filters) is optional
		if err := httpapi.DecodeJSON(w, r, &req); err != nil {
			problem.Write(w, r, err)
			return
		}
	}
	p := principal(r)
	res, err := h.Svc.Discover(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"),
		source.DiscoverOptions{Schemas: req.Schemas, ExcludePatterns: req.Exclude})
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	status := http.StatusOK
	if res.Async {
		status = http.StatusAccepted
	}
	httpapi.WriteJSON(w, status, res.Status)
}

func (h Handlers) status(w http.ResponseWriter, r *http.Request) {
	st, err := h.Svc.Status(principal(r).WorkspaceID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "did"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, st)
}

// etag is the snapshot ID (a snapshot never changes), qualified by representation.
func etag(snapshotID, variant string) string { return `"` + snapshotID + variant + `"` }

func notModified(w http.ResponseWriter, r *http.Request, tag string) bool {
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

func (h Handlers) schema(w http.ResponseWriter, r *http.Request) {
	rec, snap, err := h.Svc.Latest(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	summary := r.URL.Query().Get("summary") == "true"
	variant := ""
	if summary {
		variant = ";summary"
	}
	if notModified(w, r, etag(rec.ID, variant)) {
		return
	}
	resp := SchemaResponse{
		SnapshotID: rec.ID, CapturedAt: rec.CapturedAt, DurationMs: rec.DurationMs, TableCount: rec.TableCount,
		Database: snap.Database, ServerVersion: snap.ServerVersion, Schemas: snap.Schemas,
	}
	if summary {
		resp.Schemas = snap.Summarize().Schemas
	}
	httpapi.WriteJSON(w, http.StatusOK, resp)
}

func (h Handlers) table(w http.ResponseWriter, r *http.Request) {
	rec, snap, err := h.Svc.Latest(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	schemaName, tableName := httpapi.URLParam(r, "schema"), httpapi.URLParam(r, "table")
	t := snap.Table(schemaName, tableName)
	if t == nil {
		problem.Write(w, r, tableNotFound(schemaName, tableName))
		return
	}
	if notModified(w, r, etag(rec.ID, ";"+schemaName+"."+tableName)) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, t)
}

func (h Handlers) preview(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid request", "limit must be a positive number.")
			p.FieldErrors = []problem.FieldError{{Field: "limit", Message: "must be a positive number"}}
			problem.Write(w, r, p)
			return
		}
		limit = n
	}
	res, err := h.Svc.Preview(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"),
		httpapi.URLParam(r, "schema"), httpapi.URLParam(r, "table"), r.URL.Query().Get("orderBy"), limit)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, res)
}
