// Package keyspaces exposes the target keyspace browser (OC-013) and keyspace creation
// (OC-014) on a project's stored target repository.
package keyspaces

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Opener opens the keyspace browser of a stored target (connections.Service.OpenKeyspaces).
type Opener func(ctx context.Context, ws store.WorkspaceID, connectionID string) (target.Keyspaces, store.Connection, error)

// Handlers serves /connections/{id}/buckets….
type Handlers struct {
	Open  Opener
	Audit store.Audit
}

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/connections/{id}/buckets", h.buckets)
		r.Get("/connections/{id}/buckets/{bucket}/scopes", h.scopes)
		r.Get("/connections/{id}/buckets/{bucket}/scopes/{scope}/collections/{collection}", h.collection)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(store.RoleEditor))
			r.Post("/connections/{id}/buckets", h.createBucket)
			r.Post("/connections/{id}/buckets/{bucket}/scopes", h.createScope)
			r.Post("/connections/{id}/buckets/{bucket}/scopes/{scope}/collections", h.createCollection)
		})
	})
}

// CreateResult is returned by the create endpoints: 201 ready, or 202 pending when the cluster
// hasn't finished making the keyspace available (poll GET …/collections/{c}).
type CreateResult struct {
	Status     string `json:"status"` // ready | pending
	Bucket     string `json:"bucket"`
	Scope      string `json:"scope,omitempty"`
	Collection string `json:"collection,omitempty"`
}

type nameRequest struct {
	Name string `json:"name" validate:"required"`
}

type collectionRequest struct {
	Name         string `json:"name" validate:"required"`
	MaxExpirySec int64  `json:"maxExpirySec"`
}

type bucketRequest struct {
	Name           string `json:"name" validate:"required"`
	RAMQuotaMB     uint64 `json:"ramQuotaMB" validate:"required"`
	BucketType     string `json:"bucketType" validate:"required"`
	NumReplicas    uint32 `json:"numReplicas"`
	StorageBackend string `json:"storageBackend"`
	FlushEnabled   bool   `json:"flushEnabled"`
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func (h Handlers) open(r *http.Request) (target.Keyspaces, store.Connection, error) {
	return h.Open(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"))
}

func (h Handlers) buckets(w http.ResponseWriter, r *http.Request) {
	ks, _, err := h.open(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	list, err := ks.ListBuckets(r.Context())
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h Handlers) scopes(w http.ResponseWriter, r *http.Request) {
	ks, _, err := h.open(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	scopes, err := ks.ListScopes(r.Context(), httpapi.URLParam(r, "bucket"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"scopes": scopes})
}

func (h Handlers) collection(w http.ResponseWriter, r *http.Request) {
	ks, _, err := h.open(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	c, err := ks.Collection(r.Context(), httpapi.URLParam(r, "bucket"), httpapi.URLParam(r, "scope"), httpapi.URLParam(r, "collection"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, c)
}

func (h Handlers) created(w http.ResponseWriter, r *http.Request, conn store.Connection, res CreateResult, err error) {
	switch {
	case errors.Is(err, target.ErrPending):
		res.Status = "pending"
		h.audit(r, conn, res)
		httpapi.WriteJSON(w, http.StatusAccepted, res)
	case err != nil:
		problem.Write(w, r, err)
	default:
		res.Status = "ready"
		h.audit(r, conn, res)
		httpapi.WriteJSON(w, http.StatusCreated, res)
	}
}

func (h Handlers) createBucket(w http.ResponseWriter, r *http.Request) {
	var req bucketRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	ks, conn, err := h.open(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	err = ks.CreateBucket(r.Context(), target.BucketSpec(req))
	h.created(w, r, conn, CreateResult{Bucket: req.Name}, err)
}

func (h Handlers) createScope(w http.ResponseWriter, r *http.Request) {
	var req nameRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	ks, conn, err := h.open(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	bucket := httpapi.URLParam(r, "bucket")
	err = ks.CreateScope(r.Context(), bucket, req.Name)
	h.created(w, r, conn, CreateResult{Bucket: bucket, Scope: req.Name}, err)
}

func (h Handlers) createCollection(w http.ResponseWriter, r *http.Request) {
	var req collectionRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	ks, conn, err := h.open(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	bucket, scope := httpapi.URLParam(r, "bucket"), httpapi.URLParam(r, "scope")
	err = ks.CreateCollection(r.Context(), bucket, scope, target.CollectionSpec{Name: req.Name, MaxExpirySec: req.MaxExpirySec})
	h.created(w, r, conn, CreateResult{Bucket: bucket, Scope: scope, Collection: req.Name}, err)
}

// audit records keyspace.create (best effort: a failed audit write never fails the request).
func (h Handlers) audit(r *http.Request, conn store.Connection, res CreateResult) {
	if h.Audit == nil {
		return
	}
	meta, _ := json.Marshal(map[string]string{
		"projectId": conn.ProjectID, "bucket": res.Bucket, "scope": res.Scope, "collection": res.Collection, "status": res.Status,
	})
	p := principal(r)
	if err := h.Audit.Record(r.Context(), store.AuditEvent{
		WorkspaceID: p.WorkspaceID, UserID: p.UserID, Action: "keyspace.create",
		EntityType: "connection", EntityID: conn.ID, Metadata: meta,
	}); err != nil {
		slog.WarnContext(r.Context(), "audit record failed", "action", "keyspace.create", "error", err.Error())
	}
}
