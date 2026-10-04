package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// MemberRole is a user's role in a workspace.
type MemberRole string

// Workspace roles, from most to least privileged.
const (
	RoleOwner  MemberRole = "owner"
	RoleEditor MemberRole = "editor"
	RoleViewer MemberRole = "viewer"
)

// Rank orders roles so "at least editor" checks are simple comparisons.
func (r MemberRole) Rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleEditor:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// User is an Omni-Data-Bus account.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	DisplayName  string    `json:"displayName"`
	CreatedAt    time.Time `json:"createdAt"`
}

// NormalizeEmail lower-cases and trims an email for storage and lookup.
func NormalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// Workspace is a tenant.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// Member links a user to a workspace with a role.
type Member struct {
	WorkspaceID string     `json:"workspaceId"`
	UserID      string     `json:"userId"`
	Role        MemberRole `json:"role"`
}

// Session is a server-side login session. ID is the SHA-256 of the cookie token.
type Session struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userId"`
	WorkspaceID string    `json:"workspaceId"`
	CSRFToken   string    `json:"csrfToken"`
	CreatedAt   time.Time `json:"createdAt"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
	ExpiresAt   time.Time `json:"expiresAt"` // absolute expiry; the store drops the session after it
	UserAgent   string    `json:"userAgent"`
	IP          string    `json:"ip"`
	// StepUpAt is when the user last re-entered their password in this session (OC-052).
	StepUpAt *time.Time `json:"stepUpAt,omitempty"`
}

// NewAccount is everything sign-up creates atomically.
type NewAccount struct {
	User      User
	Workspace Workspace
	Session   Session
}

// Accounts stores users, workspaces and memberships.
type Accounts interface {
	// Signup creates the user, the workspace, an owner membership and the first session in one
	// transaction. A taken email returns ConflictError{Field:"email"} and writes nothing.
	Signup(ctx context.Context, a NewAccount) error
	UserByEmail(ctx context.Context, email string) (User, error)
	User(ctx context.Context, id string) (User, error)
	Workspace(ctx context.Context, id string) (Workspace, error)
	Membership(ctx context.Context, workspaceID, userID string) (Member, error)
	// DefaultMembership returns the user's first workspace membership.
	DefaultMembership(ctx context.Context, userID string) (Member, error)
	// AddMember adds or updates a membership (used by tests and future invitations).
	AddMember(ctx context.Context, m Member) error
}

// Sessions stores login sessions.
type Sessions interface {
	Create(ctx context.Context, s Session) error
	Get(ctx context.Context, id string) (Session, error)
	Touch(ctx context.Context, id string, at time.Time) error
	// StepUp records a password re-confirmation in the session (OC-052).
	StepUp(ctx context.Context, id string, at time.Time) error
	Delete(ctx context.Context, id string) error
}

// AuditEvent records a security-relevant action.
type AuditEvent struct {
	ID          string          `json:"id"`
	WorkspaceID WorkspaceID     `json:"workspaceId"`
	UserID      string          `json:"userId"`
	Action      string          `json:"action"`
	EntityType  string          `json:"entityType"`
	EntityID    string          `json:"entityId"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
}

// AuditFilter narrows an audit search; empty fields match everything.
type AuditFilter struct {
	Action     string // an exact action ("job.create") or a prefix ending in "." ("job.")
	EntityType string
	UserID     string
}

// Matches reports whether e passes the filter (memstore and tests).
func (f AuditFilter) Matches(e AuditEvent) bool {
	prefix := strings.HasSuffix(f.Action, ".") && strings.HasPrefix(e.Action, f.Action)
	if f.Action != "" && e.Action != f.Action && !prefix {
		return false
	}
	return (f.EntityType == "" || e.EntityType == f.EntityType) && (f.UserID == "" || e.UserID == f.UserID)
}

// Audit stores audit events.
type Audit interface {
	Record(ctx context.Context, e AuditEvent) error
	List(ctx context.Context, ws WorkspaceID, limit int) ([]AuditEvent, error)
	// Search pages a workspace's events newest first (OC-033).
	Search(ctx context.Context, ws WorkspaceID, f AuditFilter, page Page) ([]AuditEvent, string, error)
}
