package store

import (
	"context"
	"time"
)

// Relationship is a user-defined (virtual) relationship of a project's source where the database
// has no foreign key (OC-042), stored as "<projectId>::<relId>" in the relationships collection.
type Relationship struct {
	ID          string          `json:"id"`
	WorkspaceID WorkspaceID     `json:"workspaceId"`
	ProjectID   string          `json:"projectId"`
	Name        string          `json:"name"`
	From        RelationshipEnd `json:"from"`
	To          RelationshipEnd `json:"to"`
	// Cardinality as seen from From: many-to-one | one-to-one | one-to-many.
	Cardinality string                 `json:"cardinality"`
	Detected    *RelationshipDetection `json:"detected,omitempty"`
	CreatedBy   string                 `json:"createdBy"`
	CreatedAt   time.Time              `json:"createdAt"`
	UpdatedAt   time.Time              `json:"updatedAt"`
	Version     int64                  `json:"version"`
}

// RelationshipEnd is one side of a relationship: a table and its join columns (paired by position).
type RelationshipEnd struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
}

// RelationshipDetection is what sampling the data found (OC-042 cardinality detection).
type RelationshipDetection struct {
	// MaxPerKey is the most To rows sharing one key value; FromMaxPerKey the same for From.
	MaxPerKey     int64     `json:"maxPerKey"`
	FromMaxPerKey int64     `json:"fromMaxPerKey"`
	SampledRows   int64     `json:"sampledRows"`
	Suggested     string    `json:"suggested"` // a cardinality, or "unknown" when the sample timed out
	At            time.Time `json:"at"`
}

// Relationships stores a project's virtual relationships.
type Relationships interface {
	Create(ctx context.Context, ws WorkspaceID, r Relationship) (Relationship, error)
	Get(ctx context.Context, ws WorkspaceID, projectID, id string) (Relationship, error)
	ListForProject(ctx context.Context, ws WorkspaceID, projectID string) ([]Relationship, error)
	// Update saves Name, From, To, Cardinality and Detected; r.Version must equal the stored version.
	Update(ctx context.Context, ws WorkspaceID, r Relationship) (Relationship, error)
	Delete(ctx context.Context, ws WorkspaceID, projectID, id string) error
}

// RelationshipKey is a relationship's document key.
func RelationshipKey(projectID, id string) string { return projectID + "::" + id }
