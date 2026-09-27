package target

import (
	"context"
	"encoding/json"
	"errors"

	"omnidatabus/internal/connectors"
)

// BucketInfo is one bucket as listed by the keyspace browser (OC-013). Settings are zero when
// the bucket was found without management access (a partial listing).
type BucketInfo struct {
	Name           string `json:"name"`
	Type           string `json:"type,omitempty"` // couchbase | ephemeral
	RAMQuotaMB     uint64 `json:"ramQuotaMB,omitempty"`
	NumReplicas    uint32 `json:"numReplicas"`
	StorageBackend string `json:"storageBackend,omitempty"` // couchstore | magma
	// CanManage reports whether the credential may create scopes and collections in it.
	CanManage bool `json:"canManage"`
}

// ScopeInfo is a scope with its collections.
type ScopeInfo struct {
	Name        string           `json:"name"`
	Collections []CollectionInfo `json:"collections"`
}

// CollectionInfo describes a collection. MaxExpirySec is 0 when documents don't expire by
// default (or the bucket default applies), and -1 for "never expire".
type CollectionInfo struct {
	Name             string `json:"name"`
	MaxExpirySec     int64  `json:"maxExpirySec"`
	History          *bool  `json:"history,omitempty"`
	DocCountEstimate *int64 `json:"docCountEstimate,omitempty"`
}

// BucketList is GET …/buckets. Partial is true when the credential can't list buckets and the
// list was derived from its roles (possibly empty: the UI then offers manual entry).
type BucketList struct {
	Buckets []BucketInfo `json:"buckets"`
	Partial bool         `json:"partial"`
	// Capabilities gate the UI's create actions (OC-014).
	Capabilities json.RawMessage `json:"capabilities"`
}

// BucketSpec is a bucket to create (OC-014).
type BucketSpec struct {
	Name           string `json:"name"`
	RAMQuotaMB     uint64 `json:"ramQuotaMB"`
	BucketType     string `json:"bucketType"`     // couchbase | ephemeral
	NumReplicas    uint32 `json:"numReplicas"`    // 0–3
	StorageBackend string `json:"storageBackend"` // couchstore | magma
	FlushEnabled   bool   `json:"flushEnabled"`
}

// CollectionSpec is a collection to create (OC-014).
type CollectionSpec struct {
	Name         string `json:"name"`
	MaxExpirySec int64  `json:"maxExpirySec"`
}

// ErrPending means a keyspace was created but isn't usable on every node yet (HTTP 202).
var ErrPending = errors.New("created; the cluster is still making it available")

// Keyspaces browses and creates buckets, scopes and collections on a stored target
// (OC-013, OC-014). Create methods return ErrPending when the entity was created but isn't
// usable on every node yet.
type Keyspaces interface {
	ListBuckets(ctx context.Context) (BucketList, error)
	ListScopes(ctx context.Context, bucket string) ([]ScopeInfo, error)
	// Collection returns one collection with a best-effort document count.
	Collection(ctx context.Context, bucket, scope, collection string) (CollectionInfo, error)
	CreateBucket(ctx context.Context, spec BucketSpec) error
	CreateScope(ctx context.Context, bucket, scope string) error
	CreateCollection(ctx context.Context, bucket, scope string, spec CollectionSpec) error
	// SampleDocuments returns up to n existing documents to infer a design from (OC-015 AC4),
	// and the method used ("range-scan" or "query").
	SampleDocuments(ctx context.Context, bucket, scope, collection string, n int) ([]json.RawMessage, string, error)
	// ExistingKeys reports which of up to 50 keys already exist (OC-022 AC7).
	ExistingKeys(ctx context.Context, bucket, scope, collection string, keys []string) (map[string]bool, error)
}

// KeyspaceBrowser is implemented by targets with buckets, scopes and collections (Couchbase).
// id and version key a pooled connection: a new version (the repository was edited) reconnects.
type KeyspaceBrowser interface {
	Keyspaces(ctx context.Context, id, version string, cfg json.RawMessage, creds connectors.Credentials) (Keyspaces, error)
}
