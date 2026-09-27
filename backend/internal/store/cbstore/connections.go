package cbstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"omnidatabus/internal/store"

	"github.com/couchbase/gocb/v2"
)

type connectionDoc struct {
	Type           string           `json:"type"`
	ID             string           `json:"id"`
	WorkspaceID    string           `json:"workspaceId"`
	ProjectID      string           `json:"projectId"`
	Role           string           `json:"role"`
	ConnType       string           `json:"connType"`
	Name           string           `json:"name"`
	Config         json.RawMessage  `json:"config"`
	Secret         store.Sealed     `json:"secret"`
	SecretMeta     store.SecretMeta `json:"secretMeta"`
	LastTestStatus string           `json:"lastTestStatus"`
	LastTestedAt   string           `json:"lastTestedAt,omitempty"`
	CreatedBy      string           `json:"createdBy"`
	CreatedAt      string           `json:"createdAt"`
	UpdatedAt      string           `json:"updatedAt"`
	Version        int64            `json:"version"`
}

func (d connectionDoc) toConnection() store.Connection {
	return store.Connection{
		ID: d.ID, WorkspaceID: store.WorkspaceID(d.WorkspaceID), ProjectID: d.ProjectID, Role: store.Role(d.Role),
		Type: d.ConnType, Name: d.Name, Config: d.Config, Secret: d.Secret, SecretMeta: d.SecretMeta, LastTestStatus: d.LastTestStatus,
		LastTestedAt: timePtr(d.LastTestedAt), CreatedBy: d.CreatedBy, CreatedAt: parseTS(d.CreatedAt),
		UpdatedAt: parseTS(d.UpdatedAt), Version: d.Version,
	}
}

type connections struct{ s *Store }

func (r connections) Upsert(ctx context.Context, ws store.WorkspaceID, projectID string, role store.Role, in store.ConnectionInput) (store.Connection, error) {
	if _, err := projects(r).Get(ctx, ws, projectID); err != nil {
		return store.Connection{}, err
	}
	id := store.ConnectionID(projectID, role)
	c := r.s.coll("connections")
	now := formatTS(r.s.clock.Now())
	doc := connectionDoc{
		Type: "connection", ID: id, WorkspaceID: string(ws), ProjectID: projectID, Role: string(role),
		ConnType: in.Type, Name: in.Name, Config: in.Config, Secret: in.Secret, SecretMeta: in.SecretMeta, LastTestStatus: in.LastTestStatus,
		CreatedBy: in.CreatedBy, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if in.LastTestedAt != nil {
		doc.LastTestedAt = formatTS(*in.LastTestedAt)
	}
	for attempt := 0; attempt < 5; attempt++ {
		res, err := c.Get(id, &gocb.GetOptions{Context: ctx})
		if errors.Is(err, gocb.ErrDocumentNotFound) {
			_, err = c.Insert(id, doc, &gocb.InsertOptions{Context: ctx})
			if errors.Is(err, gocb.ErrDocumentExists) {
				continue // created concurrently: retry as an update
			}
			if err != nil {
				return store.Connection{}, Translate(err, "")
			}
			return doc.toConnection(), nil
		}
		if err != nil {
			return store.Connection{}, Translate(err, "")
		}
		var cur connectionDoc
		if err := res.Content(&cur); err != nil {
			return store.Connection{}, err
		}
		doc.CreatedAt, doc.CreatedBy, doc.Version = cur.CreatedAt, cur.CreatedBy, cur.Version+1
		_, err = c.Replace(id, doc, &gocb.ReplaceOptions{Cas: res.Cas(), Context: ctx})
		if errors.Is(err, gocb.ErrCasMismatch) {
			continue
		}
		if err != nil {
			return store.Connection{}, Translate(err, "")
		}
		return doc.toConnection(), nil
	}
	return store.Connection{}, store.ErrVersionConflict
}

func (r connections) get(ctx context.Context, ws store.WorkspaceID, id string) (connectionDoc, gocb.Cas, error) {
	res, err := r.s.coll("connections").Get(id, &gocb.GetOptions{Context: ctx})
	if err != nil {
		return connectionDoc{}, 0, Translate(err, "")
	}
	var d connectionDoc
	if err := res.Content(&d); err != nil {
		return connectionDoc{}, 0, err
	}
	if d.WorkspaceID != string(ws) {
		return connectionDoc{}, 0, store.ErrNotFound
	}
	return d, res.Cas(), nil
}

func (r connections) Get(ctx context.Context, ws store.WorkspaceID, id string) (store.Connection, error) {
	d, _, err := r.get(ctx, ws, id)
	if err != nil {
		return store.Connection{}, err
	}
	return d.toConnection(), nil
}

func (r connections) ListForProject(ctx context.Context, ws store.WorkspaceID, projectID string) ([]store.Connection, error) {
	var out []store.Connection
	for _, role := range []store.Role{store.RoleSource, store.RoleTarget} {
		d, _, err := r.get(ctx, ws, store.ConnectionID(projectID, role))
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, d.toConnection())
	}
	return out, nil
}

func (r connections) SetTestResult(ctx context.Context, ws store.WorkspaceID, id, status string, at time.Time) error {
	for attempt := 0; attempt < 5; attempt++ {
		d, cas, err := r.get(ctx, ws, id)
		if err != nil {
			return err
		}
		d.LastTestStatus, d.LastTestedAt = status, formatTS(at)
		_, err = r.s.coll("connections").Replace(id, d, &gocb.ReplaceOptions{Cas: cas, Context: ctx})
		if errors.Is(err, gocb.ErrCasMismatch) {
			continue
		}
		return Translate(err, "")
	}
	return store.ErrVersionConflict
}

func (r connections) ListForRewrap(ctx context.Context, keyID, afterID string, limit int) ([]store.Connection, error) {
	rows, err := r.s.query(ctx, "ListConnectionsForRewrap", map[string]any{"keyId": keyID, "afterId": afterID, "limit": limit})
	if err != nil {
		return nil, Translate(err, "")
	}
	var out []store.Connection
	for rows.Next() {
		var d connectionDoc
		if err := rows.Row(&d); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, d.toConnection())
	}
	return out, Translate(rows.Err(), "")
}

func (r connections) ReplaceSecret(ctx context.Context, c store.Connection, sealed store.Sealed) error {
	d, cas, err := r.get(ctx, c.WorkspaceID, c.ID)
	if err != nil {
		return err
	}
	if d.Version != c.Version {
		return store.ErrVersionConflict
	}
	d.Secret = sealed
	d.Version++
	_, err = r.s.coll("connections").Replace(c.ID, d, &gocb.ReplaceOptions{Cas: cas, Context: ctx})
	return Translate(err, "")
}

func (r connections) Delete(ctx context.Context, ws store.WorkspaceID, projectID string, role store.Role) error {
	id := store.ConnectionID(projectID, role)
	_, cas, err := r.get(ctx, ws, id)
	if err != nil {
		return err
	}
	if _, err = r.s.coll("connections").Remove(id, &gocb.RemoveOptions{Cas: cas, Context: ctx}); err != nil {
		return Translate(err, "")
	}
	return snapshots(r).deleteForConnection(ctx, ws, id)
}
