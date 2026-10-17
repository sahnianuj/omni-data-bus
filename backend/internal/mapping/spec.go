// Package mapping is a table→collection mapping's specification (architecture §6.2): the
// document design, key template, type field, field mappings and their bookkeeping (stats,
// validation, status). Transforms are compiled by internal/transform; auto-mapping lives in
// internal/automap.
package mapping

import (
	"crypto/rand"

	"omnidatabus/internal/docschema"
)

// Mapping origins and states.
const (
	OriginAuto      = "auto"
	OriginSuggested = "suggested"
	OriginManual    = "manual"

	StateActive   = "active"
	StatePending  = "pending" // a suggestion awaiting accept or reject
	StateRejected = "rejected"

	NullInclude = "include"
	NullOmit    = "omit"
)

// SpecVersion is the version this code writes. Specs without specVersion are 1.0 (v1) and are
// upgraded when decoded (UpgradeV1, OC-037).
const SpecVersion = 2

// Spec modes: edited on the mapping canvas, or composed from the target side (OC-047).
const (
	ModeCanvas   = "canvas"
	ModeComposed = "composed"
)

// Spec is stored in mappings.spec (architecture §6.1).
type Spec struct {
	SpecVersion int    `json:"specVersion"`
	Mode        string `json:"mode"`
	// Root is the mapped table; nil in specs from 1.0 (the mapping names the table).
	Root         *Root            `json:"root,omitempty"`
	DocSchema    docschema.Schema `json:"docSchema"`
	Key          KeySpec          `json:"key"`
	TypeField    TypeField        `json:"typeField"`
	NullHandling string           `json:"nullHandling"` // include | omit
	// OmitUnmapped leaves designed fields without an active mapping out of the document; by
	// default they're written as null so the document keeps its designed shape.
	OmitUnmapped bool `json:"omitUnmapped,omitempty"`
	// ReferenceStrict makes the generated documents' conformance to an imported design an
	// error rather than a warning, so it blocks migrations and exports (OC-061).
	ReferenceStrict bool           `json:"referenceStrict,omitempty"`
	ExpirySec       int64          `json:"expirySec,omitempty"`
	FieldMappings   []FieldMapping `json:"fieldMappings"`
	Relations       []Relation     `json:"relations"`
	Aggregates      []Aggregate    `json:"aggregates"`
	IgnoredColumns  []string       `json:"ignoredColumns"`
}

// KeySpec is the document key template (OC-016).
type KeySpec struct {
	Template string `json:"template"`
}

// TypeField adds a constant discriminator field to every document.
type TypeField struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
	Value   string `json:"value"`
}

// Root names the mapped (root) table.
type Root struct {
	Table TableRef `json:"table"`
	// GroupBy makes one document per distinct value of these driving-table columns, instead of
	// one per row (OC-063). Composed mappings only.
	GroupBy []string `json:"groupBy,omitempty"`
}

// SourceRef is one source of a field mapping: a column of the mapped table, or of a relation's
// table when Relation is set. From "via" reads a many-to-many junction column (OC-044).
type SourceRef struct {
	Relation string `json:"relation,omitempty"`
	Column   string `json:"column"`
	From     string `json:"from,omitempty"`
}

// Step is one transform in a field mapping's pipeline (ops: OC-020).
type Step struct {
	Op   string         `json:"op"`
	Args map[string]any `json:"args,omitempty"`
}

// FieldMapping fills one target field from zero (constant), one or several source columns.
type FieldMapping struct {
	ID            string      `json:"id"`
	TargetFieldID string      `json:"targetFieldId"`
	Sources       []SourceRef `json:"sources"`
	Transforms    []Step      `json:"transforms"`
	Origin        string      `json:"origin"`
	State         string      `json:"state"`
	Confidence    float64     `json:"confidence,omitempty"`
	Reason        string      `json:"reason,omitempty"`
	NeedsReview   bool        `json:"needsReview,omitempty"`
	NullHandling  string      `json:"nullHandling,omitempty"` // overrides Spec.NullHandling
	// Computed fills the field from the document's other fields instead of sources (OC-136):
	// a mapping with Computed has no sources and no constant or expression step.
	Computed *Computed `json:"computed,omitempty"`
}

// Computed field languages (OC-136). "js" is OC-137's (release 3.0): not runnable yet.
const (
	ComputedTemplate = "template"
	ComputedJS       = "js"
)

// Computed row-error policies.
const (
	OnErrorFailRow = "fail-row"
	OnErrorNull    = "null"
	OnErrorOmit    = "omit"
)

// Computed is a computed field's rule: a template such as `${self.firstName} ${self.lastName}`
// over doc, self, parent, index and row (OC-136).
type Computed struct {
	Lang string `json:"lang"`
	Expr string `json:"expr"`
	// NullIfAnyMissing makes the result null when any value it reads is missing or null,
	// instead of rendering those as empty strings.
	NullIfAnyMissing bool `json:"nullIfAnyMissing,omitempty"`
	// OnError: fail-row (default), null or omit.
	OnError string `json:"onError,omitempty"`
}

// Active reports whether the mapping takes part in the migration.
func (m FieldMapping) Active() bool { return m.State == StateActive }

// Target is where a mapping writes (OC-013/OC-014).
type Target struct {
	Bucket          string `json:"bucket"`
	Scope           string `json:"scope"`
	Collection      string `json:"collection"`
	CreateIfMissing bool   `json:"createIfMissing,omitempty"`
}

const mapAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// NewMappingID returns "map_" + 10 random characters.
func NewMappingID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = mapAlphabet[int(b[i])%len(mapAlphabet)]
	}
	return "map_" + string(b)
}

// Normalize fills nil slices and defaults so the spec round-trips as [] rather than null.
func (s *Spec) Normalize() {
	if s.SpecVersion == 0 {
		s.SpecVersion = SpecVersion // specs built in code are current
	}
	if s.Mode == "" {
		s.Mode = ModeCanvas
	}
	if s.Aggregates == nil {
		s.Aggregates = []Aggregate{}
	}
	if s.FieldMappings == nil {
		s.FieldMappings = []FieldMapping{}
	}
	if s.Relations == nil {
		s.Relations = []Relation{}
	}
	for i := range s.Relations {
		if s.Relations[i].Join == nil {
			s.Relations[i].Join = []JoinPair{}
		}
	}
	if s.IgnoredColumns == nil {
		s.IgnoredColumns = []string{}
	}
	if s.NullHandling == "" {
		s.NullHandling = NullInclude
	}
	if s.DocSchema.Root.ID == "" {
		s.DocSchema.Root = docschema.NewRoot()
	}
	if s.DocSchema.Root.Children == nil {
		s.DocSchema.Root.Children = []docschema.DocField{}
	}
	if s.Composed() {
		SyncGroup(s)
		SyncBindings(s)
	}
	for i := range s.FieldMappings {
		if s.FieldMappings[i].Sources == nil {
			s.FieldMappings[i].Sources = []SourceRef{}
		}
		if s.FieldMappings[i].Transforms == nil {
			s.FieldMappings[i].Transforms = []Step{}
		}
	}
}

// Ignored reports whether a column is excluded from the mapping.
func (s *Spec) Ignored(column string) bool {
	for _, c := range s.IgnoredColumns {
		if c == column {
			return true
		}
	}
	return false
}
