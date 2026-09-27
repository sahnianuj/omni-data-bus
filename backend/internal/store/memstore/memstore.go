// Package memstore is an in-memory store.Store with the same semantics as cbstore.
// It backs service and handler unit tests.
package memstore

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"omnidatabus/internal/store"

	"github.com/google/uuid"
)

// Store is safe for concurrent use; every operation is atomic under one lock.
type Store struct {
	mu       sync.Mutex
	clock    store.Clock
	projects map[string]store.Project
	names    map[string]string // workspace|nameKey → project id
	conns    map[string]store.Connection
	snaps    map[string]store.DiscoverySnapshot
	heads    map[string][]string // connection id → snapshot ids, newest first
	maps     map[string]store.Mapping
	acc      accountsData
	jobs     jobsState
}

// New returns an empty store.
func New(clock store.Clock) *Store {
	if clock == nil {
		clock = store.SystemClock{}
	}
	return &Store{
		clock: clock, projects: map[string]store.Project{}, names: map[string]string{}, conns: map[string]store.Connection{},
		snaps: map[string]store.DiscoverySnapshot{}, heads: map[string][]string{}, maps: map[string]store.Mapping{},
		acc: newAccountsData(),
	}
}

// Projects implements store.Store.
func (s *Store) Projects() store.Projects { return projects{s} }

// Connections implements store.Store.
func (s *Store) Connections() store.Connections { return connections{s} }

// Snapshots implements store.Store.
func (s *Store) Snapshots() store.Snapshots { return snapshots{s} }

// Mappings implements store.Store.
func (s *Store) Mappings() store.Mappings { return mappings{s} }

// Ping implements store.Store.
func (s *Store) Ping(context.Context) error { return nil }

// Close implements store.Store.
func (s *Store) Close() error { return nil }

func nameLock(ws store.WorkspaceID, name string) string {
	return string(ws) + "|" + store.NameKey(name)
}

type projects struct{ s *Store }

func (r projects) Create(_ context.Context, ws store.WorkspaceID, in store.NewProject) (store.Project, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := nameLock(ws, in.Name)
	if _, taken := s.names[lock]; taken {
		return store.Project{}, &store.ConflictError{Field: "name"}
	}
	now := s.clock.Now()
	p := store.Project{
		ID: uuid.Must(uuid.NewV7()).String(), WorkspaceID: ws, Name: in.Name, Description: in.Description,
		CreatedBy: in.CreatedBy, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	s.projects[p.ID] = p
	s.names[lock] = p.ID
	return p, nil
}

func (r projects) Get(_ context.Context, ws store.WorkspaceID, id string) (store.Project, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.s.project(ws, id)
}

func (s *Store) project(ws store.WorkspaceID, id string) (store.Project, error) {
	p, ok := s.projects[id]
	if !ok || p.WorkspaceID != ws {
		return store.Project{}, store.ErrNotFound
	}
	return p, nil
}

func (r projects) List(_ context.Context, ws store.WorkspaceID, page store.Page) ([]store.Project, string, error) {
	page = page.Normalize()
	cursorAt, cursorID, err := store.DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", err
	}
	r.s.mu.Lock()
	var all []store.Project
	for _, p := range r.s.projects {
		if p.WorkspaceID == ws && (page.Cursor == "" || store.Before(p.CreatedAt, p.ID, cursorAt, cursorID)) {
			all = append(all, p)
		}
	}
	r.s.mu.Unlock()
	sort.Slice(all, func(i, j int) bool { return store.Before(all[j].CreatedAt, all[j].ID, all[i].CreatedAt, all[i].ID) })
	if len(all) <= page.Limit {
		return all, "", nil
	}
	items := all[:page.Limit]
	last := items[len(items)-1]
	return items, store.EncodeCursor(last.CreatedAt, last.ID), nil
}

func (r projects) Update(_ context.Context, ws store.WorkspaceID, in store.Project) (store.Project, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.project(ws, in.ID)
	if err != nil {
		return store.Project{}, err
	}
	if cur.Version != in.Version {
		return store.Project{}, store.ErrVersionConflict
	}
	oldLock, newLock := nameLock(ws, cur.Name), nameLock(ws, in.Name)
	if oldLock != newLock {
		if _, taken := s.names[newLock]; taken {
			return store.Project{}, &store.ConflictError{Field: "name"}
		}
		delete(s.names, oldLock)
		s.names[newLock] = cur.ID
	}
	cur.Name, cur.Description = in.Name, in.Description
	cur.UpdatedAt = s.clock.Now()
	cur.Version++
	s.projects[cur.ID] = cur
	return cur, nil
}

func (r projects) Delete(_ context.Context, ws store.WorkspaceID, id string) error {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.project(ws, id)
	if err != nil {
		return err
	}
	delete(s.projects, id)
	delete(s.names, nameLock(ws, p.Name))
	for _, role := range []store.Role{store.RoleSource, store.RoleTarget} {
		delete(s.conns, store.ConnectionID(id, role))
		s.dropSnapshots(store.ConnectionID(id, role))
	}
	for mid, m := range s.maps {
		if m.ProjectID == id {
			delete(s.maps, mid)
		}
	}
	return nil
}

type connections struct{ s *Store }

func (r connections) Upsert(_ context.Context, ws store.WorkspaceID, projectID string, role store.Role, in store.ConnectionInput) (store.Connection, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.project(ws, projectID); err != nil {
		return store.Connection{}, err
	}
	id := store.ConnectionID(projectID, role)
	now := s.clock.Now()
	c, exists := s.conns[id]
	if !exists {
		c = store.Connection{ID: id, WorkspaceID: ws, ProjectID: projectID, Role: role, CreatedAt: now, CreatedBy: in.CreatedBy}
	}
	c.Type, c.Name, c.Secret, c.SecretMeta = in.Type, in.Name, cloneSealed(in.Secret), in.SecretMeta
	c.Config = append(json.RawMessage(nil), in.Config...)
	c.LastTestStatus, c.LastTestedAt = in.LastTestStatus, in.LastTestedAt
	c.UpdatedAt = now
	c.Version++
	s.conns[id] = c
	return c, nil
}

func cloneSealed(in store.Sealed) store.Sealed {
	return store.Sealed{
		KeyID: in.KeyID, WrappedDEK: append([]byte(nil), in.WrappedDEK...), DEKNonce: append([]byte(nil), in.DEKNonce...),
		Ciphertext: append([]byte(nil), in.Ciphertext...), Nonce: append([]byte(nil), in.Nonce...),
	}
}

func (r connections) Get(_ context.Context, ws store.WorkspaceID, id string) (store.Connection, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	c, ok := r.s.conns[id]
	if !ok || c.WorkspaceID != ws {
		return store.Connection{}, store.ErrNotFound
	}
	return c, nil
}

func (r connections) ListForProject(_ context.Context, ws store.WorkspaceID, projectID string) ([]store.Connection, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var out []store.Connection
	for _, role := range []store.Role{store.RoleSource, store.RoleTarget} {
		if c, ok := r.s.conns[store.ConnectionID(projectID, role)]; ok && c.WorkspaceID == ws {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r connections) SetTestResult(_ context.Context, ws store.WorkspaceID, id, status string, at time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	c, ok := r.s.conns[id]
	if !ok || c.WorkspaceID != ws {
		return store.ErrNotFound
	}
	at = at.UTC()
	c.LastTestStatus, c.LastTestedAt = status, &at
	r.s.conns[id] = c
	return nil
}

func (r connections) ListForRewrap(_ context.Context, keyID, afterID string, limit int) ([]store.Connection, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var out []store.Connection
	for _, c := range r.s.conns {
		if c.Secret.KeyID != keyID && c.ID > afterID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r connections) ReplaceSecret(_ context.Context, c store.Connection, sealed store.Sealed) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	cur, ok := r.s.conns[c.ID]
	if !ok || cur.WorkspaceID != c.WorkspaceID {
		return store.ErrNotFound
	}
	if cur.Version != c.Version {
		return store.ErrVersionConflict
	}
	cur.Secret = cloneSealed(sealed)
	cur.Version++
	r.s.conns[c.ID] = cur
	return nil
}

func (r connections) Delete(_ context.Context, ws store.WorkspaceID, projectID string, role store.Role) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	id := store.ConnectionID(projectID, role)
	c, ok := r.s.conns[id]
	if !ok || c.WorkspaceID != ws {
		return store.ErrNotFound
	}
	delete(r.s.conns, id)
	r.s.dropSnapshots(id)
	return nil
}

func (s *Store) dropSnapshots(connectionID string) {
	for _, id := range s.heads[connectionID] {
		delete(s.snaps, id)
	}
	delete(s.heads, connectionID)
}

type snapshots struct{ s *Store }

func (r snapshots) Save(_ context.Context, ws store.WorkspaceID, snap store.DiscoverySnapshot, keep int) error {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.conns[snap.ConnectionID]; !ok || c.WorkspaceID != ws {
		return store.ErrNotFound
	}
	snap.WorkspaceID = ws
	snap.Payload = append(json.RawMessage(nil), snap.Payload...)
	s.snaps[snap.ID] = snap
	ids := append([]string{snap.ID}, s.heads[snap.ConnectionID]...)
	if keep > 0 && len(ids) > keep {
		for _, old := range ids[keep:] {
			delete(s.snaps, old)
		}
		ids = ids[:keep]
	}
	s.heads[snap.ConnectionID] = ids
	return nil
}

func (r snapshots) Latest(_ context.Context, ws store.WorkspaceID, connectionID string) (store.DiscoverySnapshot, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	ids := r.s.heads[connectionID]
	if len(ids) == 0 {
		return store.DiscoverySnapshot{}, store.ErrNotFound
	}
	return r.s.snapshot(ws, ids[0])
}

func (r snapshots) Get(_ context.Context, ws store.WorkspaceID, id string) (store.DiscoverySnapshot, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.s.snapshot(ws, id)
}

func (s *Store) snapshot(ws store.WorkspaceID, id string) (store.DiscoverySnapshot, error) {
	snap, ok := s.snaps[id]
	if !ok || snap.WorkspaceID != ws {
		return store.DiscoverySnapshot{}, store.ErrNotFound
	}
	ids := s.heads[snap.ConnectionID]
	snap.IsLatest = len(ids) > 0 && ids[0] == id
	return snap, nil
}

func (r snapshots) ListIDs(_ context.Context, ws store.WorkspaceID, connectionID string) ([]string, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if c, ok := r.s.conns[connectionID]; !ok || c.WorkspaceID != ws {
		return nil, nil
	}
	return append([]string(nil), r.s.heads[connectionID]...), nil
}

type mappings struct{ s *Store }

func cloneMapping(m store.Mapping) store.Mapping {
	m.Spec = append(json.RawMessage(nil), m.Spec...)
	m.AutomapStats = append(json.RawMessage(nil), m.AutomapStats...)
	return m
}

func (r mappings) Create(_ context.Context, ws store.WorkspaceID, m store.Mapping) (store.Mapping, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.project(ws, m.ProjectID); err != nil {
		return store.Mapping{}, err
	}
	for _, other := range s.maps {
		if other.ProjectID == m.ProjectID && other.SourceSchema == m.SourceSchema && other.SourceTable == m.SourceTable {
			return store.Mapping{}, &store.ConflictError{Field: "sourceTable"}
		}
	}
	if m.ID == "" {
		m.ID = uuid.Must(uuid.NewV7()).String()
	}
	now := s.clock.Now()
	m.WorkspaceID, m.CreatedAt, m.UpdatedAt, m.Version = ws, now, now, 1
	s.maps[m.ID] = cloneMapping(m)
	return cloneMapping(m), nil
}

func (r mappings) Get(_ context.Context, ws store.WorkspaceID, id string) (store.Mapping, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	m, ok := r.s.maps[id]
	if !ok || m.WorkspaceID != ws {
		return store.Mapping{}, store.ErrNotFound
	}
	return cloneMapping(m), nil
}

func (r mappings) ListForProject(_ context.Context, ws store.WorkspaceID, projectID string) ([]store.Mapping, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	out := []store.Mapping{}
	for _, m := range r.s.maps {
		if m.WorkspaceID == ws && m.ProjectID == projectID {
			out = append(out, cloneMapping(m))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceSchema != out[j].SourceSchema {
			return out[i].SourceSchema < out[j].SourceSchema
		}
		return out[i].SourceTable < out[j].SourceTable
	})
	return out, nil
}

func (r mappings) Update(_ context.Context, ws store.WorkspaceID, m store.Mapping) (store.Mapping, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.maps[m.ID]
	if !ok || cur.WorkspaceID != ws {
		return store.Mapping{}, store.ErrNotFound
	}
	if cur.Version != m.Version {
		return store.Mapping{}, store.ErrVersionConflict
	}
	cur.Target, cur.Spec, cur.AutomapStats, cur.Status = m.Target, m.Spec, m.AutomapStats, m.Status
	cur.UpdatedAt = s.clock.Now()
	cur.Version++
	s.maps[m.ID] = cloneMapping(cur)
	return cloneMapping(cur), nil
}

func (r mappings) Delete(_ context.Context, ws store.WorkspaceID, id string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	m, ok := r.s.maps[id]
	if !ok || m.WorkspaceID != ws {
		return store.ErrNotFound
	}
	delete(r.s.maps, id)
	return nil
}
