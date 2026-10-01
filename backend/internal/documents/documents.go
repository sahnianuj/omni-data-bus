// Package documents serves migrated documents back from the target (OC-039, OC-041): one
// document by key for any target kind, and a directory's output listing for a job.
package documents

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Targets is the part of connections.Service used here.
type Targets interface {
	FindDocument(ctx context.Context, ws store.WorkspaceID, connID string, loc store.Location, key string) (target.Document, error)
	ListOutput(ctx context.Context, ws store.WorkspaceID, connID string, loc store.Location, mappingID, cursor string, limit int) ([]target.OutputItem, string, error)
}

// Handlers serves the document routes.
type Handlers struct {
	Store   store.Store
	Targets Targets
}

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		// The key is the rest of the path: keys may hold "/" and "::".
		r.Get("/projects/{id}/mappings/{mid}/documents/*", h.document)
		r.Get("/jobs/{jid}/output", h.output)
	})
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

var errNoMapping = problem.New(http.StatusNotFound, problem.CodeNotFound, "Not found", "No such mapping in this project.")

// DocumentView is GET …/documents/{key}.
type DocumentView struct {
	target.Document
	Location store.Location `json:"location"`
}

func (h Handlers) document(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	projectID, key := httpapi.URLParam(r, "id"), chi.URLParam(r, "*")
	m, err := h.Store.Mappings().Get(r.Context(), p.WorkspaceID, httpapi.URLParam(r, "mid"))
	if err == nil && m.ProjectID != projectID {
		err = errNoMapping
	}
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	if key == "" {
		problem.Write(w, r, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Missing key", "Put the document key at the end of the path."))
		return
	}
	doc, err := h.Targets.FindDocument(r.Context(), p.WorkspaceID, store.ConnectionID(projectID, store.RoleTarget), m.Where(), key)
	var rej *directory.PathRejected
	if errors.As(err, &rej) {
		h.audit(r, p, projectID, m.ID, key)
	}
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		name := key
		if doc.Path != "" {
			name = doc.Path[strings.LastIndex(doc.Path, "/")+1:]
			name = strings.TrimSuffix(name, ".gz")
		} else {
			name += ".json"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
		_, _ = w.Write(doc.Body)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, DocumentView{Document: doc, Location: m.Where()})
}

// OutputPage is GET /jobs/{jid}/output.
type OutputPage struct {
	MappingID  string              `json:"mappingId"`
	Location   store.Location      `json:"location"`
	Items      []target.OutputItem `json:"items"`
	NextCursor string              `json:"nextCursor"`
}

func (h Handlers) output(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	j, err := h.Store.Jobs().Get(r.Context(), p.WorkspaceID, httpapi.URLParam(r, "jid"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	mid := r.URL.Query().Get("mapping")
	if mid == "" && len(j.MappingIDs) > 0 {
		mid = j.MappingIDs[0]
	}
	inJob := false
	for _, id := range j.MappingIDs {
		inJob = inJob || id == mid
	}
	if !inJob {
		problem.Write(w, r, problem.New(http.StatusNotFound, problem.CodeNotFound, "Not found", "That mapping isn't part of this job."))
		return
	}
	m, err := h.Store.Mappings().Get(r.Context(), p.WorkspaceID, mid)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := h.Targets.ListOutput(r.Context(), p.WorkspaceID, store.ConnectionID(j.ProjectID, store.RoleTarget), m.Where(), m.ID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, OutputPage{MappingID: m.ID, Location: m.Where(), Items: items, NextCursor: next})
}

// audit records a rejected lookup (best effort).
func (h Handlers) audit(r *http.Request, p auth.Principal, projectID, mappingID, key string) {
	meta, _ := json.Marshal(map[string]string{"projectId": projectID, "mappingId": mappingID, "key": key})
	if err := h.Store.Audit().Record(r.Context(), store.AuditEvent{
		WorkspaceID: p.WorkspaceID, UserID: p.UserID, Action: "output.path_rejected", EntityType: "mapping", EntityID: mappingID, Metadata: meta,
	}); err != nil {
		slog.WarnContext(r.Context(), "audit record failed", "action", "output.path_rejected", "error", err.Error())
	}
}
