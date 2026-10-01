package store

import (
	"encoding/json"
	"strings"
)

// Target kinds (OC-036). A project's target connection has exactly one.
const (
	KindCouchbase = "couchbase"
	KindDirectory = "directory"
)

// Location is where a mapping's documents go on its project's target: a tagged union on Kind.
// Couchbase uses Bucket, Scope and Collection; a directory uses Folder (relative to its root).
type Location struct {
	Kind       string `json:"kind"`
	Bucket     string `json:"bucket,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Collection string `json:"collection,omitempty"`
	Folder     string `json:"folder,omitempty"`
	// CreateIfMissing creates the collection or folder when the migration starts.
	CreateIfMissing bool `json:"createIfMissing,omitempty"`
}

// IsZero reports whether no location is set.
func (l Location) IsZero() bool {
	return l.Bucket == "" && l.Scope == "" && l.Collection == "" && l.Folder == ""
}

// String is the location as users see it: "bucket.scope.collection" or "orders/".
func (l Location) String() string {
	if l.Kind == KindDirectory {
		return strings.Trim(l.Folder, "/") + "/"
	}
	return l.Bucket + "." + l.Scope + "." + l.Collection
}

// Path is the location as slash-separated segments ("bucket/scope/collection", "orders/2026"),
// the form the location browser takes as its parent.
func (l Location) Path() string {
	if l.Kind == KindDirectory {
		return strings.Trim(l.Folder, "/")
	}
	var segs []string
	for _, s := range []string{l.Bucket, l.Scope, l.Collection} {
		if s == "" {
			break
		}
		segs = append(segs, s)
	}
	return strings.Join(segs, "/")
}

// LocationFromPath parses Path for kind; an empty path is the root.
func LocationFromPath(kind, path string) Location {
	path = strings.Trim(path, "/")
	if kind == KindDirectory {
		return Location{Kind: kind, Folder: path}
	}
	l := Location{Kind: kind}
	if path == "" {
		return l
	}
	segs := strings.SplitN(path, "/", 3)
	l.Bucket = segs[0]
	if len(segs) > 1 {
		l.Scope = segs[1]
	}
	if len(segs) > 2 {
		l.Collection = segs[2]
	}
	return l
}

// Location converts a 1.0 mapping target to a Couchbase location (zero when unset).
func (t MappingTarget) Location() Location {
	if t.Bucket == "" && t.Scope == "" && t.Collection == "" {
		return Location{}
	}
	return Location{Kind: KindCouchbase, Bucket: t.Bucket, Scope: t.Scope, Collection: t.Collection, CreateIfMissing: t.CreateIfMissing}
}

// MappingTarget is the 1.0 form of a Couchbase location (zero for other kinds). The API and the
// stored document keep it so 1.0 clients and old job options still read.
func (l Location) MappingTarget() MappingTarget {
	if l.Kind != KindCouchbase {
		return MappingTarget{}
	}
	return MappingTarget{Bucket: l.Bucket, Scope: l.Scope, Collection: l.Collection, CreateIfMissing: l.CreateIfMissing}
}

// Where returns the mapping's location: Location when set, otherwise its 1.0 target (OC-036 AC4).
func (m Mapping) Where() Location {
	if m.Location.Kind != "" && !m.Location.IsZero() {
		return m.Location
	}
	return m.Target.Location()
}

// SetLocation stores l and mirrors a Couchbase location into the 1.0 Target field.
func (m *Mapping) SetLocation(l Location) {
	m.Location = l
	m.Target = l.MappingTarget()
}

// NormalizeLocation fills Location from a 1.0 Target and mirrors a Couchbase Location into
// Target. The stores apply it on every write, and cbstore on read of documents from 1.0.
func (m *Mapping) NormalizeLocation() { m.SetLocation(m.Where()) }

// UpdatedLocation resolves an Update of cur to next: whichever of Location and the 1.0 Target
// the caller changed wins, so a 1.0 code path that edits only Target isn't overwritten by the
// Location it read.
func UpdatedLocation(cur, next Mapping) Location {
	switch {
	case next.Location != cur.Location:
		return next.Where()
	case next.Target != cur.Target:
		return next.Target.Location()
	}
	return cur.Where()
}

// Driving names the table whose rows become documents: a composed spec's root table (the
// composer can drive a mapping from any table or view, OC-047), else the mapping's own table.
func (m Mapping) Driving() (schemaName, table string) {
	var s struct {
		Mode string `json:"mode"`
		Root *struct {
			Table struct {
				Schema string `json:"schema"`
				Name   string `json:"name"`
			} `json:"table"`
		} `json:"root"`
	}
	if json.Unmarshal(m.Spec, &s) == nil && s.Mode == "composed" && s.Root != nil && s.Root.Table.Name != "" {
		return s.Root.Table.Schema, s.Root.Table.Name
	}
	return m.SourceSchema, m.SourceTable
}
