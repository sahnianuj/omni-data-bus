package cbstore

import (
	"context"
	"errors"
	"sort"

	"github.com/couchbase/gocb/v2"
	"github.com/google/uuid"

	"omnidatabus/internal/store"
)

// Virtual relationships (OC-042) are one document each, keyed "<projectId>::<relId>", plus an
// index document per project ("relidx::<projectId>") listing their IDs, updated with CAS, so
// listing is KV-only and consistent right after a write (as for mappings).

type relationshipDoc struct {
	Type        string                       `json:"type"`
	ID          string                       `json:"id"`
	WorkspaceID string                       `json:"workspaceId"`
	ProjectID   string                       `json:"projectId"`
	Name        string                       `json:"name"`
	From        store.RelationshipEnd        `json:"from"`
	To          store.RelationshipEnd        `json:"to"`
	Cardinality string                       `json:"cardinality"`
	Detected    *store.RelationshipDetection `json:"detected,omitempty"`
	CreatedBy   string                       `json:"createdBy"`
	CreatedAt   string                       `json:"createdAt"`
	UpdatedAt   string                       `json:"updatedAt"`
	Version     int64                        `json:"version"`
}

func (d relationshipDoc) toRelationship() store.Relationship {
	return store.Relationship{
		ID: d.ID, WorkspaceID: store.WorkspaceID(d.WorkspaceID), ProjectID: d.ProjectID, Name: d.Name, From: d.From, To: d.To,
		Cardinality: d.Cardinality, Detected: d.Detected, CreatedBy: d.CreatedBy,
		CreatedAt: parseTS(d.CreatedAt), UpdatedAt: parseTS(d.UpdatedAt), Version: d.Version,
	}
}

type relIndex struct {
	Type        string   `json:"type"`
	WorkspaceID string   `json:"workspaceId"`
	ProjectID   string   `json:"projectId"`
	IDs         []string `json:"ids"`
}

func relIndexKey(projectID string) string { return "relidx::" + projectID }

type relationships struct{ s *Store }

// Relationships implements store.Store.
func (s *Store) Relationships() store.Relationships { return relationships{s} }

func (r relationships) coll() *gocb.Collection { return r.s.coll("relationships") }

// updateIndex applies fn to the project's index with CAS, retrying on concurrent changes.
func (r relationships) updateIndex(ctx context.Context, ws store.WorkspaceID, projectID string, fn func(*relIndex)) error {
	c := r.coll()
	key := relIndexKey(projectID)
	for range 10 {
		idx := relIndex{Type: "relationship_index", WorkspaceID: string(ws), ProjectID: projectID, IDs: []string{}}
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
		fn(&idx)
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

func (r relationships) Create(ctx context.Context, ws store.WorkspaceID, rel store.Relationship) (store.Relationship, error) {
	if _, err := projects(r).Get(ctx, ws, rel.ProjectID); err != nil {
		return store.Relationship{}, err
	}
	if rel.ID == "" {
		rel.ID = "vrel_" + uuid.Must(uuid.NewV7()).String()
	}
	now := formatTS(r.s.clock.Now())
	d := relationshipDoc{
		Type: "relationship", ID: rel.ID, WorkspaceID: string(ws), ProjectID: rel.ProjectID, Name: rel.Name, From: rel.From, To: rel.To,
		Cardinality: rel.Cardinality, Detected: rel.Detected, CreatedBy: rel.CreatedBy, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if _, err := r.coll().Insert(store.RelationshipKey(rel.ProjectID, rel.ID), d, &gocb.InsertOptions{Context: ctx}); err != nil {
		return store.Relationship{}, Translate(err, "id")
	}
	if err := r.updateIndex(ctx, ws, rel.ProjectID, func(idx *relIndex) { idx.IDs = append(idx.IDs, rel.ID) }); err != nil {
		_, _ = r.coll().Remove(store.RelationshipKey(rel.ProjectID, rel.ID), &gocb.RemoveOptions{Context: ctx})
		return store.Relationship{}, err
	}
	return d.toRelationship(), nil
}

func (r relationships) get(ctx context.Context, ws store.WorkspaceID, projectID, id string) (relationshipDoc, gocb.Cas, error) {
	res, err := r.coll().Get(store.RelationshipKey(projectID, id), &gocb.GetOptions{Context: ctx})
	if err != nil {
		return relationshipDoc{}, 0, Translate(err, "")
	}
	var d relationshipDoc
	if err := res.Content(&d); err != nil {
		return relationshipDoc{}, 0, err
	}
	if d.WorkspaceID != string(ws) || d.Type != "relationship" {
		return relationshipDoc{}, 0, store.ErrNotFound
	}
	return d, res.Cas(), nil
}

func (r relationships) Get(ctx context.Context, ws store.WorkspaceID, projectID, id string) (store.Relationship, error) {
	d, _, err := r.get(ctx, ws, projectID, id)
	return d.toRelationship(), err
}

func (r relationships) ListForProject(ctx context.Context, ws store.WorkspaceID, projectID string) ([]store.Relationship, error) {
	out := []store.Relationship{}
	res, err := r.coll().Get(relIndexKey(projectID), &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, Translate(err, "")
	}
	var idx relIndex
	if err := res.Content(&idx); err != nil {
		return nil, err
	}
	if idx.WorkspaceID != string(ws) {
		return out, nil
	}
	for _, id := range idx.IDs {
		d, _, err := r.get(ctx, ws, projectID, id)
		if errors.Is(err, store.ErrNotFound) {
			continue // removed concurrently
		}
		if err != nil {
			return nil, err
		}
		out = append(out, d.toRelationship())
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (r relationships) Update(ctx context.Context, ws store.WorkspaceID, rel store.Relationship) (store.Relationship, error) {
	d, cas, err := r.get(ctx, ws, rel.ProjectID, rel.ID)
	if err != nil {
		return store.Relationship{}, err
	}
	if d.Version != rel.Version {
		return store.Relationship{}, store.ErrVersionConflict
	}
	d.Name, d.From, d.To, d.Cardinality, d.Detected = rel.Name, rel.From, rel.To, rel.Cardinality, rel.Detected
	d.UpdatedAt = formatTS(r.s.clock.Now())
	d.Version++
	if _, err := r.coll().Replace(store.RelationshipKey(d.ProjectID, d.ID), d, &gocb.ReplaceOptions{Cas: cas, Context: ctx}); err != nil {
		if errors.Is(err, gocb.ErrCasMismatch) {
			return store.Relationship{}, store.ErrVersionConflict
		}
		return store.Relationship{}, Translate(err, "")
	}
	return d.toRelationship(), nil
}

func (r relationships) Delete(ctx context.Context, ws store.WorkspaceID, projectID, id string) error {
	if _, _, err := r.get(ctx, ws, projectID, id); err != nil {
		return err
	}
	if err := r.updateIndex(ctx, ws, projectID, func(idx *relIndex) {
		kept := idx.IDs[:0]
		for _, x := range idx.IDs {
			if x != id {
				kept = append(kept, x)
			}
		}
		idx.IDs = kept
	}); err != nil {
		return err
	}
	_, err := r.coll().Remove(store.RelationshipKey(projectID, id), &gocb.RemoveOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil
	}
	return Translate(err, "")
}

// deleteForProject removes a deleted project's relationships and index.
func (r relationships) deleteForProject(ctx context.Context, projectID string) error {
	c := r.coll()
	res, err := c.Get(relIndexKey(projectID), &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil
	}
	if err != nil {
		return Translate(err, "")
	}
	var idx relIndex
	if err := res.Content(&idx); err != nil {
		return err
	}
	for _, id := range idx.IDs {
		if _, err := c.Remove(store.RelationshipKey(projectID, id), &gocb.RemoveOptions{Context: ctx}); err != nil && !errors.Is(err, gocb.ErrDocumentNotFound) {
			return Translate(err, "")
		}
	}
	_, err = c.Remove(relIndexKey(projectID), &gocb.RemoveOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil
	}
	return Translate(err, "")
}
