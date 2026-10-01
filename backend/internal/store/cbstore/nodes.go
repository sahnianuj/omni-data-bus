package cbstore

import (
	"context"
	"errors"
	"fmt"

	"omnidatabus/internal/store"

	"github.com/couchbase/gocb/v2"
)

// nodesDocID is the node registry in the system collection: one small document holding every
// node's heartbeat, updated with CAS. Nodes are few, so one KV document needs no index and no
// query, and expired entries are pruned on every beat.
const nodesDocID = "nodes"

type nodesDoc struct {
	Type  string                    `json:"type"`
	Nodes map[string]store.NodeBeat `json:"nodes"`
}

// Nodes implements store.Store.
func (s *Store) Nodes() store.Nodes { return nodesRepo{s} }

type nodesRepo struct{ s *Store }

func (r nodesRepo) Beat(ctx context.Context, b store.NodeBeat) error {
	c := r.s.coll("system")
	for range 10 {
		res, err := c.Get(nodesDocID, &gocb.GetOptions{Context: ctx})
		doc := nodesDoc{Type: "nodes", Nodes: map[string]store.NodeBeat{}}
		switch {
		case errors.Is(err, gocb.ErrDocumentNotFound):
		case err != nil:
			return Translate(err, "")
		default:
			if err := res.Content(&doc); err != nil {
				return err
			}
		}
		now := r.s.clock.Now()
		b.SeenAt = now
		live := map[string]store.NodeBeat{}
		for _, nb := range store.LiveBeats(doc.Nodes, now) {
			live[nb.ID] = nb
		}
		live[b.ID] = b
		doc.Nodes = live
		if res == nil {
			_, err = c.Insert(nodesDocID, doc, &gocb.InsertOptions{Context: ctx})
			if errors.Is(err, gocb.ErrDocumentExists) {
				continue
			}
		} else {
			_, err = c.Replace(nodesDocID, doc, &gocb.ReplaceOptions{Cas: res.Cas(), Context: ctx})
			if errors.Is(err, gocb.ErrCasMismatch) {
				continue
			}
		}
		return Translate(err, "")
	}
	return fmt.Errorf("node heartbeat: %w", store.ErrVersionConflict)
}

func (r nodesRepo) Live(ctx context.Context) ([]store.NodeBeat, error) {
	res, err := r.s.coll("system").Get(nodesDocID, &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return []store.NodeBeat{}, nil
	}
	if err != nil {
		return nil, Translate(err, "")
	}
	var doc nodesDoc
	if err := res.Content(&doc); err != nil {
		return nil, err
	}
	return store.LiveBeats(doc.Nodes, r.s.clock.Now()), nil
}

// SchemaVersion is the provisioned metadata schema version (shown in build info, OC-058).
func (s *Store) SchemaVersion(ctx context.Context) (int, error) { return env{s}.CurrentVersion(ctx) }
