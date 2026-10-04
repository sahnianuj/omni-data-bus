package memstore

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"omnidatabus/internal/store"
)

// Exports implements store.Store.
func (s *Store) Exports() store.Exports { return exports{s} }

type exports struct{ s *Store }

func cloneExport(e store.ExportRecord) store.ExportRecord {
	e.MappingIDs = append([]string(nil), e.MappingIDs...)
	ph := make(map[string]string, len(e.PlanHashes))
	for k, v := range e.PlanHashes {
		ph[k] = v
	}
	e.PlanHashes = ph
	return e
}

func (r exports) Create(_ context.Context, ws store.WorkspaceID, e store.ExportRecord) (store.ExportRecord, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.project(ws, e.ProjectID); err != nil {
		return store.ExportRecord{}, err
	}
	if e.ID == "" {
		e.ID = uuid.Must(uuid.NewV7()).String()
	}
	e.WorkspaceID = ws
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.clock.Now()
	}
	s.exports[e.ID] = cloneExport(e)
	return cloneExport(e), nil
}

func (r exports) List(_ context.Context, ws store.WorkspaceID, projectID string, page store.Page) ([]store.ExportRecord, string, error) {
	page = page.Normalize()
	cursorAt, cursorID, err := store.DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", err
	}
	r.s.mu.Lock()
	var all []store.ExportRecord
	for _, e := range r.s.exports {
		if e.WorkspaceID == ws && e.ProjectID == projectID && (page.Cursor == "" || store.Before(e.CreatedAt, e.ID, cursorAt, cursorID)) {
			all = append(all, cloneExport(e))
		}
	}
	r.s.mu.Unlock()
	sort.Slice(all, func(i, k int) bool { return store.Before(all[k].CreatedAt, all[k].ID, all[i].CreatedAt, all[i].ID) })
	if len(all) <= page.Limit {
		return all, "", nil
	}
	items := all[:page.Limit]
	last := items[len(items)-1]
	return items, store.EncodeCursor(last.CreatedAt, last.ID), nil
}

func (r exports) FindByBundle(_ context.Context, ws store.WorkspaceID, projectID, sha string) (store.ExportRecord, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, e := range r.s.exports {
		if e.WorkspaceID == ws && e.ProjectID == projectID && e.BundleSHA256 == sha {
			return cloneExport(e), nil
		}
	}
	return store.ExportRecord{}, store.ErrNotFound
}
