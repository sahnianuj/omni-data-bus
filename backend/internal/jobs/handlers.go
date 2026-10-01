package jobs

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/audit"
	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Handlers exposes the jobs API (OC-023) and the error browser (OC-026).
type Handlers struct {
	Svc *Service
	// Audit records each job action and report download (OC-033); nil records nothing.
	Audit *audit.Recorder
}

// record audits one successful action on a job.
func (h Handlers) record(r *http.Request, action, jobID string, meta map[string]string) {
	p := principal(r)
	h.Audit.Record(r.Context(), p.WorkspaceID, p.UserID, action, "job", jobID, meta)
}

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/jobs", h.listAll)
		r.Get("/projects/{id}/jobs", h.listProject)
		r.Get("/jobs/{jid}", h.get)
		r.Get("/jobs/{jid}/events/log", h.log)
		r.Get("/jobs/{jid}/errors", h.errors)
		r.Get("/jobs/{jid}/errors/summary", h.errorSummary)
		r.Get("/jobs/{jid}/errors/export", h.errorExport)
		r.Get("/jobs/{jid}/report", h.report("json"))
		r.Get("/jobs/{jid}/report.csv", h.report("csv"))
		r.Get("/jobs/{jid}/report.html", h.report("html"))
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(store.RoleEditor))
			r.Post("/projects/{id}/jobs/preflight", h.preflight)
			r.Post("/projects/{id}/jobs", h.create)
			r.Post("/jobs/{jid}/pause", h.pause)
			r.Post("/jobs/{jid}/resume", h.resume)
			r.Post("/jobs/{jid}/cancel", h.cancel)
			r.Post("/jobs/{jid}/retry", h.retry)
		})
	})
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeOptions reads the request's options over the defaults (an empty body = defaults).
func decodeOptions(w http.ResponseWriter, r *http.Request) (Options, error) {
	if r.ContentLength == 0 {
		return DefaultOptions(), nil
	}
	// Decoded over the defaults: omitted fields keep them, unknown fields are rejected.
	o := DefaultOptions()
	if err := httpapi.DecodeJSON(w, r, &o); err != nil {
		return Options{}, err
	}
	return o, nil
}

func (h Handlers) preflight(w http.ResponseWriter, r *http.Request) {
	opts, err := decodeOptions(w, r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	res, err := h.Svc.Preflight(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), opts)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h Handlers) create(w http.ResponseWriter, r *http.Request) {
	opts, err := decodeOptions(w, r)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	p := principal(r)
	v, pre, err := h.Svc.Create(r.Context(), p.WorkspaceID, p.UserID, httpapi.URLParam(r, "id"), opts)
	if problem.HasCode(err, problem.CodePreflightFailed) || problem.HasCode(err, problem.CodeMappingInvalid) {
		// problem+json with the checklist attached, so the UI can show what failed.
		pr := problem.From(err)
		pr.Instance = problem.RequestIDFrom(r)
		w.Header().Set("Content-Type", problem.ContentType)
		w.WriteHeader(pr.Status)
		_ = json.NewEncoder(w).Encode(struct {
			problem.Problem
			Preflight PreflightResult `json:"preflight"`
		}{pr, pre})
		return
	}
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	h.record(r, "job.create", v.ID, map[string]string{"projectId": v.ProjectID})
	writeJSON(w, http.StatusCreated, map[string]any{"job": v, "preflight": pre})
}

func page(r *http.Request) store.Page {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return store.Page{Limit: n, Cursor: r.URL.Query().Get("cursor")}
}

func (h Handlers) list(w http.ResponseWriter, r *http.Request, projectID string) {
	p := principal(r)
	items, next, err := h.Svc.List(r.Context(), p.WorkspaceID, projectID, page(r))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	if items == nil {
		items = []View{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}

func (h Handlers) listAll(w http.ResponseWriter, r *http.Request) { h.list(w, r, "") }
func (h Handlers) listProject(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, httpapi.URLParam(r, "id"))
}

func (h Handlers) get(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	v, err := h.Svc.Get(r.Context(), p.WorkspaceID, httpapi.URLParam(r, "jid"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h Handlers) log(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	evs, err := h.Svc.Events(r.Context(), p.WorkspaceID, httpapi.URLParam(r, "jid"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	if evs == nil {
		evs = []store.JobEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": evs})
}

type control func(*Service, *http.Request, store.WorkspaceID, string, string) (View, error)

func (h Handlers) control(action string, meta map[string]string, fn control) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		id := httpapi.URLParam(r, "jid")
		v, err := fn(h.Svc, r, p.WorkspaceID, p.UserID, id)
		if err != nil {
			problem.Write(w, r, err)
			return
		}
		if v.ID != id { // a restart replaced the job
			meta = map[string]string{"mode": ResumeRestart, "newJobId": v.ID}
		}
		h.record(r, action, id, meta)
		writeJSON(w, http.StatusOK, v)
	}
}

func (h Handlers) pause(w http.ResponseWriter, r *http.Request) {
	h.control("job.pause", nil, func(s *Service, r *http.Request, ws store.WorkspaceID, u, id string) (View, error) {
		return s.Pause(r.Context(), ws, u, id)
	})(w, r)
}

func (h Handlers) resume(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string `json:"mode"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			problem.Write(w, r, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid request", err.Error()))
			return
		}
	}
	h.control("job.resume", nil, func(s *Service, r *http.Request, ws store.WorkspaceID, u, id string) (View, error) {
		return s.Resume(r.Context(), ws, u, id, in.Mode)
	})(w, r)
}

func (h Handlers) cancel(w http.ResponseWriter, r *http.Request) {
	h.control("job.cancel", nil, func(s *Service, r *http.Request, ws store.WorkspaceID, u, id string) (View, error) {
		return s.Cancel(r.Context(), ws, u, id)
	})(w, r)
}

// report serves the job report as JSON, CSV (one row per table) or standalone HTML (OC-030).
// ?download=1 asks the browser to save it.
func (h Handlers) report(format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		id := httpapi.URLParam(r, "jid")
		rep, err := h.Svc.Report(r.Context(), p.WorkspaceID, id)
		if err != nil {
			problem.Write(w, r, err)
			return
		}
		var body []byte
		switch format {
		case "csv":
			body, err = rep.CSV()
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		case "html":
			body, err = rep.HTML()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		default:
			body, err = json.Marshal(rep)
			w.Header().Set("Content-Type", "application/json")
		}
		if err != nil {
			problem.Write(w, r, err)
			return
		}
		if r.URL.Query().Get("download") != "" {
			ext := map[string]string{"csv": "csv", "html": "html", "json": "json"}[format]
			w.Header().Set("Content-Disposition", `attachment; filename="job-`+id+`-report.`+ext+`"`)
			// Only downloads are audited; the monitor fetches the JSON report repeatedly.
			h.record(r, "report.download", id, map[string]string{"format": format})
		}
		_, _ = w.Write(body) //nolint:gosec // G705: JSON, CSV, or HTML escaped by html/template
	}
}
