package target

import (
	"context"
	"encoding/json"
	"time"

	"omnidatabus/internal/connectors"
)

// Doc is one serialized document to write (OC-025). Value is JSON, written as-is.
type Doc struct {
	Key   string
	Value []byte
	// SourceKey is the row's primary key, for dead-lettering a failed write (OC-026).
	SourceKey []any
}

// Outcome of one document write.
type Outcome int

// Outcomes.
const (
	Written Outcome = iota
	Skipped         // insert of an existing key, or replace of a missing one
	Failed
)

// Write error codes. Fatal ones stop the mapping or the job (OC-025 AC5).
const (
	CodeDocTooLarge          = "DOC_TOO_LARGE"
	CodeKeyInvalid           = "KEY_INVALID"
	CodeTemporaryExhausted   = "TEMPORARY_FAILURE_EXHAUSTED"
	CodeWriteFailed          = "WRITE_FAILED"
	CodeKeyspaceMissing      = "TARGET_KEYSPACE_MISSING"
	CodePermissionDenied     = "TARGET_PERMISSION_DENIED"
	CodeDurabilityImpossible = "DURABILITY_IMPOSSIBLE"
)

// WriteResult is one document's final outcome.
type WriteResult struct {
	Key      string
	Outcome  Outcome
	Code     string // set when Failed
	Message  string
	Attempts int
	// Fatal means continuing is pointless (missing keyspace, no permission, impossible durability).
	Fatal bool
}

// WriteOptions configure a writer (from the mapping and the job options).
type WriteOptions struct {
	Bucket, Scope, Collection string
	Mode                      string // upsert | insert | replace
	Durability                string // none | majority | majorityAndPersistActive | persistToMajority
	Expiry                    time.Duration
	// Concurrency is the maximum number of in-flight KV operations (AIMD may lower it).
	Concurrency int
	// OnConcurrency is told when adaptive concurrency changes (job log, OC-028 AC7).
	OnConcurrency func(limit int, reason string)
}

// DocWriter writes documents. WriteBatch returns one result per document, in order; it never
// silently drops a document.
type DocWriter interface {
	WriteBatch(ctx context.Context, docs []Doc) []WriteResult
	Close() error
}

// DocReader is implemented by writers that can read documents back (post-migration
// verification, OC-030): the documents found, by key; absent keys aren't in the map.
type DocReader interface {
	GetDocs(ctx context.Context, keys []string) (map[string][]byte, error)
}

// WriterOpener is implemented by targets that can write documents (Couchbase). Each job gets a
// dedicated connection, so migration traffic doesn't share KV connections with the UI.
type WriterOpener interface {
	OpenWriter(ctx context.Context, cfg json.RawMessage, creds connectors.Credentials, opts WriteOptions) (DocWriter, error)
}
