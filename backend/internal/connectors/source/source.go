// Package source defines the source-connector contract (OC-008). PostgreSQL ships in 1.0;
// MySQL, SQL Server and Oracle can be added without changing discovery, mapping or the pipeline.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"time"

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
	// Related holds each row's related rows (index = row index; OC-021, nested from OC-043),
	// filled by the pipeline's relation resolver.
	Related []Related
	// RelatedFailures are rows whose related rows couldn't be read (row index → failure),
	// e.g. RELATION_MEMORY_EXCEEDED; the transform stage dead-letters them.
	RelatedFailures map[int]RowFailure
}

// RowFailure fails one row.
type RowFailure struct {
	Code    string
	Message string
}

// Related are one parent row's related rows by relation id.
type Related map[string]*RelatedSet

// RelatedSet is one relation's rows for one parent row, in the relation's order.
type RelatedSet struct {
	Rows []RelatedRow
	// Dangling counts many-to-many junction rows dropped because their target row is missing
	// (OC-044 RELATION_DANGLING).
	Dangling int
}

// RelatedRow is one related row: its table's columns (a many-to-many row appends its junction
// columns, OC-044), and the rows of the relations nested under it (OC-043).
type RelatedRow struct {
	Values []any
	Sub    Related
}

// FlatRelated builds a parent's related rows from rows without nested relations (relation id
// → rows): the 1.0 shape, used by tests and callers with depth-1 relations only.
func FlatRelated(m map[string][][]any) Related {
	if m == nil {
		return nil
	}
	out := make(Related, len(m))
	for id, rows := range m {
		set := &RelatedSet{Rows: make([]RelatedRow, len(rows))}
		for i, r := range rows {
			set.Rows[i] = RelatedRow{Values: r}
		}
		out[id] = set
	}
	return out
}

// Len is the number of rows (0 for a nil set).
func (s *RelatedSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.Rows)
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
	// DistinctOn reads one row per distinct value of these columns (the first in OrderBy),
	// for a grouped driving table (OC-063).
	DistinctOn []string
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

// ErrQueryTimeout is returned when a probe query hit its statement timeout.
var ErrQueryTimeout = errors.New("the query timed out")

// KeyCounter is implemented by sessions that can measure how many rows share a key (the
// cardinality detection of virtual relationships, OC-042): the most rows of one key value among
// the first sample distinct values, and how many values were looked at. Null keys are ignored.
type KeyCounter interface {
	MaxRowsPerKey(ctx context.Context, schemaName, table string, columns []string, sample int, timeout time.Duration) (maxPerKey, keys int64, err error)
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
