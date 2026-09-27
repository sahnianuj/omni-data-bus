package cbstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"omnidatabus/internal/store"

	"github.com/couchbase/gocb/v2"
	"github.com/google/uuid"
)

// AuditRetention is the default for how long audit events are kept (document expiry,
// OC_AUDIT_RETENTION_DAYS).
const AuditRetention = 365 * 24 * time.Hour

type userDoc struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	Email        string `json:"email"`
	PasswordHash string `json:"passwordHash"`
	DisplayName  string `json:"displayName"`
	CreatedAt    string `json:"createdAt"`
}

type workspaceDoc struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

type memberDoc struct {
	Type        string `json:"type"`
	WorkspaceID string `json:"workspaceId"`
	UserID      string `json:"userId"`
	Role        string `json:"role"`
}

type sessionDoc struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	WorkspaceID string `json:"workspaceId"`
	CSRFToken   string `json:"csrfToken"`
	CreatedAt   string `json:"createdAt"`
	LastSeenAt  string `json:"lastSeenAt"`
	ExpiresAt   string `json:"expiresAt"`
	UserAgent   string `json:"userAgent"`
	IP          string `json:"ip"`
}

type auditDoc struct {
	Type        string          `json:"type"`
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspaceId"`
	UserID      string          `json:"userId"`
	Action      string          `json:"action"`
	EntityType  string          `json:"entityType"`
	EntityID    string          `json:"entityId"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   string          `json:"createdAt"`
}

func emailKey(email string) string        { return "email::" + store.NormalizeEmail(email) }
func memberDocKey(ws, user string) string { return ws + "::" + user }
func (d userDoc) toUser() store.User {
	return store.User{ID: d.ID, Email: d.Email, PasswordHash: d.PasswordHash, DisplayName: d.DisplayName, CreatedAt: parseTS(d.CreatedAt)}
}

func (d memberDoc) toMember() store.Member {
	return store.Member{WorkspaceID: d.WorkspaceID, UserID: d.UserID, Role: store.MemberRole(d.Role)}
}

func (d sessionDoc) toSession() store.Session {
	return store.Session{
		ID: d.ID, UserID: d.UserID, WorkspaceID: d.WorkspaceID, CSRFToken: d.CSRFToken, CreatedAt: parseTS(d.CreatedAt),
		LastSeenAt: parseTS(d.LastSeenAt), ExpiresAt: parseTS(d.ExpiresAt), UserAgent: d.UserAgent, IP: d.IP,
	}
}

// Accounts implements store.Store.
func (s *Store) Accounts() store.Accounts { return accounts{s} }

// Sessions implements store.Store.
func (s *Store) Sessions() store.Sessions { return sessions{s} }

// Audit implements store.Store.
func (s *Store) Audit() store.Audit { return audit{s} }

type accounts struct{ s *Store }

func (r accounts) Signup(ctx context.Context, a store.NewAccount) error {
	u := a.User
	email := store.NormalizeEmail(u.Email)
	err := r.s.tx(func(tx *gocb.TransactionAttemptContext) error {
		lock := uniqueDoc{Type: "unique", Kind: "email", Owner: u.ID}
		if _, err := tx.Insert(r.s.coll("uniques"), emailKey(email), lock); err != nil {
			return err
		}
		if _, err := tx.Insert(r.s.coll("users"), u.ID, userDoc{
			Type: "user", ID: u.ID, Email: email, PasswordHash: u.PasswordHash,
			DisplayName: u.DisplayName, CreatedAt: formatTS(u.CreatedAt),
		}); err != nil {
			return err
		}
		if _, err := tx.Insert(r.s.coll("workspaces"), a.Workspace.ID, workspaceDoc{
			Type: "workspace", ID: a.Workspace.ID,
			Name: a.Workspace.Name, CreatedAt: formatTS(a.Workspace.CreatedAt),
		}); err != nil {
			return err
		}
		_, err := tx.Insert(r.s.coll("members"), memberDocKey(a.Workspace.ID, u.ID), memberDoc{
			Type:        "member",
			WorkspaceID: a.Workspace.ID, UserID: u.ID, Role: string(store.RoleOwner),
		})
		return err
	})
	if err != nil {
		return Translate(err, "email")
	}
	// Transactions can't set document expiry, so the first session is written just after.
	return sessions(r).Create(ctx, a.Session)
}

func (r accounts) UserByEmail(ctx context.Context, email string) (store.User, error) {
	res, err := r.s.coll("uniques").Get(emailKey(email), &gocb.GetOptions{Context: ctx})
	if err != nil {
		return store.User{}, Translate(err, "")
	}
	var lock uniqueDoc
	if err := res.Content(&lock); err != nil {
		return store.User{}, err
	}
	return r.User(ctx, lock.Owner)
}

func (r accounts) User(ctx context.Context, id string) (store.User, error) {
	res, err := r.s.coll("users").Get(id, &gocb.GetOptions{Context: ctx})
	if err != nil {
		return store.User{}, Translate(err, "")
	}
	var d userDoc
	if err := res.Content(&d); err != nil {
		return store.User{}, err
	}
	return d.toUser(), nil
}

func (r accounts) Workspace(ctx context.Context, id string) (store.Workspace, error) {
	res, err := r.s.coll("workspaces").Get(id, &gocb.GetOptions{Context: ctx})
	if err != nil {
		return store.Workspace{}, Translate(err, "")
	}
	var d workspaceDoc
	if err := res.Content(&d); err != nil {
		return store.Workspace{}, err
	}
	return store.Workspace{ID: d.ID, Name: d.Name, CreatedAt: parseTS(d.CreatedAt)}, nil
}

func (r accounts) Membership(ctx context.Context, ws, user string) (store.Member, error) {
	res, err := r.s.coll("members").Get(memberDocKey(ws, user), &gocb.GetOptions{Context: ctx})
	if err != nil {
		return store.Member{}, Translate(err, "")
	}
	var d memberDoc
	if err := res.Content(&d); err != nil {
		return store.Member{}, err
	}
	return d.toMember(), nil
}

func (r accounts) DefaultMembership(ctx context.Context, user string) (store.Member, error) {
	rows, err := r.s.query(ctx, "FirstMembershipForUser", map[string]any{"userId": user})
	if err != nil {
		return store.Member{}, Translate(err, "")
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return store.Member{}, Translate(err, "")
		}
		return store.Member{}, store.ErrNotFound
	}
	var d memberDoc
	if err := rows.Row(&d); err != nil {
		return store.Member{}, err
	}
	return d.toMember(), nil
}

func (r accounts) AddMember(ctx context.Context, m store.Member) error {
	_, err := r.s.coll("members").Upsert(memberDocKey(m.WorkspaceID, m.UserID),
		memberDoc{Type: "member", WorkspaceID: m.WorkspaceID, UserID: m.UserID, Role: string(m.Role)}, &gocb.UpsertOptions{Context: ctx})
	return Translate(err, "")
}

type sessions struct{ s *Store }

func (r sessions) Create(ctx context.Context, sess store.Session) error {
	ttl := sess.ExpiresAt.Sub(r.s.clock.Now())
	if ttl < time.Second {
		ttl = time.Second
	}
	doc := sessionDoc{
		Type: "session", ID: sess.ID, UserID: sess.UserID, WorkspaceID: sess.WorkspaceID, CSRFToken: sess.CSRFToken,
		CreatedAt: formatTS(sess.CreatedAt), LastSeenAt: formatTS(sess.LastSeenAt), ExpiresAt: formatTS(sess.ExpiresAt),
		UserAgent: sess.UserAgent, IP: sess.IP,
	}
	_, err := r.s.coll("sessions").Insert(sess.ID, doc, &gocb.InsertOptions{Context: ctx, Expiry: ttl})
	return Translate(err, "id")
}

func (r sessions) Get(ctx context.Context, id string) (store.Session, error) {
	res, err := r.s.coll("sessions").Get(id, &gocb.GetOptions{Context: ctx})
	if err != nil {
		return store.Session{}, Translate(err, "")
	}
	var d sessionDoc
	if err := res.Content(&d); err != nil {
		return store.Session{}, err
	}
	sess := d.toSession()
	if !r.s.clock.Now().Before(sess.ExpiresAt) { // expiry is eventually enforced by the server; be exact here
		return store.Session{}, store.ErrNotFound
	}
	return sess, nil
}

func (r sessions) Touch(ctx context.Context, id string, at time.Time) error {
	_, err := r.s.coll("sessions").MutateIn(id, []gocb.MutateInSpec{gocb.UpsertSpec("lastSeenAt", formatTS(at), nil)},
		&gocb.MutateInOptions{Context: ctx, PreserveExpiry: true})
	return Translate(err, "")
}

func (r sessions) Delete(ctx context.Context, id string) error {
	_, err := r.s.coll("sessions").Remove(id, &gocb.RemoveOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil
	}
	return Translate(err, "")
}

type audit struct{ s *Store }

func (r audit) Record(ctx context.Context, e store.AuditEvent) error {
	if e.ID == "" {
		e.ID = uuid.Must(uuid.NewV7()).String()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = r.s.clock.Now()
	}
	doc := auditDoc{
		Type: "audit", ID: e.ID, WorkspaceID: string(e.WorkspaceID), UserID: e.UserID, Action: e.Action,
		EntityType: e.EntityType, EntityID: e.EntityID, Metadata: e.Metadata, CreatedAt: formatTS(e.CreatedAt),
	}
	keep := r.s.cfg.AuditRetention
	if keep <= 0 {
		keep = AuditRetention
	}
	_, err := r.s.coll("audit").Insert(e.ID, doc, &gocb.InsertOptions{Context: ctx, Expiry: keep})
	return Translate(err, "")
}

func (r audit) Search(ctx context.Context, ws store.WorkspaceID, f store.AuditFilter, page store.Page) ([]store.AuditEvent, string, error) {
	page = page.Normalize()
	cursorAt, cursorID, err := store.DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", err
	}
	at := "9999-12-31T23:59:59.999999999Z"
	if page.Cursor != "" {
		at = formatTS(cursorAt)
	}
	action, prefix := f.Action, ""
	if strings.HasSuffix(action, ".") {
		action, prefix = "", f.Action
	}
	rows, err := r.s.query(ctx, "SearchAudit", map[string]any{
		"workspaceId": string(ws), "action": action, "prefix": prefix, "prefixEnd": prefix + "\uffff",
		"entityType": f.EntityType, "userId": f.UserID, "cursorAt": at, "cursorId": cursorID, "limit": page.Limit + 1,
	})
	if err != nil {
		return nil, "", Translate(err, "")
	}
	var out []store.AuditEvent
	for rows.Next() {
		var d auditDoc
		if err := rows.Row(&d); err != nil {
			_ = rows.Close()
			return nil, "", err
		}
		out = append(out, store.AuditEvent{
			ID: d.ID, WorkspaceID: store.WorkspaceID(d.WorkspaceID), UserID: d.UserID,
			Action: d.Action, EntityType: d.EntityType, EntityID: d.EntityID, Metadata: d.Metadata, CreatedAt: parseTS(d.CreatedAt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, "", Translate(err, "")
	}
	if len(out) <= page.Limit {
		return out, "", nil
	}
	out = out[:page.Limit]
	last := out[len(out)-1]
	return out, store.EncodeCursor(last.CreatedAt, last.ID), nil
}

func (r audit) List(ctx context.Context, ws store.WorkspaceID, limit int) ([]store.AuditEvent, error) {
	rows, err := r.s.query(ctx, "ListAudit", map[string]any{"workspaceId": string(ws), "limit": limit})
	if err != nil {
		return nil, Translate(err, "")
	}
	var out []store.AuditEvent
	for rows.Next() {
		var d auditDoc
		if err := rows.Row(&d); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, store.AuditEvent{
			ID: d.ID, WorkspaceID: store.WorkspaceID(d.WorkspaceID), UserID: d.UserID,
			Action: d.Action, EntityType: d.EntityType, EntityID: d.EntityID, Metadata: d.Metadata, CreatedAt: parseTS(d.CreatedAt),
		})
	}
	return out, Translate(rows.Err(), "")
}
