package memstore

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"omnidatabus/internal/store"
)

// Relationships implements store.Store.
func (s *Store) Relationships() store.Relationships { return relationships{s} }

type relationships struct{ s *Store }

func cloneRel(r store.Relationship) store.Relationship {
	r.From.Columns = append([]string(nil), r.From.Columns...)
	r.To.Columns = append([]string(nil), r.To.Columns...)
	if r.Detected != nil {
		d := *r.Detected
		r.Detected = &d
	}
	return r
}

func (r relationships) Create(_ context.Context, ws store.WorkspaceID, rel store.Relationship) (store.Relationship, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.project(ws, rel.ProjectID); err != nil {
		return store.Relationship{}, err
	}
	if rel.ID == "" {
		rel.ID = "vrel_" + uuid.Must(uuid.NewV7()).String()
	}
	key := store.RelationshipKey(rel.ProjectID, rel.ID)
	if _, dup := s.rels[key]; dup {
		return store.Relationship{}, &store.ConflictError{Field: "id"}
	}
	now := s.clock.Now()
	rel.WorkspaceID, rel.CreatedAt, rel.UpdatedAt, rel.Version = ws, now, now, 1
	s.rels[key] = cloneRel(rel)
	return cloneRel(rel), nil
}

func (r relationships) Get(_ context.Context, ws store.WorkspaceID, projectID, id string) (store.Relationship, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	rel, ok := r.s.rels[store.RelationshipKey(projectID, id)]
	if !ok || rel.WorkspaceID != ws {
		return store.Relationship{}, store.ErrNotFound
	}
	return cloneRel(rel), nil
}

func (r relationships) ListForProject(_ context.Context, ws store.WorkspaceID, projectID string) ([]store.Relationship, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	out := []store.Relationship{}
	for _, rel := range r.s.rels {
		if rel.WorkspaceID == ws && rel.ProjectID == projectID {
			out = append(out, cloneRel(rel))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID)
	})
	return out, nil
}

func (r relationships) Update(_ context.Context, ws store.WorkspaceID, rel store.Relationship) (store.Relationship, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	key := store.RelationshipKey(rel.ProjectID, rel.ID)
	cur, ok := s.rels[key]
	if !ok || cur.WorkspaceID != ws {
		return store.Relationship{}, store.ErrNotFound
	}
	if cur.Version != rel.Version {
		return store.Relationship{}, store.ErrVersionConflict
	}
	cur.Name, cur.From, cur.To, cur.Cardinality, cur.Detected = rel.Name, rel.From, rel.To, rel.Cardinality, rel.Detected
	cur.UpdatedAt = s.clock.Now()
	cur.Version++
	s.rels[key] = cloneRel(cur)
	return cloneRel(cur), nil
}

func (r relationships) Delete(_ context.Context, ws store.WorkspaceID, projectID, id string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	key := store.RelationshipKey(projectID, id)
	rel, ok := r.s.rels[key]
	if !ok || rel.WorkspaceID != ws {
		return store.ErrNotFound
	}
	delete(r.s.rels, key)
	return nil
}
