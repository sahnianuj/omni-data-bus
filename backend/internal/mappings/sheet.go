package mappings

import (
	"context"
	"net/http"
	"strings"
	"time"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// MappingSheet is a mapping's fields with what fills each (OC-061), and a title.
func (s *Service) MappingSheet(ctx context.Context, ws store.WorkspaceID, projectID, id string) (string, []mapping.SheetRow, error) {
	m, spec, p, t, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return "", nil, err
	}
	d := driving(m, &spec)
	return d.String(), mapping.Sheet(&spec, d, issues(&spec, p.vctx(t, m.Where()))), nil
}

func (h Handlers) mappingSheet(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "md" {
		problem.Write(w, r, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Unknown format", "format is csv or md."))
		return
	}
	title, rows, err := h.Svc.MappingSheet(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "mid"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	name := strings.NewReplacer(".", "-", " ", "-", "/", "-", `"`, "").Replace(title) + "-mapping-sheet-" + time.Now().UTC().Format("2006-01-02") + "." + format
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_ = mapping.WriteSheetCSV(w, rows)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_ = mapping.WriteSheetMarkdown(w, title, rows)
}

type reconcileRequest struct {
	Current  docschema.Schema `json:"current"`
	Incoming docschema.Schema `json:"incoming"`
	Declined []string         `json:"declined,omitempty"`
}

// ReconcileResult is the revised design with the current IDs kept, and what changed.
type ReconcileResult struct {
	DocSchema docschema.Schema `json:"docSchema"`
	Diff      docschema.Diff   `json:"diff"`
}

// reconcile is the stateless form of the reconcile strategy (OC-061): the composer shows the
// diff, lets the user decline renames and moves, and applies the result as an undoable edit.
func (h Handlers) reconcile(w http.ResponseWriter, r *http.Request) {
	var in reconcileRequest
	if err := decodeLarge(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	out, diff := docschema.Reconcile(&in.Current, &in.Incoming, in.Declined)
	if bad := docschema.Validate(&out); len(bad) > 0 {
		problem.Write(w, r, docschema.Problem(bad))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, ReconcileResult{DocSchema: out, Diff: diff})
}
