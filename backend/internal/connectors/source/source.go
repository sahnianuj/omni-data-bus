// Package source defines the source-connector contract (OC-008). PostgreSQL ships in 1.0;
// MySQL, SQL Server and Oracle can be added without changing discovery, mapping or the pipeline.
package source

import (
	"context"
	"encoding/json"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/schema"
)

// ServerInfo describes a connected source database.
type ServerInfo struct {
	Product  string // "PostgreSQL"
	Version  string // "16.3"
	Database string
	User     string
}

// PrivilegeReport summarizes read access.
type PrivilegeReport struct {
	UnreadableTables []string // schema.table the user can't SELECT
	Superuser        bool
	CanWrite         bool
}

// Batch is one chunk of rows from a Reader (OC-024).
type Batch struct {
	Seq       uint64 // monotonically increasing per partition, from 1
	Partition int
	Rows      [][]any
	LastKey   []any // keyset position after this batch
	// Related holds each embed's rows for every parent row: embed id → row index → child rows
	// (OC-021; filled by the pipeline's embed resolver).
	Related map[string][][][]any
}

// Reader streams batches; Next returns io.EOF when done.
type Reader interface {
	Next(ctx context.Context) (*Batch, error)
	Close() error
}

// DiscoverOptions narrow schema discovery (OC-010).
type DiscoverOptions struct {
	// Schemas is an include list; empty means every non-system schema.
	Schemas []string `json:"schemas,omitempty"`
	// ExcludePatterns are globs matched against "table" and "schema.table".
	ExcludePatterns []string `json:"exclude,omitempty"`
}

// Preview limits (OC-011).
const (
	DefaultPreviewLimit = 50
	MaxPreviewLimit     = 200
)

// ClampPreviewLimit applies the default and the cap.
func ClampPreviewLimit(n int) int {
	switch {
	case n <= 0:
		return DefaultPreviewLimit
	case n > MaxPreviewLimit:
		return MaxPreviewLimit
	}
	return n
}

// PreviewRequest reads the first rows of a table (OC-011). Columns come from the latest
// discovery snapshot; OrderBy names must be columns of the table.
type PreviewRequest struct {
	Schema, Table string
	Columns       []schema.Column
	OrderBy       []string
	Limit         int
	// Random samples rows instead of taking the first ones (OC-022); RowEstimate chooses
	// between TABLESAMPLE (large tables) and ORDER BY random() (small ones).
	Random      bool
	RowEstimate int64
	// TimeoutSec is the statement timeout (default 5 s).
	TimeoutSec int
}

// Session is an open source connection. Reading is added by OC-024.
type Session interface {
	Ping(ctx context.Context) (ServerInfo, error)
	CheckPrivileges(ctx context.Context) (PrivilegeReport, error)
	// EstimateRelations counts what Discover would return (cheap), to choose sync or async.
	EstimateRelations(ctx context.Context, opts DiscoverOptions) (int, error)
	Discover(ctx context.Context, opts DiscoverOptions) (*schema.Snapshot, error)
	// Preview returns up to MaxPreviewLimit rows as driver values, one per requested column.
	Preview(ctx context.Context, req PreviewRequest) ([][]any, error)
	Close() error
}

// SnapshotProber is implemented by sessions whose database can export a transaction snapshot,
// so parallel readers see one consistent state (OC-024). It returns an error when export isn't
// available (e.g. a hot standby without hot_standby_feedback).
type SnapshotProber interface {
	ProbeSnapshotExport(ctx context.Context) error
}

// Connector is a source database type.
type Connector interface {
	Descriptor() connectors.Descriptor
	// Validate checks config shape without network access.
	Validate(cfg json.RawMessage) error
	Open(ctx context.Context, cfg json.RawMessage, creds connectors.Credentials) (Session, error)
	connectors.Tester
}

// Registry is the source connector registry.
type Registry = connectors.Registry[Connector]

// NewRegistry returns an empty source registry.
func NewRegistry() *Registry { return connectors.NewRegistry[Connector](connectors.RoleSource) }
