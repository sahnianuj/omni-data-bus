package source

import (
	"context"
	"errors"

	"omnidatabus/internal/schema"
)

// TableRead describes the rows a mapping reads (OC-024). Rows are returned in the layout of
// Columns (every column of the table, in order); columns not in Project are nil, so a
// transform.Program compiled against the table applies to them directly.
type TableRead struct {
	Schema, Table string
	Columns       []schema.Column
	// Project marks the columns to select (mapped columns, key template and keyset columns).
	Project []bool
	// Key is the keyset order (the primary key, or a not-null unique key); empty = cursor read.
	Key []string
	// App is the session's application_name, e.g. "omni-data-bus job <id> p<k>".
	App string
}

// KeyIndexes returns the positions of t.Key within t.Columns.
func (t TableRead) KeyIndexes() []int {
	out := make([]int, len(t.Key))
	for i, k := range t.Key {
		out[i] = -1
		for j, c := range t.Columns {
			if c.Name == k {
				out[i] = j
			}
		}
	}
	return out
}

// PartitionReader reads one keyset partition inside the shared snapshot.
type PartitionReader interface {
	// ReadAfter returns up to limit rows in key order with key > after (after nil: key ≥ the
	// partition's lower bound) and key < the partition's upper bound.
	ReadAfter(ctx context.Context, after []any, limit int) ([][]any, error)
	Close() error
}

// Resnapshotter is implemented by partition readers that report whether a reconnect had to
// take a new snapshot (the coordinator was gone), which weakens consistency (OC-024).
type Resnapshotter interface{ Resnapshotted() bool }

// CursorReader reads a table without a usable key, once, from the start (no resume).
type CursorReader interface {
	Fetch(ctx context.Context, limit int) ([][]any, error)
	Close() error
}

// ReadSession is one mapping's read phase: a coordinator transaction holding an exported
// snapshot that every partition reader shares (OC-024 AC3).
type ReadSession interface {
	// SnapshotID is the exported snapshot, or "" when export isn't available (then only one
	// reader may be used, inside the coordinator's own transaction).
	SnapshotID() string
	// KeyBounds returns min and max of a single-column key (nil, nil for an empty table).
	KeyBounds(ctx context.Context, t TableRead) (lo, hi any, err error)
	// Count counts the table's rows inside the snapshot.
	Count(ctx context.Context, t TableRead) (int64, error)
	// OpenPartition opens a reader for keys in [lo, hi); nil bounds are open.
	OpenPartition(ctx context.Context, t TableRead, lo, hi any) (PartitionReader, error)
	// OpenCursor opens a single cursor reader over the whole table.
	OpenCursor(ctx context.Context, t TableRead) (CursorReader, error)
	// ReadKeys returns the rows whose key is one of keys (retry of failed rows, OC-026).
	ReadKeys(ctx context.Context, t TableRead, keys [][]any) ([][]any, error)
	Close() error
}

// SnapshotReader is implemented by sessions that can stream tables (PostgreSQL).
type SnapshotReader interface {
	// BeginRead opens the coordinator transaction; with exportSnapshot false (or when export
	// isn't available) SnapshotID is "".
	BeginRead(ctx context.Context, app string, exportSnapshot bool) (ReadSession, error)
}

// TransientError wraps a read error worth retrying (connection reset, admin shutdown,
// serialization failure); the reader reconnects on the next call (OC-024 AC7).
type TransientError struct{ Err error }

func (e *TransientError) Error() string { return "transient: " + e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// IsTransient reports whether err is (or wraps) a TransientError.
func IsTransient(err error) bool {
	var t *TransientError
	return errors.As(err, &t)
}

// OrderColumn is one column of a related table's order (OC-021).
type OrderColumn struct {
	Column string
	Desc   bool
}

// RelatedQuery reads the rows of a related table whose join columns match a batch of keys
// (an embed, OC-021): one query per batch, never one per parent row.
type RelatedQuery struct {
	// Table is the related table; Project must include the join and order columns.
	Table TableRead
	// Join are the related table's columns matched against each key, in key order.
	Join []string
	// OrderBy orders each key's rows (after the join columns).
	OrderBy []OrderColumn
	// PerKeyLimit, when > 0, returns at most this many rows per key.
	PerKeyLimit int
}

// RelatedReader is implemented by read sessions that can look up related rows inside the
// session's snapshot, so embedded rows are consistent with their parents (OC-021 AC5).
type RelatedReader interface {
	ReadRelated(ctx context.Context, q RelatedQuery, keys [][]any) ([][]any, error)
}
