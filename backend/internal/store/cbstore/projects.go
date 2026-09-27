package cbstore

import (
	"context"
	"errors"
	"time"

	"omnidatabus/internal/store"

	"github.com/couchbase/gocb/v2"
	"github.com/google/uuid"
)

type projectDoc struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedBy   string `json:"createdBy"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	Version     int64  `json:"version"`
}

func (d projectDoc) toProject() store.Project {
	return store.Project{
		ID: d.ID, WorkspaceID: store.WorkspaceID(d.WorkspaceID), Name: d.Name, Description: d.Description,
		CreatedBy: d.CreatedBy, CreatedAt: parseTS(d.CreatedAt), UpdatedAt: parseTS(d.UpdatedAt), Version: d.Version,
	}
}

type uniqueDoc struct {
	Type        string `json:"type"`
	Kind        string `json:"kind"`
	Owner       string `json:"owner"`
	WorkspaceID string `json:"workspaceId"`
}

func projectNameKey(ws store.WorkspaceID, name string) string {
	return "projname::" + string(ws) + "::" + store.NameKey(name)
}

type projects struct{ s *Store }

func (r projects) Create(_ context.Context, ws store.WorkspaceID, in store.NewProject) (store.Project, error) {
	now := formatTS(r.s.clock.Now())
	doc := projectDoc{
		Type: "project", ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: string(ws), Name: in.Name,
		Description: in.Description, CreatedBy: in.CreatedBy, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	err := r.s.tx(func(tx *gocb.TransactionAttemptContext) error {
		lock := uniqueDoc{Type: "unique", Kind: "projname", Owner: doc.ID, WorkspaceID: string(ws)}
		if _, err := tx.Insert(r.s.coll("uniques"), projectNameKey(ws, in.Name), lock); err != nil {
			return err
		}
		_, err := tx.Insert(r.s.coll("projects"), doc.ID, doc)
		return err
	})
	if err != nil {
		return store.Project{}, Translate(err, "name")
	}
	return doc.toProject(), nil
}

func (r projects) Get(ctx context.Context, ws store.WorkspaceID, id string) (store.Project, error) {
	res, err := r.s.coll("projects").Get(id, &gocb.GetOptions{Context: ctx})
	if err != nil {
		return store.Project{}, Translate(err, "")
	}
	var d projectDoc
	if err := res.Content(&d); err != nil {
		return store.Project{}, err
	}
	if d.WorkspaceID != string(ws) {
		return store.Project{}, store.ErrNotFound
	}
	return d.toProject(), nil
}

func (r projects) List(ctx context.Context, ws store.WorkspaceID, page store.Page) ([]store.Project, string, error) {
	page = page.Normalize()
	cursorAt, cursorID, err := store.DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", err
	}
	at := "9999-12-31T23:59:59.999999999Z" // first page: after everything
	if page.Cursor != "" {
		at = formatTS(cursorAt)
	}
	rows, err := r.s.query(ctx, "ListProjects", map[string]any{
		"workspaceId": string(ws), "cursorAt": at, "cursorId": cursorID, "limit": page.Limit + 1,
	})
	if err != nil {
		return nil, "", Translate(err, "")
	}
	var items []store.Project
	for rows.Next() {
		var d projectDoc
		if err := rows.Row(&d); err != nil {
			_ = rows.Close()
			return nil, "", err
		}
		items = append(items, d.toProject())
	}
	if err := rows.Err(); err != nil {
		return nil, "", Translate(err, "")
	}
	if len(items) <= page.Limit {
		return items, "", nil
	}
	items = items[:page.Limit]
	last := items[len(items)-1]
	return items, store.EncodeCursor(last.CreatedAt, last.ID), nil
}

func (r projects) Update(_ context.Context, ws store.WorkspaceID, in store.Project) (store.Project, error) {
	var out projectDoc
	err := r.s.tx(func(tx *gocb.TransactionAttemptContext) error {
		res, err := tx.Get(r.s.coll("projects"), in.ID)
		if errors.Is(err, gocb.ErrDocumentNotFound) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		var d projectDoc
		if err := res.Content(&d); err != nil {
			return err
		}
		if d.WorkspaceID != string(ws) {
			return store.ErrNotFound
		}
		if d.Version != in.Version {
			return store.ErrVersionConflict
		}
		if oldKey, newKey := projectNameKey(ws, d.Name), projectNameKey(ws, in.Name); oldKey != newKey {
			lock := uniqueDoc{Type: "unique", Kind: "projname", Owner: d.ID, WorkspaceID: string(ws)}
			if _, err := tx.Insert(r.s.coll("uniques"), newKey, lock); err != nil {
				if errors.Is(err, gocb.ErrDocumentExists) {
					return &store.ConflictError{Field: "name"}
				}
				return err
			}
			if old, err := tx.Get(r.s.coll("uniques"), oldKey); err == nil {
				if err := tx.Remove(old); err != nil {
					return err
				}
			} else if !errors.Is(err, gocb.ErrDocumentNotFound) {
				return err
			}
		}
		d.Name, d.Description = in.Name, in.Description
		d.UpdatedAt = formatTS(r.s.clock.Now())
		d.Version++
		if _, err := tx.Replace(res, d); err != nil {
			return err
		}
		out = d
		return nil
	})
	if err != nil {
		return store.Project{}, Translate(err, "name")
	}
	return out.toProject(), nil
}

func (r projects) Delete(ctx context.Context, ws store.WorkspaceID, id string) error {
	err := r.s.tx(func(tx *gocb.TransactionAttemptContext) error {
		res, err := tx.Get(r.s.coll("projects"), id)
		if errors.Is(err, gocb.ErrDocumentNotFound) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		var d projectDoc
		if err := res.Content(&d); err != nil {
			return err
		}
		if d.WorkspaceID != string(ws) {
			return store.ErrNotFound
		}
		if err := tx.Remove(res); err != nil {
			return err
		}
		related := []struct{ coll, key string }{
			{"uniques", projectNameKey(ws, d.Name)},
			{"connections", store.ConnectionID(id, store.RoleSource)},
			{"connections", store.ConnectionID(id, store.RoleTarget)},
		}
		for _, rel := range related {
			doc, err := tx.Get(r.s.coll(rel.coll), rel.key)
			if errors.Is(err, gocb.ErrDocumentNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if err := tx.Remove(doc); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Translate(err, "")
	}
	// Snapshots can be megabytes, so they're removed after the transaction commits. A failure
	// here only leaves unreachable documents behind: nothing references them any more.
	for _, role := range []store.Role{store.RoleSource, store.RoleTarget} {
		if err := snapshots(r).deleteForConnection(ctx, ws, store.ConnectionID(id, role)); err != nil {
			return err
		}
	}
	return mappings(r).deleteForProject(ctx, id)
}

// timePtr parses an optional timestamp.
func timePtr(s string) *time.Time {
	if s == "" {
		return nil
	}
	t := parseTS(s)
	return &t
}
