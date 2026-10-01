package connections

import (
	"encoding/json"
	"time"

	"omnidatabus/internal/store"
)

// DTO is the API representation of a connection. It never contains secrets — only
// flags such as passwordSet and the CA certificate fingerprint.
type DTO struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"projectId"`
	Role      string          `json:"role"`
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	// Secrets is absent for kinds without credentials (a directory, OC-038 AC4).
	Secrets        *store.SecretMeta `json:"secrets,omitempty"`
	LastTestStatus string            `json:"lastTestStatus"`
	LastTestedAt   *time.Time        `json:"lastTestedAt,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
	Version        int64             `json:"version"`
}

// ToDTO converts a stored connection for the API.
func ToDTO(c store.Connection) DTO {
	cfg := c.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage(`{}`)
	}
	status := c.LastTestStatus
	if status == "" {
		status = "untested"
	}
	var secrets *store.SecretMeta
	if c.Type != store.KindDirectory {
		secrets = &c.SecretMeta
	}
	return DTO{
		ID: c.ID, ProjectID: c.ProjectID, Role: string(c.Role), Type: c.Type, Name: c.Name, Config: cfg,
		Secrets: secrets, LastTestStatus: status, LastTestedAt: c.LastTestedAt,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Version: c.Version,
	}
}
