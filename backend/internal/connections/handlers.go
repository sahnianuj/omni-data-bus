package connections

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Handlers exposes the project-scoped connection endpoints.
type Handlers struct{ Svc *Service }

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/target-kinds", h.targetKinds)
		r.Get("/connections/{id}", h.get)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(store.RoleEditor))
			r.Post("/connections/test", h.testUnsaved)
			r.Get("/target-kinds/directory/folders", h.folders)
			r.Post("/target-kinds/directory/folders", h.makeFolder)
			r.Post("/connections/{id}/test", h.testStored)
			r.Put("/projects/{id}/connections/{role}", h.save)
			r.Delete("/projects/{id}/connections/{role}", h.remove)
		})
	})
}

type testRequest struct {
	Role    string          `json:"role" validate:"required,oneof=source target"`
	Type    string          `json:"type" validate:"required"`
	Config  json.RawMessage `json:"config" validate:"required"`
	Secrets SecretsPatch    `json:"secrets"`
}

type saveRequest struct {
	Type    string          `json:"type" validate:"required"`
	Name    string          `json:"name" validate:"max=100"`
	Config  json.RawMessage `json:"config" validate:"required"`
	Secrets SecretsPatch    `json:"secrets"`
	// Relocate moves the mappings when the target kind changes (OC-068).
	Relocate *Relocate `json:"relocate,omitempty"`
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func role(r *http.Request) (store.Role, error) {
	ro := store.Role(httpapi.URLParam(r, "role"))
	if !ro.Valid() {
		return "", problem.New(http.StatusNotFound, problem.CodeNotFound, "Not found", "Role must be source or target.")
	}
	return ro, nil
}

func (h Handlers) testUnsaved(w http.ResponseWriter, r *http.Request) {
	var req testRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	res, err := h.Svc.TestUnsaved(r.Context(), store.Role(req.Role), req.Type, req.Config, req.Secrets)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, res)
}

func (h Handlers) testStored(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	res, err := h.Svc.TestStored(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, res)
}

func (h Handlers) save(w http.ResponseWriter, r *http.Request) {
	ro, err := role(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	var req saveRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	conn, created, err := h.Svc.Save(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), ro,
		SaveInput(req))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpapi.WriteJSON(w, status, ToDTO(conn))
}

func (h Handlers) get(w http.ResponseWriter, r *http.Request) {
	conn, err := h.Svc.Get(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, ToDTO(conn))
}

func (h Handlers) remove(w http.ResponseWriter, r *http.Request) {
	ro, err := role(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	if err := h.Svc.Delete(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), ro); err != nil {
		problem.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// targetKinds lists the target kinds with their capabilities (OC-036).
func (h Handlers) targetKinds(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"kinds": h.Svc.TargetKinds()})
}

// folders serves the Target step's folder picker (OC-038).
func (h Handlers) folders(w http.ResponseWriter, r *http.Request) {
	res, err := h.Svc.DirectoryFolders(r.URL.Query().Get("path"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, res)
}

type makeFolderRequest struct {
	Parent string `json:"parent" validate:"required"`
	Name   string `json:"name" validate:"required"`
}

func (h Handlers) makeFolder(w http.ResponseWriter, r *http.Request) {
	var req makeFolderRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	path, err := h.Svc.MakeDirectoryFolder(r.Context(), p.WorkspaceID, p.UserID, req.Parent, req.Name)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]string{"path": path})
}
