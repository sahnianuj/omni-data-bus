// Package store defines Omni-Data-Bus's metadata repositories. Implementations:
// cbstore (Couchbase — Capella in production) and memstore (in memory, for tests).
package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// WorkspaceID scopes every tenant-owned record. Repositories require it on every call.
type WorkspaceID string

// Role is a connection's role within a project.
type Role string

// Connection roles.
const (
	RoleSource Role = "source"
	RoleTarget Role = "target"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleSource || r == RoleTarget }

// Clock is injected so timestamps are testable.
type Clock interface{ Now() time.Time }

// SystemClock is the real clock.
type SystemClock struct{}

// Now returns the current UTC time.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// Page requests one page of a list.
type Page struct {
	Limit  int
	Cursor string
}

// Normalize applies the default (20) and maximum (100) page sizes.
func (p Page) Normalize() Page {
	switch {
	case p.Limit <= 0:
		p.Limit = 20
	case p.Limit > 100:
		p.Limit = 100
	}
	return p
}

// Project is a migration project.
type Project struct {
	ID          string      `json:"id"`
	WorkspaceID WorkspaceID `json:"workspaceId"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	CreatedBy   string      `json:"createdBy"`
	CreatedAt   time.Time   `json:"createdAt"`
	UpdatedAt   time.Time   `json:"updatedAt"`
	// Version increases on every update; updates must send the version they read.
	Version int64 `json:"version"`
}

// NewProject holds the fields needed to create a project.
type NewProject struct {
	Name        string
	Description string
	CreatedBy   string
}

// NameKey normalizes a project name for the per-workspace uniqueness check.
func NameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Sealed is an encrypted secret (OC-006). Stored as-is; never decrypted by the store.
type Sealed struct {
	KeyID      string `json:"keyId"`
	WrappedDEK []byte `json:"wrappedDek"`
	DEKNonce   []byte `json:"dekNonce"`
	Ciphertext []byte `json:"ciphertext"`
	Nonce      []byte `json:"nonce"`
}

// SecretMeta describes a sealed secret without revealing it (safe to return from the API).
type SecretMeta struct {
	PasswordSet       bool   `json:"passwordSet"`
	CACertSet         bool   `json:"caCertSet"`
	CACertFingerprint string `json:"caCertFingerprint,omitempty"`
	ClientCertSet     bool   `json:"clientCertSet"`
}

// Connection is a project's source or target repository.
type Connection struct {
	ID             string          `json:"id"`
	WorkspaceID    WorkspaceID     `json:"workspaceId"`
	ProjectID      string          `json:"projectId"`
	Role           Role            `json:"role"`
	Type           string          `json:"type"`
	Name           string          `json:"name"`
	Config         json.RawMessage `json:"config"`
	Secret         Sealed          `json:"secret"`
	SecretMeta     SecretMeta      `json:"secretMeta"`
	LastTestStatus string          `json:"lastTestStatus"`
	LastTestedAt   *time.Time      `json:"lastTestedAt,omitempty"`
	CreatedBy      string          `json:"createdBy"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	Version        int64           `json:"version"`
}

// ConnectionInput is written by Upsert.
type ConnectionInput struct {
	Type           string
	Name           string
	Config         json.RawMessage
	Secret         Sealed
	SecretMeta     SecretMeta
	LastTestStatus string
	LastTestedAt   *time.Time
	CreatedBy      string
}

// ConnectionID is the deterministic ID (and document key) of a project's connection:
// one source and one target per project.
func ConnectionID(projectID string, role Role) string { return projectID + "::" + string(role) }

// Projects stores migration projects.
type Projects interface {
	Create(ctx context.Context, ws WorkspaceID, p NewProject) (Project, error)
	Get(ctx context.Context, ws WorkspaceID, id string) (Project, error)
	List(ctx context.Context, ws WorkspaceID, page Page) (items []Project, nextCursor string, err error)
	// Update saves Name and Description; p.Version must equal the stored version.
	Update(ctx context.Context, ws WorkspaceID, p Project) (Project, error)
	// Delete removes the project together with its connections, their snapshots, its mappings
	// and its name lock.
	Delete(ctx context.Context, ws WorkspaceID, id string) error
}

// Connections stores project-owned connections.
type Connections interface {
	// Upsert creates or replaces the project's connection for role, keeping its ID.
	Upsert(ctx context.Context, ws WorkspaceID, projectID string, role Role, in ConnectionInput) (Connection, error)
	Get(ctx context.Context, ws WorkspaceID, id string) (Connection, error)
	ListForProject(ctx context.Context, ws WorkspaceID, projectID string) ([]Connection, error)
	SetTestResult(ctx context.Context, ws WorkspaceID, id, status string, at time.Time) error
	// ListForRewrap lists connections in ANY workspace whose secret isn't sealed with keyID,
	// ordered by ID after afterID. Admin-only: used by `odb admin rotate-secrets`.
	ListForRewrap(ctx context.Context, keyID, afterID string, limit int) ([]Connection, error)
	// ReplaceSecret swaps the sealed secret if c.Version is still current.
	ReplaceSecret(ctx context.Context, c Connection, sealed Sealed) error
	// Delete removes the connection and its discovery snapshots.
	Delete(ctx context.Context, ws WorkspaceID, projectID string, role Role) error
}

// DiscoverySnapshot is a stored schema discovery result (OC-010, architecture §5).
type DiscoverySnapshot struct {
	ID           string          `json:"id"`
	WorkspaceID  WorkspaceID     `json:"workspaceId"`
	ConnectionID string          `json:"connectionId"`
	CapturedAt   time.Time       `json:"capturedAt"`
	DurationMs   int64           `json:"durationMs"`
	TableCount   int             `json:"tableCount"`
	Payload      json.RawMessage `json:"payload"` // schema.Snapshot
	IsLatest     bool            `json:"isLatest"`
}

// Snapshots stores discovery snapshots. Each connection has one latest snapshot; older ones
// are kept for drift diffs up to a retention limit. Deleting a connection or its project
// deletes its snapshots.
type Snapshots interface {
	// Save stores snap as its connection's latest snapshot and deletes all but the newest keep.
	Save(ctx context.Context, ws WorkspaceID, snap DiscoverySnapshot, keep int) error
	// Latest returns the connection's newest snapshot, or ErrNotFound.
	Latest(ctx context.Context, ws WorkspaceID, connectionID string) (DiscoverySnapshot, error)
	Get(ctx context.Context, ws WorkspaceID, id string) (DiscoverySnapshot, error)
	// ListIDs returns the connection's snapshot IDs, newest first.
	ListIDs(ctx context.Context, ws WorkspaceID, connectionID string) ([]string, error)
}

// MappingTarget is where a mapping writes.
type MappingTarget struct {
	Bucket          string `json:"bucket"`
	Scope           string `json:"scope"`
	Collection      string `json:"collection"`
	CreateIfMissing bool   `json:"createIfMissing,omitempty"`
}

// Mapping is one source table → target collection mapping of a project (OC-035). Spec and
// AutomapStats are opaque to the store (internal/mapping owns them).
type Mapping struct {
	ID           string          `json:"id"`
	WorkspaceID  WorkspaceID     `json:"workspaceId"`
	ProjectID    string          `json:"projectId"`
	SourceSchema string          `json:"sourceSchema"`
	SourceTable  string          `json:"sourceTable"`
	Target       MappingTarget   `json:"target"`
	Spec         json.RawMessage `json:"spec"`
	AutomapStats json.RawMessage `json:"automapStats,omitempty"`
	// Status is draft | ready | stale, refreshed by the mapping service on every save.
	Status    string    `json:"status"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Version   int64     `json:"version"`
}

// Mappings stores a project's mappings; each source table maps at most once per project.
type Mappings interface {
	// Create stores a new mapping; a second mapping of the same table is a ConflictError.
	Create(ctx context.Context, ws WorkspaceID, m Mapping) (Mapping, error)
	Get(ctx context.Context, ws WorkspaceID, id string) (Mapping, error)
	ListForProject(ctx context.Context, ws WorkspaceID, projectID string) ([]Mapping, error)
	// Update saves Target, Spec, AutomapStats and Status; m.Version must equal the stored version.
	Update(ctx context.Context, ws WorkspaceID, m Mapping) (Mapping, error)
	Delete(ctx context.Context, ws WorkspaceID, id string) error
}

// Store is the metadata store.
type Store interface {
	Accounts() Accounts
	Sessions() Sessions
	Audit() Audit
	Projects() Projects
	Connections() Connections
	Snapshots() Snapshots
	Mappings() Mappings
	Jobs() Jobs
	Ping(ctx context.Context) error
	Close() error
}
