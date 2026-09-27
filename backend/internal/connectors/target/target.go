// Package target defines the target-connector contract (Couchbase in 1.0, OC-012).
package target

import (
	"context"
	"encoding/json"

	"omnidatabus/internal/connectors"
)

// Connector is a target database type.
type Connector interface {
	Descriptor() connectors.Descriptor
	Validate(cfg json.RawMessage) error
	connectors.Tester
	// Close releases pooled sessions on shutdown.
	Close(ctx context.Context) error
}

// Registry is the target connector registry.
type Registry = connectors.Registry[Connector]

// NewRegistry returns an empty target registry.
func NewRegistry() *Registry { return connectors.NewRegistry[Connector](connectors.RoleTarget) }
