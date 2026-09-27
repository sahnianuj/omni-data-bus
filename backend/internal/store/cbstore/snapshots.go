package cbstore

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/couchbase/gocb/v2"

	"omnidatabus/internal/store"
)

// Discovery snapshots are stored one document per snapshot (key = snapshot ID), plus a small
// head document per connection (key "latest::<connectionId>") listing its snapshot IDs newest
// first. The head makes "latest" and retention KV-only (no index) and is the single source of
// truth for isLatest. Snapshot payloads can be megabytes, so they're written outside
// transactions; only the head is updated with CAS.

type snapshotDoc struct {
	Type         string          `json:"type"`
	ID           string          `json:"id"`
	WorkspaceID  string          `json:"workspaceId"`
	ConnectionID string          `json:"connectionId"`
	CapturedAt   string          `json:"capturedAt"`
	DurationMs   int64           `json:"durationMs"`
	TableCount   int             `json:"tableCount"`
	Payload      json.RawMessage `json:"payload"`
}

type snapshotHead struct {
	Type         string   `json:"type"`
	WorkspaceID  string   `json:"workspaceId"`
	ConnectionID string   `json:"connectionId"`
	IDs          []string `json:"ids"`
}

func headKey(connectionID string) string { return "latest::" + connectionID }

type snapshots struct{ s *Store }

// Snapshots implements store.Store.
func (s *Store) Snapshots() store.Snapshots { return snapshots{s} }

func (r snapshots) Save(ctx context.Context, ws store.WorkspaceID, snap store.DiscoverySnapshot, keep int) error {
	if _, err := connections(r).Get(ctx, ws, snap.ConnectionID); err != nil {
		return err
	}
	c := r.s.coll("discovery_snapshots")
	doc := snapshotDoc{
		Type: "discovery_snapshot", ID: snap.ID, WorkspaceID: string(ws), ConnectionID: snap.ConnectionID,
		CapturedAt: formatTS(snap.CapturedAt), DurationMs: snap.DurationMs, TableCount: snap.TableCount, Payload: snap.Payload,
	}
	if _, err := c.Insert(snap.ID, doc, &gocb.InsertOptions{Context: ctx}); err != nil {
		return Translate(err, "")
	}
	key := headKey(snap.ConnectionID)
	for attempt := 0; attempt < 10; attempt++ {
		res, err := c.Get(key, &gocb.GetOptions{Context: ctx})
		head := snapshotHead{Type: "snapshot_head", WorkspaceID: string(ws), ConnectionID: snap.ConnectionID}
		var cas gocb.Cas
		switch {
		case errors.Is(err, gocb.ErrDocumentNotFound):
		case err != nil:
			return Translate(err, "")
		default:
			if err := res.Content(&head); err != nil {
				return err
			}
			cas = res.Cas()
		}
		ids := append([]string{snap.ID}, head.IDs...)
		var pruned []string
		if keep > 0 && len(ids) > keep {
			ids, pruned = ids[:keep], ids[keep:]
		}
		head.IDs = ids
		if cas == 0 {
			_, err = c.Insert(key, head, &gocb.InsertOptions{Context: ctx})
		} else {
			_, err = c.Replace(key, head, &gocb.ReplaceOptions{Cas: cas, Context: ctx})
		}
		if errors.Is(err, gocb.ErrCasMismatch) || errors.Is(err, gocb.ErrDocumentExists) {
			continue // a concurrent save moved the head: re-read and retry
		}
		if err != nil {
			return Translate(err, "")
		}
		for _, id := range pruned {
			if _, err := c.Remove(id, &gocb.RemoveOptions{Context: ctx}); err != nil && !errors.Is(err, gocb.ErrDocumentNotFound) {
				return Translate(err, "")
			}
		}
		return nil
	}
	return store.ErrVersionConflict
}

func (r snapshots) head(ctx context.Context, ws store.WorkspaceID, connectionID string) (snapshotHead, error) {
	res, err := r.s.coll("discovery_snapshots").Get(headKey(connectionID), &gocb.GetOptions{Context: ctx})
	if err != nil {
		return snapshotHead{}, Translate(err, "")
	}
	var h snapshotHead
	if err := res.Content(&h); err != nil {
		return snapshotHead{}, err
	}
	if h.WorkspaceID != string(ws) {
		return snapshotHead{}, store.ErrNotFound
	}
	return h, nil
}

func (r snapshots) Latest(ctx context.Context, ws store.WorkspaceID, connectionID string) (store.DiscoverySnapshot, error) {
	h, err := r.head(ctx, ws, connectionID)
	if err != nil {
		return store.DiscoverySnapshot{}, err
	}
	if len(h.IDs) == 0 {
		return store.DiscoverySnapshot{}, store.ErrNotFound
	}
	snap, err := r.get(ctx, ws, h.IDs[0])
	snap.IsLatest = true
	return snap, err
}

func (r snapshots) Get(ctx context.Context, ws store.WorkspaceID, id string) (store.DiscoverySnapshot, error) {
	snap, err := r.get(ctx, ws, id)
	if err != nil {
		return snap, err
	}
	if h, err := r.head(ctx, ws, snap.ConnectionID); err == nil && len(h.IDs) > 0 {
		snap.IsLatest = h.IDs[0] == id
	}
	return snap, nil
}

func (r snapshots) get(ctx context.Context, ws store.WorkspaceID, id string) (store.DiscoverySnapshot, error) {
	res, err := r.s.coll("discovery_snapshots").Get(id, &gocb.GetOptions{Context: ctx})
	if err != nil {
		return store.DiscoverySnapshot{}, Translate(err, "")
	}
	var d snapshotDoc
	if err := res.Content(&d); err != nil {
		return store.DiscoverySnapshot{}, err
	}
	if d.WorkspaceID != string(ws) || d.Type != "discovery_snapshot" {
		return store.DiscoverySnapshot{}, store.ErrNotFound
	}
	return store.DiscoverySnapshot{
		ID: d.ID, WorkspaceID: ws, ConnectionID: d.ConnectionID, CapturedAt: parseTS(d.CapturedAt),
		DurationMs: d.DurationMs, TableCount: d.TableCount, Payload: d.Payload,
	}, nil
}

func (r snapshots) ListIDs(ctx context.Context, ws store.WorkspaceID, connectionID string) ([]string, error) {
	h, err := r.head(ctx, ws, connectionID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return h.IDs, err
}

// deleteForConnection removes a connection's snapshots and head (best effort per document).
func (r snapshots) deleteForConnection(ctx context.Context, ws store.WorkspaceID, connectionID string) error {
	h, err := r.head(ctx, ws, connectionID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	c := r.s.coll("discovery_snapshots")
	for _, id := range append(h.IDs, headKey(connectionID)) {
		if _, err := c.Remove(id, &gocb.RemoveOptions{Context: ctx}); err != nil && !errors.Is(err, gocb.ErrDocumentNotFound) {
			return Translate(err, "")
		}
	}
	return nil
}
