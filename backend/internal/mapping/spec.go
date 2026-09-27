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

// Spec is stored in mappings.spec.
type Spec struct {
	DocSchema    docschema.Schema `json:"docSchema"`
	Key          KeySpec          `json:"key"`
	TypeField    TypeField        `json:"typeField"`
	NullHandling string           `json:"nullHandling"` // include | omit
	// OmitUnmapped leaves designed fields without an active mapping out of the document; by
	// default they're written as null so the document keeps its designed shape.
	OmitUnmapped   bool           `json:"omitUnmapped,omitempty"`
	ExpirySec      int64          `json:"expirySec,omitempty"`
	FieldMappings  []FieldMapping `json:"fieldMappings"`
	Embeds         []Embed        `json:"embeds"` // OC-021
	IgnoredColumns []string       `json:"ignoredColumns"`
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

// SourceRef is one source of a field mapping: a column of the mapped table, or of an embedded
// table when Embed is set (OC-021).
type SourceRef struct {
	Column string `json:"column"`
	Embed  string `json:"embed,omitempty"`
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
	if s.FieldMappings == nil {
		s.FieldMappings = []FieldMapping{}
	}
	if s.Embeds == nil {
		s.Embeds = []Embed{}
	}
	for i := range s.Embeds {
		if s.Embeds[i].Join == nil {
			s.Embeds[i].Join = []JoinPair{}
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
