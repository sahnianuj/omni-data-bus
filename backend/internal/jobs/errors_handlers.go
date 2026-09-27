package jobs

import (
	"encoding/json"
	"net/http"
	"strconv"

	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

func (h Handlers) errors(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	q := r.URL.Query()
	n, _ := strconv.Atoi(q.Get("limit"))
	items, next, err := h.Svc.store.Jobs().ListErrors(r.Context(), p.WorkspaceID, httpapi.URLParam(r, "jid"),
		store.JobErrorFilter{Stage: q.Get("stage"), Code: q.Get("code"), MappingID: q.Get("mappingId")}, store.Page{Limit: n, Cursor: q.Get("cursor")})
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	if items == nil {
		items = []store.JobError{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}

func (h Handlers) errorSummary(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	id := httpapi.URLParam(r, "jid")
	j, err := h.Svc.store.Jobs().Get(r.Context(), p.WorkspaceID, id)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	items, err := h.Svc.store.Jobs().SummarizeErrors(r.Context(), p.WorkspaceID, id)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	if items == nil {
		items = []store.JobErrorCount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "truncated": j.ErrorsTruncated})
}

func (h Handlers) errorExport(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	id := httpapi.URLParam(r, "jid")
	if _, err := h.Svc.store.Jobs().Get(r.Context(), p.WorkspaceID, id); err != nil {
		problem.Write(w, r, err)
		return
	}
	ndjson := r.URL.Query().Get("format") == "ndjson"
	if ndjson {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", `attachment; filename="job-`+id+`-errors.ndjson"`)
		_ = h.Svc.ErrorPages(r.Context(), p.WorkspaceID, id, func(errs []store.JobError) error { return WriteNDJSON(w, errs) })
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="job-`+id+`-errors.csv"`)
	cw, err := NewCSVWriter(w)
	if err != nil {
		return
	}
	_ = h.Svc.ErrorPages(r.Context(), p.WorkspaceID, id, cw.Write)
}

func (h Handlers) retry(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OnlyCodes []string `json:"onlyCodes"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			problem.Write(w, r, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid request", err.Error()))
			return
		}
	}
	p := principal(r)
	id := httpapi.URLParam(r, "jid")
	v, pre, err := h.Svc.Retry(r.Context(), p.WorkspaceID, p.UserID, id, in.OnlyCodes)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	h.record(r, "job.retry", id, map[string]string{"newJobId": v.ID})
	writeJSON(w, http.StatusCreated, map[string]any{"job": v, "preflight": pre})
}
