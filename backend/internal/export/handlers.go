package export

import (
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Handlers exposes exports (OC-050, OC-052, OC-053) and external run reports (OC-055).
type Handlers struct{ Svc *Service }

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/exports/options", h.options)
		r.Get("/projects/{id}/exports", h.history)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(store.RoleEditor))
			r.Post("/projects/{id}/exports", h.export)
			r.Post("/projects/{id}/runs/import", h.importReport)
		})
	})
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func (h Handlers) options(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, h.Svc.Describe())
}

// export streams the export folder's zip as a download.
func (h Handlers) export(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httpapi.DecodeJSON(w, r, &in); err != nil {
		problem.Write(w, r, err) // unknown fields too: mappings, target and credentials are the project's
		return
	}
	art, rec, err := h.Svc.Export(r.Context(), principal(r), chi.URLParam(r, "id"), in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	w.Header().Set("Content-Type", art.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+art.FileName+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(art.Body)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Odb-Export-Id", rec.ID)
	w.Header().Set("X-Odb-Bundle-Sha256", rec.BundleSHA256)
	if art.IncludedOdb != "" {
		w.Header().Set("X-Odb-Included", art.IncludedOdb)
	}
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(art.Body)
}

type historyResponse struct {
	Items      []HistoryItem `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

func (h Handlers) history(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := h.Svc.History(r.Context(), principal(r).WorkspaceID, chi.URLParam(r, "id"),
		store.Page{Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, historyResponse{Items: items, NextCursor: next})
}

// importReport takes a report.json as a multipart "report" file (or a raw JSON body).
func (h Handlers) importReport(w http.ResponseWriter, r *http.Request) {
	var raw []byte
	if ct := r.Header.Get("Content-Type"); len(ct) >= 19 && ct[:19] == "multipart/form-data" {
		f, _, err := r.FormFile("report")
		if err != nil {
			problem.Write(w, r, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "No report", `Send report.json as the "report" file.`))
			return
		}
		defer func() { _ = f.Close() }()
		raw, err = io.ReadAll(io.LimitReader(f, MaxReportBytes+1))
		if err != nil {
			problem.Write(w, r, err)
			return
		}
	} else {
		var err error
		if raw, err = io.ReadAll(io.LimitReader(r.Body, MaxReportBytes+1)); err != nil {
			problem.Write(w, r, problem.New(http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge, "Report too large", "A report.json is at most 5 MB."))
			return
		}
	}
	if len(raw) > MaxReportBytes {
		problem.Write(w, r, problem.New(http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge, "Report too large", "A report.json is at most 5 MB."))
		return
	}
	j, err := h.Svc.ImportReport(r.Context(), principal(r), chi.URLParam(r, "id"), raw)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, j)
}
