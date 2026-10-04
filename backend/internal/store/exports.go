package store

import (
	"context"
	"time"
)

// ExportRecord is one export of a project (OC-055): who exported which mappings, how, and the
// bundle's hash. It never holds the bundle, a passphrase or any credential.
type ExportRecord struct {
	ID             string      `json:"id"`
	WorkspaceID    WorkspaceID `json:"workspaceId"`
	ProjectID      string      `json:"projectId"`
	CreatedBy      string      `json:"createdBy"`
	CreatedByEmail string      `json:"createdByEmail"`
	CreatedAt      time.Time   `json:"createdAt"`
	MappingIDs     []string    `json:"mappingIds"`
	// PlanHashes are each mapping's plan hash in the bundle, to tell what changed since.
	PlanHashes     map[string]string `json:"planHashes"`
	TargetType     string            `json:"targetType"`
	CredentialMode string            `json:"credentialMode"`
	Runtime        string            `json:"runtime"`
	OdbVersion     string            `json:"odbVersion"`
	BundleSHA256   string            `json:"bundleSha256"`
	PlanHash       string            `json:"planHash"`
}

// Exports stores export history.
type Exports interface {
	Create(ctx context.Context, ws WorkspaceID, e ExportRecord) (ExportRecord, error)
	// List returns the project's exports, newest first.
	List(ctx context.Context, ws WorkspaceID, projectID string, page Page) ([]ExportRecord, string, error)
	// FindByBundle returns the project's export of the bundle with this hash, or ErrNotFound.
	FindByBundle(ctx context.Context, ws WorkspaceID, projectID, bundleSHA256 string) (ExportRecord, error)
}
