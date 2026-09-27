package audit

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Handlers serve GET /audit to workspace owners.
type Handlers struct{ Store store.Audit }

// Page is one page of the audit log.
type Page struct {
	Items      []store.AuditEvent `json:"items"`
	NextCursor string             `json:"nextCursor"`
}

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.With(auth.RequireAuth, auth.RequireRole(store.RoleOwner)).Get("/audit", h.list)
}

func (h Handlers) list(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, next, err := h.Store.Search(r.Context(), p.WorkspaceID, store.AuditFilter{
		Action: q.Get("action"), EntityType: q.Get("entityType"), UserID: q.Get("userId"),
	}, store.Page{Limit: limit, Cursor: q.Get("cursor")})
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	if items == nil {
		items = []store.AuditEvent{}
	}
	httpapi.WriteJSON(w, http.StatusOK, Page{Items: items, NextCursor: next})
}
