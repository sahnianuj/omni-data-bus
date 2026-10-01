package relations

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Handlers exposes the relationship graph and virtual relationships (OC-042).
type Handlers struct{ Svc *Service }

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/connections/{id}/relations", h.graph)
		r.Get("/projects/{id}/relationships", h.list)
		r.Get("/projects/{id}/relationships/{rid}", h.get)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(store.RoleEditor))
			r.Post("/projects/{id}/relationships", h.create)
			r.Post("/projects/{id}/relationships/detect", h.detect)
			r.Put("/projects/{id}/relationships/{rid}", h.update)
			r.Delete("/projects/{id}/relationships/{rid}", h.remove)
		})
	})
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

// graph serves ?root=schema.table&depth=4.
func (h Handlers) graph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sch, tbl, ok := strings.Cut(q.Get("root"), ".")
	if !ok || sch == "" || tbl == "" {
		problem.Write(w, r, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid root", "root must be schema.table, e.g. public.orders."))
		return
	}
	depth := DefaultDepth
	if v := q.Get("depth"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxDepth {
			problem.Write(w, r, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid depth", "depth must be 1 to 6."))
			return
		}
		depth = n
	}
	res, err := h.Svc.Graph(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), mapping.TableRef{Schema: sch, Name: tbl}, depth)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, res)
}

func (h Handlers) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.Svc.List(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func writeRel(w http.ResponseWriter, status int, rel store.Relationship) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(rel.Version, 10)+`"`)
	httpapi.WriteJSON(w, status, rel)
}

func (h Handlers) get(w http.ResponseWriter, r *http.Request) {
	rel, err := h.Svc.Get(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "rid"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeRel(w, http.StatusOK, rel)
}

func (h Handlers) create(w http.ResponseWriter, r *http.Request) {
	var in RelationshipInput
	if err := httpapi.DecodeJSON(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	rel, err := h.Svc.Create(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeRel(w, http.StatusCreated, rel)
}

func (h Handlers) detect(w http.ResponseWriter, r *http.Request) {
	var in RelationshipInput
	if err := httpapi.DecodeJSON(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	d, err := h.Svc.Detect(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, d)
}

func (h Handlers) update(w http.ResponseWriter, r *http.Request) {
	var version int64
	if v := strings.Trim(strings.TrimPrefix(strings.TrimSpace(r.Header.Get("If-Match")), "W/"), `"`); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			problem.Write(w, r, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid If-Match", "If-Match must be the relationship version."))
			return
		}
		version = n
	}
	var in RelationshipInput
	if err := httpapi.DecodeJSON(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	rel, err := h.Svc.Update(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "rid"), version, in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeRel(w, http.StatusOK, rel)
}

func (h Handlers) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.Delete(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "rid")); err != nil {
		problem.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
