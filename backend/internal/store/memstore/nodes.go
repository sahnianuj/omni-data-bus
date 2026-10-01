package memstore

import (
	"context"

	"omnidatabus/internal/store"
	"omnidatabus/internal/store/provision"
)

// Nodes implements store.Store.
func (s *Store) Nodes() store.Nodes { return nodes{s} }

type nodes struct{ s *Store }

func (r nodes) Beat(_ context.Context, b store.NodeBeat) error {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	b.SeenAt = now
	live := map[string]store.NodeBeat{}
	for _, nb := range store.LiveBeats(s.nodes, now) {
		live[nb.ID] = nb
	}
	live[b.ID] = b
	s.nodes = live
	return nil
}

func (r nodes) Live(context.Context) ([]store.NodeBeat, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	return store.LiveBeats(s.nodes, s.clock.Now()), nil
}

// SchemaVersion reports the newest provisioning step: an in-memory store is always current.
func (s *Store) SchemaVersion(context.Context) (int, error) {
	steps := provision.Steps()
	return steps[len(steps)-1].Version, nil
}
