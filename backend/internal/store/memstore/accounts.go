package memstore

import (
	"context"
	"sort"
	"time"

	"omnidatabus/internal/store"

	"github.com/google/uuid"
)

type accountsData struct {
	users      map[string]store.User
	emails     map[string]string // normalized email → user id
	workspaces map[string]store.Workspace
	members    map[string]store.Member // ws|user
	sessions   map[string]store.Session
	audit      []store.AuditEvent
}

func newAccountsData() accountsData {
	return accountsData{
		users: map[string]store.User{}, emails: map[string]string{}, workspaces: map[string]store.Workspace{},
		members: map[string]store.Member{}, sessions: map[string]store.Session{},
	}
}

// Accounts implements store.Store.
func (s *Store) Accounts() store.Accounts { return accounts{s} }

// Sessions implements store.Store.
func (s *Store) Sessions() store.Sessions { return sessions{s} }

// Audit implements store.Store.
func (s *Store) Audit() store.Audit { return audit{s} }

type accounts struct{ s *Store }

func memberKey(ws, user string) string { return ws + "|" + user }

func (r accounts) Signup(_ context.Context, a store.NewAccount) error {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	email := store.NormalizeEmail(a.User.Email)
	if _, taken := s.acc.emails[email]; taken {
		return &store.ConflictError{Field: "email"}
	}
	a.User.Email = email
	s.acc.users[a.User.ID] = a.User
	s.acc.emails[email] = a.User.ID
	s.acc.workspaces[a.Workspace.ID] = a.Workspace
	s.acc.members[memberKey(a.Workspace.ID, a.User.ID)] = store.Member{WorkspaceID: a.Workspace.ID, UserID: a.User.ID, Role: store.RoleOwner}
	s.acc.sessions[a.Session.ID] = a.Session
	return nil
}

func (r accounts) UserByEmail(_ context.Context, email string) (store.User, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	id, ok := r.s.acc.emails[store.NormalizeEmail(email)]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return r.s.acc.users[id], nil
}

func (r accounts) User(_ context.Context, id string) (store.User, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	u, ok := r.s.acc.users[id]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return u, nil
}

func (r accounts) Workspace(_ context.Context, id string) (store.Workspace, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	w, ok := r.s.acc.workspaces[id]
	if !ok {
		return store.Workspace{}, store.ErrNotFound
	}
	return w, nil
}

func (r accounts) Membership(_ context.Context, ws, user string) (store.Member, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	m, ok := r.s.acc.members[memberKey(ws, user)]
	if !ok {
		return store.Member{}, store.ErrNotFound
	}
	return m, nil
}

func (r accounts) DefaultMembership(_ context.Context, user string) (store.Member, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var ms []store.Member
	for _, m := range r.s.acc.members {
		if m.UserID == user {
			ms = append(ms, m)
		}
	}
	if len(ms) == 0 {
		return store.Member{}, store.ErrNotFound
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].WorkspaceID < ms[j].WorkspaceID })
	return ms[0], nil
}

func (r accounts) AddMember(_ context.Context, m store.Member) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.acc.members[memberKey(m.WorkspaceID, m.UserID)] = m
	return nil
}

type sessions struct{ s *Store }

func (r sessions) Create(_ context.Context, sess store.Session) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, exists := r.s.acc.sessions[sess.ID]; exists {
		return &store.ConflictError{Field: "id"}
	}
	r.s.acc.sessions[sess.ID] = sess
	return nil
}

func (r sessions) Get(_ context.Context, id string) (store.Session, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	sess, ok := r.s.acc.sessions[id]
	if !ok || !r.s.clock.Now().Before(sess.ExpiresAt) {
		return store.Session{}, store.ErrNotFound // expired documents vanish, like Couchbase TTL
	}
	return sess, nil
}

func (r sessions) Touch(_ context.Context, id string, at time.Time) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	sess, ok := r.s.acc.sessions[id]
	if !ok {
		return store.ErrNotFound
	}
	sess.LastSeenAt = at
	r.s.acc.sessions[id] = sess
	return nil
}

func (r sessions) Delete(_ context.Context, id string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	delete(r.s.acc.sessions, id)
	return nil
}

type audit struct{ s *Store }

func (r audit) Record(_ context.Context, e store.AuditEvent) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if e.ID == "" {
		e.ID = uuid.Must(uuid.NewV7()).String()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = r.s.clock.Now()
	}
	r.s.acc.audit = append(r.s.acc.audit, e)
	return nil
}

func (r audit) Search(_ context.Context, ws store.WorkspaceID, f store.AuditFilter, page store.Page) ([]store.AuditEvent, string, error) {
	page = page.Normalize()
	cursorAt, cursorID, err := store.DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", err
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var all []store.AuditEvent
	for _, e := range r.s.acc.audit {
		if e.WorkspaceID == ws && f.Matches(e) && (page.Cursor == "" || store.Before(e.CreatedAt, e.ID, cursorAt, cursorID)) {
			all = append(all, e)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID > all[j].ID
	})
	if len(all) <= page.Limit {
		return all, "", nil
	}
	all = all[:page.Limit]
	last := all[len(all)-1]
	return all, store.EncodeCursor(last.CreatedAt, last.ID), nil
}

func (r audit) List(_ context.Context, ws store.WorkspaceID, limit int) ([]store.AuditEvent, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	var out []store.AuditEvent
	for i := len(r.s.acc.audit) - 1; i >= 0 && len(out) < limit; i-- {
		if r.s.acc.audit[i].WorkspaceID == ws {
			out = append(out, r.s.acc.audit[i])
		}
	}
	return out, nil
}
