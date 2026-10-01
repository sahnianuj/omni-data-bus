package jobs

import (
	"context"
	"net/http"

	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

func retryNotPossible(detail string) problem.Problem {
	return problem.New(http.StatusUnprocessableEntity, problem.CodeRetryNotPossible, "Can't retry just the failed rows", detail)
}

// Retry starts a new job that re-reads only the rows the job failed on, using each mapping's
// current spec, so a fixed transform applies (OC-026 AC6). It needs every failure stored (not
// truncated) and a primary key on each table, else 422 RETRY_NOT_POSSIBLE (run it again instead).
func (s *Service) Retry(ctx context.Context, ws store.WorkspaceID, userID, id string, onlyCodes []string) (View, PreflightResult, error) {
	j, err := s.store.Jobs().Get(ctx, ws, id)
	if err != nil {
		return View{}, PreflightResult{}, err
	}
	if j.Status != store.JobFailed && j.Status != store.JobCompletedWithErrors {
		return View{}, PreflightResult{}, retryNotPossible("Only a failed job, or one that completed with errors, has rows to retry.")
	}
	if j.ErrorsTruncated {
		return View{}, PreflightResult{}, retryNotPossible("More rows failed than were stored, so the failed rows aren't all known. Run the migration again instead.")
	}
	// The mappings with stored failures (optionally only some codes).
	want := map[string]bool{}
	for _, c := range onlyCodes {
		want[c] = true
	}
	seen := map[string]bool{}
	var mappingIDs []string
	cursor := ""
	for {
		page, next, err := s.store.Jobs().ListErrors(ctx, ws, id, store.JobErrorFilter{}, store.Page{Limit: 100, Cursor: cursor})
		if err != nil {
			return View{}, PreflightResult{}, err
		}
		for _, e := range page {
			if (len(want) == 0 || want[e.Code]) && !seen[e.MappingID] && len(e.SourceKey) > 0 {
				seen[e.MappingID] = true
				mappingIDs = append(mappingIDs, e.MappingID)
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(mappingIDs) == 0 {
		return View{}, PreflightResult{}, retryNotPossible("No stored failures match.")
	}
	// Every table needs a primary key to read rows back by key.
	if s.preflight != nil && s.preflight.Latest != nil {
		_, snap, err := s.preflight.Latest(ctx, ws, store.ConnectionID(j.ProjectID, store.RoleSource))
		if err == nil && snap != nil {
			for _, mid := range mappingIDs {
				m, err := s.store.Mappings().Get(ctx, ws, mid)
				if err != nil {
					return View{}, PreflightResult{}, retryNotPossible("A mapping of this job was deleted.")
				}
				srcSchema, srcTable := m.Driving()
				if t := snap.Table(srcSchema, srcTable); t == nil || len(t.PrimaryKey) == 0 {
					return View{}, PreflightResult{}, retryNotPossible(srcSchema + "." + srcTable + " has no primary key, so its rows can't be read back by key. Run the migration again instead.")
				}
			}
		}
	}
	opts, err := ParseOptions(j.Options)
	if err != nil {
		return View{}, PreflightResult{}, err
	}
	opts.MappingIDs, opts.RetryOf, opts.KeyFilter, opts.OnlyCodes = mappingIDs, id, "failed", onlyCodes
	v, pre, err := s.Create(ctx, ws, userID, j.ProjectID, opts)
	if err == nil {
		s.event(ctx, id, "retry", "Retry of the failed rows started as job "+v.ID)
	}
	return v, pre, err
}
