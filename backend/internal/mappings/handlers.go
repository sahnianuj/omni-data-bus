package mappings

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/automap"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Handlers exposes mappings and the design import endpoints.
type Handlers struct{ Svc *Service }

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/projects/{id}/mappings", h.list)
		r.Get("/projects/{id}/mappings/{mid}", h.get)
		r.Post("/projects/{id}/mappings/{mid}/validate", h.validate)
		r.Post("/projects/{id}/mappings/{mid}/preview", h.preview)
		r.Post("/doc-schemas/infer", h.infer)
		r.Post("/doc-schemas/export", h.export)
		r.Post("/connections/{id}/buckets/{bucket}/scopes/{scope}/collections/{collection}/infer-schema", h.inferFromCollection)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(store.RoleEditor))
			r.Post("/projects/{id}/mappings", h.create)
			r.Post("/projects/{id}/mappings:bulk", h.bulk)
			r.Put("/projects/{id}/mappings/{mid}", h.update)
			r.Delete("/projects/{id}/mappings/{mid}", h.remove)
			r.Post("/projects/{id}/mappings/{mid}/automap", h.automap)
			r.Post("/projects/{id}/mappings/{mid}/design", h.design)
			r.Post("/projects/{id}/mappings/{mid}/embeds", h.addEmbed)
		})
	})
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

// ifMatch reads If-Match ("3" or 3); 0 when absent.
func ifMatch(r *http.Request) (int64, error) {
	v := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	v = strings.TrimPrefix(v, "W/")
	v = strings.Trim(v, `"`)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		return 0, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid If-Match", "If-Match must be the mapping version, e.g. \"3\".")
	}
	return n, nil
}

func writeView(w http.ResponseWriter, status int, v View) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(v.Version, 10)+`"`)
	httpapi.WriteJSON(w, status, v)
}

func (h Handlers) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.Svc.List(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h Handlers) get(w http.ResponseWriter, r *http.Request) {
	v, err := h.Svc.Get(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "mid"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeView(w, http.StatusOK, v)
}

func (h Handlers) create(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := httpapi.DecodeJSON(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	v, err := h.Svc.Create(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeView(w, http.StatusCreated, v)
}

type bulkRequest struct {
	Items []CreateInput `json:"items" validate:"required,min=1,max=200,dive"`
}

func (h Handlers) bulk(w http.ResponseWriter, r *http.Request) {
	var in bulkRequest
	if err := httpapi.DecodeJSON(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	out, err := h.Svc.BulkCreate(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), in.Items)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"results": out})
}

func (h Handlers) update(w http.ResponseWriter, r *http.Request) {
	version, err := ifMatch(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	var in UpdateInput
	if err := httpapi.DecodeJSON(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	v, err := h.Svc.Update(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "mid"), version, in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeView(w, http.StatusOK, v)
}

func (h Handlers) remove(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := h.Svc.Delete(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "mid")); err != nil {
		problem.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h Handlers) automap(w http.ResponseWriter, r *http.Request) {
	version, err := ifMatch(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	var in AutomapInput
	if r.ContentLength != 0 {
		if err := httpapi.DecodeJSON(w, r, &in); err != nil {
			problem.Write(w, r, err)
			return
		}
	}
	p := principal(r)
	v, err := h.Svc.Automap(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "mid"), version, in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeView(w, http.StatusOK, v)
}

func (h Handlers) addEmbed(w http.ResponseWriter, r *http.Request) {
	version, err := ifMatch(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	var in automap.EmbedInput
	if err := httpapi.DecodeJSON(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	v, err := h.Svc.AddEmbed(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "mid"), version, in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeView(w, http.StatusOK, v)
}

func (h Handlers) design(w http.ResponseWriter, r *http.Request) {
	version, err := ifMatch(r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	var in DesignInput
	if err := httpapi.DecodeJSON(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	res, err := h.Svc.ReplaceDesign(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "mid"), version, in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(res.Mapping.Version, 10)+`"`)
	httpapi.WriteJSON(w, http.StatusOK, res)
}

func (h Handlers) validate(w http.ResponseWriter, r *http.Request) {
	res, err := h.Svc.Validate(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "mid"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, res)
}

func (h Handlers) preview(w http.ResponseWriter, r *http.Request) {
	var in PreviewInput
	if r.ContentLength != 0 {
		if err := httpapi.DecodeJSON(w, r, &in); err != nil {
			problem.Write(w, r, err)
			return
		}
	}
	res, err := h.Svc.Preview(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "mid"), in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, res)
}

// InferResult is a design derived from a sample, a JSON Schema or existing documents.
type InferResult struct {
	DocSchema docschema.Schema `json:"docSchema"`
	Warnings  []string         `json:"warnings"`
	Method    string           `json:"method,omitempty"`
}

type inferRequest struct {
	Kind    string `json:"kind" validate:"required,oneof=json-sample json-schema"`
	Content string `json:"content" validate:"required"`
}

func (h Handlers) infer(w http.ResponseWriter, r *http.Request) {
	var in inferRequest
	if err := decodeLarge(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	var (
		s        docschema.Schema
		warnings []string
		err      error
	)
	if in.Kind == "json-schema" {
		s, warnings, err = docschema.FromJSONSchema([]byte(in.Content))
	} else {
		var docs []json.RawMessage
		if docs, err = docschema.ParseSamples([]byte(in.Content)); err == nil {
			s, warnings, err = docschema.InferFromSamples(docs)
		}
	}
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, InferResult{DocSchema: s, Warnings: warnings})
}

// decodeLarge decodes a body of up to 6 MB (samples are limited to 5 MB of content).
func decodeLarge(w http.ResponseWriter, r *http.Request, dst any) error {
	return httpapi.DecodeJSONMax(w, r, dst, httpapi.LargeBodyLimit)
}

type exportRequest struct {
	DocSchema docschema.Schema `json:"docSchema"`
}

func (h Handlers) export(w http.ResponseWriter, r *http.Request) {
	var in exportRequest
	if err := decodeLarge(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	out, err := docschema.ToJSONSchema(&in.DocSchema)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/schema+json")
	w.Header().Set("Content-Disposition", `attachment; filename="document.schema.json"`)
	_, _ = w.Write(out)
}

func (h Handlers) inferFromCollection(w http.ResponseWriter, r *http.Request) {
	ks, _, err := h.Svc.openKeyspaces(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	docs, method, err := ks.SampleDocuments(r.Context(), httpapi.URLParam(r, "bucket"), httpapi.URLParam(r, "scope"), httpapi.URLParam(r, "collection"), 50)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	if len(docs) == 0 {
		problem.Write(w, r, problem.New(http.StatusUnprocessableEntity, problem.CodeInvalidSample, "No documents",
			"The collection has no documents to sample."))
		return
	}
	s, warnings, err := docschema.InferFromSamples(docs)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	s.Source = docschema.SourceCollectionSample
	httpapi.WriteJSON(w, http.StatusOK, InferResult{DocSchema: s, Warnings: warnings, Method: method})
}
