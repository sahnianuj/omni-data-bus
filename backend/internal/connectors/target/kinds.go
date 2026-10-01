package target

import (
	"context"
	"encoding/json"
	"time"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/store"
)

// Location is where a mapping writes on its target (a tagged union on Kind, OC-036).
type Location = store.Location

// Target kinds.
const (
	KindCouchbase = store.KindCouchbase
	KindDirectory = store.KindDirectory
)

// Capabilities are what a target kind supports; the UI hides options a kind doesn't (OC-036).
type Capabilities struct {
	// SupportsDurability: the job's durability option applies.
	SupportsDurability bool `json:"supportsDurability"`
	// SupportsExpiry: documents can expire (document TTL).
	SupportsExpiry bool `json:"supportsExpiry"`
	// SupportsCreateLocation: locations can be created from the tool.
	SupportsCreateLocation bool `json:"supportsCreateLocation"`
	// RequiresCredentials: the connection has a password (a directory has none).
	RequiresCredentials bool `json:"requiresCredentials"`
	// MaxDocBytes is the largest document the kind accepts.
	MaxDocBytes int64 `json:"maxDocBytes"`
	// LocationLevels names the levels of a location, root first: bucket/scope/collection or folder.
	LocationLevels []string `json:"locationLevels"`
}

// Connector is a target kind (Couchbase in 1.0; directory from 2.0).
type Connector interface {
	Descriptor() connectors.Descriptor
	Capabilities() Capabilities
	Validate(cfg json.RawMessage) error
	connectors.Tester
	// Close releases pooled sessions on shutdown.
	Close(ctx context.Context) error
}

// Availability is implemented by kinds that the server can switch off (a directory target
// without allowed roots, OC-038). Enabled is false with a reason users can act on.
type Availability interface {
	Available() (enabled bool, reason string)
}

// KindInfo is one entry of GET /target-kinds.
type KindInfo struct {
	connectors.Descriptor
	Capabilities   Capabilities `json:"kindCapabilities"`
	Enabled        bool         `json:"enabled"`
	DisabledReason string       `json:"disabledReason,omitempty"`
}

// Kinds lists the registered target kinds with their capabilities, sorted by display name.
func Kinds(r *Registry) []KindInfo {
	descs := r.Descriptors()
	out := make([]KindInfo, 0, len(descs))
	for _, d := range descs {
		c, err := r.Get(d.Type)
		if err != nil {
			continue
		}
		out = append(out, kindInfo(c))
	}
	return out
}

func kindInfo(c Connector) KindInfo {
	k := KindInfo{Descriptor: c.Descriptor(), Capabilities: c.Capabilities(), Enabled: true}
	if a, ok := c.(Availability); ok {
		k.Enabled, k.DisabledReason = a.Available()
	}
	return k
}

// Info describes one connector (see Kinds).
func Info(c Connector) KindInfo { return kindInfo(c) }

// Conn is a stored target connection opened for a port call: its ID and version key pooled
// sessions (an edit reconnects), Config is the kind's config and Creds its decrypted secrets.
type Conn struct {
	ID      string
	Version string
	Config  json.RawMessage
	Creds   connectors.Credentials
}

// LocationNode is one entry of the location browser: a bucket, scope, collection or folder.
type LocationNode struct {
	Name string `json:"name"`
	// Level is the location level: bucket | scope | collection | folder.
	Level string `json:"level"`
	// Path is the node's location path, the parent to list its children with.
	Path     string   `json:"path"`
	Location Location `json:"location"`
	// Leaf means a mapping can write here (a collection, any folder).
	Leaf bool `json:"leaf"`
	// DocCount is a best-effort count of documents (nil when unknown); DocCountMore means
	// there are more than DocCount (a capped count).
	DocCount     *int64 `json:"docCount,omitempty"`
	DocCountMore bool   `json:"docCountMore,omitempty"`
	// CanCreate reports whether children can be created under this node.
	CanCreate bool `json:"canCreate"`
}

// LocationBrowser lists, inspects and creates locations; it generalizes the Couchbase keyspace
// browser (OC-013, OC-014) so the Target step and the Tables step work for every kind.
type LocationBrowser interface {
	// ListLocations lists the children of parent (the zero location is the root).
	ListLocations(ctx context.Context, c Conn, parent Location) ([]LocationNode, error)
	// StatLocation describes one location; a missing one is a NOT_FOUND problem.
	StatLocation(ctx context.Context, c Conn, loc Location) (LocationNode, error)
	// CreateLocation creates loc (and missing parents the kind can create); ErrPending when
	// it was created but isn't usable everywhere yet.
	CreateLocation(ctx context.Context, c Conn, loc Location) error
}

// PreflightLocation is one mapping's location to check before a job starts.
type PreflightLocation struct {
	MappingID string
	// Name labels the checks ("shop.orders") and prefixes their IDs.
	Name            string
	Location        Location
	CreateIfMissing bool
	WriteMode       string // upsert | insert | replace
	EstimatedDocs   int64
	// AvgDocBytes estimates one document's size (the source table's average row); 0 = unknown.
	AvgDocBytes int64
}

// PreflightCheck is one line of the pre-flight checklist (jobs.Check).
type PreflightCheck struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Status    string `json:"status"` // ok | warn | fail | skipped
	Detail    string `json:"detail,omitempty"`
	Hint      string `json:"hint,omitempty"`
	Code      string `json:"code,omitempty"`
	MappingID string `json:"mappingId,omitempty"`
}

// LocationPreflight is the outcome for one PreflightLocation.
type LocationPreflight struct {
	Checks []PreflightCheck
	// Empty: the location held no documents (or was just created); verification then
	// compares its count (OC-030).
	Empty bool
}

// Preflighter runs a kind's target checks for the locations a job writes to (moved out of
// jobs, OC-023 → OC-036). Results are in the order of locs.
type Preflighter interface {
	Preflight(ctx context.Context, c Conn, locs []PreflightLocation) []LocationPreflight
}

// FolderEntry is one folder in the folder picker.
type FolderEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FolderListing is one screen of the folder picker: the areas a directory target may be in
// (Path ""), or the sub-folders of Path.
type FolderListing struct {
	Path string `json:"path"`
	// Parent is where "up" goes: "" from an area's top folder (back to the areas).
	Parent    string        `json:"parent"`
	Folders   []FolderEntry `json:"folders"`
	CanCreate bool          `json:"canCreate"`
}

// FolderPicker lets users pick a directory target's folder on the server before it's saved
// (OC-038): browse the allowed areas and create folders in them.
type FolderPicker interface {
	Folders(path string) (FolderListing, error)
	MakeFolder(parent, name string) (string, error)
}

// CaseProber is implemented by kinds whose names may fold case (a directory on macOS or
// Windows): keys differing only in case would share a file (OC-039).
type CaseProber interface {
	CaseSensitive(ctx context.Context, c Conn) (bool, error)
}

// Counter counts the documents at a location exactly (verification, OC-030/OC-041); kinds
// without it use LocationBrowser.StatLocation's count.
type Counter interface {
	CountDocuments(ctx context.Context, c Conn, loc Location) (int64, error)
}

// IntegrityIssue is a listed document whose stored copy doesn't check out.
type IntegrityIssue struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	Problem string `json:"problem"` // missing | hash_mismatch
}

// IntegrityReport checks a sample of a writer's own record of what it wrote (a directory's
// manifest) against what is stored now (OC-041).
type IntegrityReport struct {
	Listed     int              `json:"listed"`
	Checked    int              `json:"checked"`
	Missing    int              `json:"missing"`
	Mismatched int              `json:"mismatched"`
	Issues     []IntegrityIssue `json:"issues"`
}

// IntegrityChecker is implemented by writers that keep such a record.
type IntegrityChecker interface {
	CheckIntegrity(ctx context.Context, sample int) (IntegrityReport, error)
}

// Document is one migrated document as stored (OC-039 lookup, OC-041 output browser).
type Document struct {
	Key string `json:"key"`
	// Path is the file, relative to the target folder (directories only).
	Path       string          `json:"path,omitempty"`
	Bytes      int64           `json:"bytes"`
	ModifiedAt *time.Time      `json:"modifiedAt,omitempty"`
	Body       json.RawMessage `json:"document"`
}

// DocumentFinder reads one document by key at a location; a missing one is NOT_FOUND.
type DocumentFinder interface {
	FindDocument(ctx context.Context, c Conn, loc Location, key string) (Document, error)
}

// OutputItem is one entry of an output listing.
type OutputItem struct {
	Key   string `json:"key"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// OutputLister pages through what a mapping wrote, sorted by key (a directory's manifest).
type OutputLister interface {
	ListOutput(ctx context.Context, c Conn, loc Location, mappingID, cursor string, limit int) (items []OutputItem, next string, err error)
}
