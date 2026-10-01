// Package target defines the target-connector contract: Couchbase (OC-012) and, from 2.0,
// pluggable kinds behind the same ports (OC-036).
package target

import (
	"omnidatabus/internal/connectors"
)

// Registry is the target connector registry.
type Registry = connectors.Registry[Connector]

// NewRegistry returns an empty target registry.
func NewRegistry() *Registry { return connectors.NewRegistry[Connector](connectors.RoleTarget) }
