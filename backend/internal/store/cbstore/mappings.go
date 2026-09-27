package cbstore

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/couchbase/gocb/v2"
	"github.com/google/uuid"

	"omnidatabus/internal/store"
)

// Mappings are one document each (key = mapping ID) plus an index document per project
// ("mapidx::<projectId>") listing {id, schema, table}. The index is updated with CAS, makes
// listing KV-only and consistent right after a write, and enforces one mapping per source
// table. Specs can be large, so they're written outside transactions.

type mappingDoc struct {
	Type         string              `json:"type"`
	ID           string              `json:"id"`
	WorkspaceID  string              `json:"workspaceId"`
	ProjectID    string              `json:"projectId"`
	SourceSchema string              `json:"sourceSchema"`
	SourceTable  string              `json:"sourceTable"`
	Target       store.MappingTarget `json:"target"`
	Spec         json.RawMessage     `json:"spec"`
	AutomapStats json.RawMessage     `json:"automapStats,omitempty"`
	Status       string              `json:"status"`
	CreatedBy    string              `json:"createdBy"`
	CreatedAt    string              `json:"createdAt"`
	UpdatedAt    string              `json:"updatedAt"`
	Version      int64               `json:"version"`
}

func (d mappingDoc) toMapping() store.Mapping {
	return store.Mapping{
		ID: d.ID, WorkspaceID: store.WorkspaceID(d.WorkspaceID), ProjectID: d.ProjectID, SourceSchema: d.SourceSchema,
		SourceTable: d.SourceTable, Target: d.Target, Spec: d.Spec, AutomapStats: d.AutomapStats, Status: d.Status,
		CreatedBy: d.CreatedBy, CreatedAt: parseTS(d.CreatedAt), UpdatedAt: parseTS(d.UpdatedAt), Version: d.Version,
	}
}

type mapIndexEntry struct {
	ID     string `json:"id"`
	Schema string `json:"schema"`
	Table  string `json:"table"`
}

type mapIndex struct {
	Type        string          `json:"type"`
	WorkspaceID string          `json:"workspaceId"`
	ProjectID   string          `json:"projectId"`
	Entries     []mapIndexEntry `json:"entries"`
}

func mapIndexKey(projectID string) string { return "mapidx::" + projectID }

type mappings struct{ s *Store }

// Mappings implements store.Store.
func (s *Store) Mappings() store.Mappings { return mappings{s} }

// updateIndex applies fn to the project's index with CAS, retrying on concurrent changes.
func (r mappings) updateIndex(ctx context.Context, ws store.WorkspaceID, projectID string, fn func(*mapIndex) error) error {
	c := r.s.coll("mappings")
	key := mapIndexKey(projectID)
	for attempt := 0; attempt < 10; attempt++ {
		idx := mapIndex{Type: "mapping_index", WorkspaceID: string(ws), ProjectID: projectID, Entries: []mapIndexEntry{}}
		var cas gocb.Cas
		res, err := c.Get(key, &gocb.GetOptions{Context: ctx})
		switch {
		case errors.Is(err, gocb.ErrDocumentNotFound):
		case err != nil:
			return Translate(err, "")
		default:
			if err := res.Content(&idx); err != nil {
				return err
			}
			cas = res.Cas()
		}
		if err := fn(&idx); err != nil {
			return err
		}
		if cas == 0 {
			_, err = c.Insert(key, idx, &gocb.InsertOptions{Context: ctx})
		} else {
			_, err = c.Replace(key, idx, &gocb.ReplaceOptions{Cas: cas, Context: ctx})
		}
		if errors.Is(err, gocb.ErrCasMismatch) || errors.Is(err, gocb.ErrDocumentExists) {
			continue
		}
		return Translate(err, "")
	}
	return store.ErrVersionConflict
}

func (r mappings) Create(ctx context.Context, ws store.WorkspaceID, m store.Mapping) (store.Mapping, error) {
	if _, err := projects(r).Get(ctx, ws, m.ProjectID); err != nil {
		return store.Mapping{}, err
	}
	if m.ID == "" {
		m.ID = uuid.Must(uuid.NewV7()).String()
	}
	now := formatTS(r.s.clock.Now())
	doc := mappingDoc{
		Type: "mapping", ID: m.ID, WorkspaceID: string(ws), ProjectID: m.ProjectID, SourceSchema: m.SourceSchema,
		SourceTable: m.SourceTable, Target: m.Target, Spec: m.Spec, AutomapStats: m.AutomapStats, Status: m.Status,
		CreatedBy: m.CreatedBy, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	c := r.s.coll("mappings")
	if _, err := c.Insert(doc.ID, doc, &gocb.InsertOptions{Context: ctx}); err != nil {
		return store.Mapping{}, Translate(err, "id")
	}
	err := r.updateIndex(ctx, ws, m.ProjectID, func(idx *mapIndex) error {
		for _, e := range idx.Entries {
			if e.Schema == m.SourceSchema && e.Table == m.SourceTable {
				return &store.ConflictError{Field: "sourceTable"}
			}
		}
		idx.Entries = append(idx.Entries, mapIndexEntry{ID: doc.ID, Schema: m.SourceSchema, Table: m.SourceTable})
		return nil
	})
	if err != nil {
		_, _ = c.Remove(doc.ID, &gocb.RemoveOptions{Context: ctx}) // undo: the table is already mapped
		return store.Mapping{}, err
	}
	return doc.toMapping(), nil
}

func (r mappings) get(ctx context.Context, ws store.WorkspaceID, id string) (mappingDoc, gocb.Cas, error) {
	res, err := r.s.coll("mappings").Get(id, &gocb.GetOptions{Context: ctx})
	if err != nil {
		return mappingDoc{}, 0, Translate(err, "")
	}
	var d mappingDoc
	if err := res.Content(&d); err != nil {
		return mappingDoc{}, 0, err
	}
	if d.WorkspaceID != string(ws) || d.Type != "mapping" {
		return mappingDoc{}, 0, store.ErrNotFound
	}
	return d, res.Cas(), nil
}

func (r mappings) Get(ctx context.Context, ws store.WorkspaceID, id string) (store.Mapping, error) {
	d, _, err := r.get(ctx, ws, id)
	return d.toMapping(), err
}

func (r mappings) ListForProject(ctx context.Context, ws store.WorkspaceID, projectID string) ([]store.Mapping, error) {
	out := []store.Mapping{}
	res, err := r.s.coll("mappings").Get(mapIndexKey(projectID), &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, Translate(err, "")
	}
	var idx mapIndex
	if err := res.Content(&idx); err != nil {
		return nil, err
	}
	if idx.WorkspaceID != string(ws) {
		return out, nil
	}
	for _, e := range idx.Entries {
		d, _, err := r.get(ctx, ws, e.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue // removed concurrently
		}
		if err != nil {
			return nil, err
		}
		out = append(out, d.toMapping())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceSchema != out[j].SourceSchema {
			return out[i].SourceSchema < out[j].SourceSchema
		}
		return out[i].SourceTable < out[j].SourceTable
	})
	return out, nil
}

func (r mappings) Update(ctx context.Context, ws store.WorkspaceID, m store.Mapping) (store.Mapping, error) {
	d, cas, err := r.get(ctx, ws, m.ID)
	if err != nil {
		return store.Mapping{}, err
	}
	if d.Version != m.Version {
		return store.Mapping{}, store.ErrVersionConflict
	}
	d.Target, d.Spec, d.AutomapStats, d.Status = m.Target, m.Spec, m.AutomapStats, m.Status
	d.UpdatedAt = formatTS(r.s.clock.Now())
	d.Version++
	if _, err := r.s.coll("mappings").Replace(d.ID, d, &gocb.ReplaceOptions{Cas: cas, Context: ctx}); err != nil {
		if errors.Is(err, gocb.ErrCasMismatch) {
			return store.Mapping{}, store.ErrVersionConflict
		}
		return store.Mapping{}, Translate(err, "")
	}
	return d.toMapping(), nil
}

func (r mappings) Delete(ctx context.Context, ws store.WorkspaceID, id string) error {
	d, _, err := r.get(ctx, ws, id)
	if err != nil {
		return err
	}
	if err := r.updateIndex(ctx, ws, d.ProjectID, func(idx *mapIndex) error {
		kept := idx.Entries[:0]
		for _, e := range idx.Entries {
			if e.ID != id {
				kept = append(kept, e)
			}
		}
		idx.Entries = kept
		return nil
	}); err != nil {
		return err
	}
	_, err = r.s.coll("mappings").Remove(id, &gocb.RemoveOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil
	}
	return Translate(err, "")
}

// deleteForProject removes a deleted project's mappings and index (best effort per document).
func (r mappings) deleteForProject(ctx context.Context, projectID string) error {
	c := r.s.coll("mappings")
	res, err := c.Get(mapIndexKey(projectID), &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil
	}
	if err != nil {
		return Translate(err, "")
	}
	var idx mapIndex
	if err := res.Content(&idx); err != nil {
		return err
	}
	for _, e := range idx.Entries {
		if _, err := c.Remove(e.ID, &gocb.RemoveOptions{Context: ctx}); err != nil && !errors.Is(err, gocb.ErrDocumentNotFound) {
			return Translate(err, "")
		}
	}
	_, err = c.Remove(mapIndexKey(projectID), &gocb.RemoveOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil
	}
	return Translate(err, "")
}
