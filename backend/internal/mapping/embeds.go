package mapping

import (
	"fmt"
	"sort"
	"strings"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

// Relation kinds (OC-021; many-to-many from OC-044).
const (
	EmbedManyToOne     = "many-to-one"  // FK on the parent → a nested object
	EmbedOneToMany     = "one-to-many"  // child rows referencing the parent → an array of objects
	RelationManyToMany = "many-to-many" // through a junction table → an array
)

// Relation limits and empty-array handling.
const (
	DefaultEmbedLimit = 1000
	MaxEmbedLimit     = 100_000
	// SQLLimitThreshold: at or below it, the per-parent limit is applied in SQL (ROW_NUMBER).
	SQLLimitThreshold = 100
	EmptyArray        = "array" // an empty one-to-many is written as []
	EmptyOmit         = "omit"  // … or left out
	// EmbedDocWarnBytes is the estimated document size that raises a warning (Couchbase's
	// limit is 20 MB).
	EmbedDocWarnBytes = 10 << 20
)

// Relation issue codes.
const (
	CodeEmbedInvalid      = "EMBED_INVALID"
	CodeEmbedTableMissing = "EMBED_TABLE_MISSING"
	CodeEmbedJoinInvalid  = "EMBED_JOIN_INVALID"
	CodeEmbedTargetKind   = "EMBED_TARGET_KIND"
	CodeEmbedDuplicate    = "EMBED_DUPLICATE_TARGET"
	CodeEmbedCardinality  = "EMBED_CARDINALITY"
	CodeEmbedDocSize      = "EMBED_DOC_SIZE"
	// CodeEmbedScope is the 1.0 name of the scope rule's code; 2.0 reports SOURCE_OUT_OF_SCOPE
	// for canvas and composed mappings alike (OC-047).
	CodeEmbedScope = CodeSourceOutOfScope
)

// TableRef names a source table.
type TableRef struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

func (t TableRef) String() string { return t.Schema + "." + t.Name }

// JoinPair matches a parent column with a column of the embedded table.
type JoinPair struct {
	Parent string `json:"parent"`
	Child  string `json:"child"`
}

// OrderColumn orders a one-to-many embed's items.
type OrderColumn struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
	From   string `json:"from,omitempty"` // "via": a many-to-many junction column (OC-044)
}

// Relation brings a related table into the document (spec v2, OC-037; 1.0's embeds are
// relations at depth 1): many-to-one as the object field TargetFieldID, one-to-many and
// many-to-many as the array field TargetFieldID. Relations form a tree through ParentID (nil =
// a child of the root table). Field mappings read a relation's columns with sources
// {relation: ID, column: …}.
type Relation struct {
	ID       string     `json:"id"`
	ParentID *string    `json:"parentId"`
	Kind     string     `json:"kind"`
	Table    TableRef   `json:"table"`
	Join     []JoinPair `json:"join,omitempty"`
	// Via is the junction table of a many-to-many relation (OC-044).
	Via           *Via   `json:"via,omitempty"`
	TargetFieldID string `json:"targetFieldId,omitempty"`
	// LookupOnly relations create no field; their columns can be mapped anywhere in scope.
	LookupOnly bool `json:"lookupOnly,omitempty"`
	// Shape of an array relation: objects (default) | scalars (ValueColumn) | keyed (KeyColumn), OC-045.
	Shape       string        `json:"shape,omitempty"`
	ValueColumn string        `json:"valueColumn,omitempty"`
	KeyColumn   string        `json:"keyColumn,omitempty"`
	OrderBy     []OrderColumn `json:"orderBy,omitempty"`
	Limit       int           `json:"limit,omitempty"` // arrays; 0 = DefaultEmbedLimit
	Filter      []Filter      `json:"filter,omitempty"`
	// Overflow past Limit: truncate (default) | fail | split, OC-046.
	Overflow string `json:"overflow,omitempty"`
	// Strategy: auto (default) | batched | sorted-merge, OC-046.
	Strategy string `json:"strategy,omitempty"`
	Empty    string `json:"empty,omitempty"` // an array without rows: array (default) | omit
	// Distinct keeps one item per target row and parent (many-to-many, OC-044); the junction
	// columns then come from the first row in order.
	Distinct bool `json:"distinct,omitempty"`
	// KeepDangling keeps junction rows whose target row is missing, with null target columns
	// (many-to-many; they're dropped with RELATION_DANGLING by default).
	KeepDangling bool `json:"keepDangling,omitempty"`
	// Group marks the relation holding a grouped document's rows (OC-063): a one-to-many from
	// the driving table to itself on the root's group-by columns, kept in sync by SyncGroup.
	Group bool `json:"group,omitempty"`
}

// Parent is the parent relation's ID, "" for a child of the root table.
func (e Relation) Parent() string {
	if e.ParentID == nil {
		return ""
	}
	return *e.ParentID
}

// Via is a many-to-many relation's junction table (OC-044).
type Via struct {
	Table      TableRef  `json:"table"`
	ParentJoin []ViaJoin `json:"parentJoin"`
	ChildJoin  []ViaJoin `json:"childJoin"`
	// Columns of the junction exposed to field mappings (sources with from: "via").
	Columns []string `json:"columns,omitempty"`
}

// ViaJoin pairs a junction column with a parent (ParentJoin) or child (ChildJoin) column.
type ViaJoin struct {
	Parent string `json:"parent,omitempty"`
	Via    string `json:"via"`
	Child  string `json:"child,omitempty"`
}

// Filter is one condition on a relation's rows, pushed down as a bind parameter (OC-045).
type Filter struct {
	Column string `json:"column"`
	Op     string `json:"op"` // eq ne lt lte gt gte in notIn isNull notNull like
	Value  any    `json:"value,omitempty"`
	From   string `json:"from,omitempty"` // "via": a junction column
}

// Aggregate writes a function over a relation's rows into a scalar field (OC-045).
type Aggregate struct {
	ID            string `json:"id"`
	RelationID    string `json:"relationId"`
	Fn            string `json:"fn"` // count | sum | min | max | avg | first | last
	Column        string `json:"column,omitempty"`
	From          string `json:"from,omitempty"` // "via": a many-to-many junction column
	TargetFieldID string `json:"targetFieldId"`
}

// EffectiveLimit is the per-parent item limit of a one-to-many embed.
func (e Relation) EffectiveLimit() int {
	if e.Limit <= 0 {
		return DefaultEmbedLimit
	}
	return e.Limit
}

// RelationByID returns the embed with id, or nil.
func (s *Spec) RelationByID(id string) *Relation {
	for i := range s.Relations {
		if s.Relations[i].ID == id {
			return &s.Relations[i]
		}
	}
	return nil
}

// RelationContainer is the object whose children are a relation's fields: the target object
// (many-to-one, or a keyed shape), or the target array's item object (one-to-many and
// many-to-many as objects); nil when the design doesn't have the right shape, and for a
// scalars shape (see RelationValueField) or a relation without a field. With a composer
// adapter (OC-047) a many relation fills an object (first) and a many-to-one relation fills an
// array's item object (wrap).
func RelationContainer(d *docschema.Schema, e Relation) *docschema.DocField {
	if e.LookupOnly || e.TargetFieldID == "" {
		return nil
	}
	f := d.Find(e.TargetFieldID)
	if f == nil {
		return nil
	}
	switch adapterOf(f) {
	case "":
	case docschema.AdapterFirst:
		if e.Many() && f.Type == docschema.Object {
			return f
		}
		return nil
	case docschema.AdapterWrap:
		if e.Kind == EmbedManyToOne && f.Type == docschema.Array && f.Items != nil && f.Items.Type == docschema.Object {
			return f.Items
		}
		return nil
	default:
		return nil
	}
	switch {
	case e.Kind == EmbedManyToOne:
		if f.Type == docschema.Object {
			return f
		}
	case !e.Many():
	case e.EffectiveShape() == ShapeKeyed:
		if f.Type == docschema.Object {
			return f
		}
	case e.EffectiveShape() == ShapeObjects:
		if f.Type == docschema.Array && f.Items != nil && f.Items.Type == docschema.Object {
			return f.Items
		}
	}
	return nil
}

// RelationValueField is the item field of a scalars-shaped relation's array (OC-045): a
// mapping on it can transform the value column; nil when the design isn't an array of scalars.
func RelationValueField(d *docschema.Schema, e Relation) *docschema.DocField {
	if !e.Many() || e.EffectiveShape() != ShapeScalars || e.TargetFieldID == "" {
		return nil
	}
	f := d.Find(e.TargetFieldID)
	if f == nil || adapterOf(f) != "" || f.Type != docschema.Array || f.Items == nil || !f.Items.Type.Scalar() {
		return nil
	}
	return f.Items
}

// RelationFields maps every field inside a relation (not the relation's own target field) to
// the relation whose rows fill it: the nearest one, for fields of nested relations (OC-043).
func RelationFields(s *Spec) map[string]string {
	containers := map[string]bool{}
	for _, e := range s.Relations {
		if c := RelationContainer(&s.DocSchema, e); c != nil {
			containers[c.ID] = true // an array's item object holds the fields, it isn't one
		}
	}
	out := map[string]string{}
	for id, scope := range FieldScopes(s) {
		if scope != "" && !containers[id] {
			out[id] = scope
		}
	}
	return out
}

// Scoped is the sub-spec of one relation: its container's fields as the document root and the
// mappings of fields in its scope, whose sources of this relation become plain columns (the
// auto-mapper runs on it). Sources of other relations (ancestors, lookups) keep their relation.
func Scoped(s *Spec, e Relation) Spec {
	out := Spec{NullHandling: s.NullHandling, OmitUnmapped: s.OmitUnmapped, DocSchema: docschema.Schema{Root: docschema.NewRoot(), Source: s.DocSchema.Source}}
	if c := RelationContainer(&s.DocSchema, e); c != nil {
		out.DocSchema.Root.Children = c.Children
	}
	scopes := FieldScopes(s)
	for _, m := range s.FieldMappings {
		if scopes[m.TargetFieldID] != e.ID && !UsesRelation(m, e.ID) {
			continue
		}
		m.Sources = append([]SourceRef(nil), m.Sources...)
		for i := range m.Sources {
			if m.Sources[i].Relation == e.ID {
				m.Sources[i].Relation = ""
			}
		}
		out.FieldMappings = append(out.FieldMappings, m)
	}
	out.Normalize()
	return out
}

// UsesRelation reports whether a mapping reads from embed id.
func UsesRelation(m FieldMapping, id string) bool {
	for _, s := range m.Sources {
		if s.Relation == id {
			return true
		}
	}
	return false
}

// UsesAnyRelation reports whether a mapping reads from any embed.
func UsesAnyRelation(m FieldMapping) bool {
	for _, s := range m.Sources {
		if s.Relation != "" {
			return true
		}
	}
	return false
}

// EmbedCandidate is a related table the mapped table can embed (OC-021 AC1).
type EmbedCandidate struct {
	Kind       string     `json:"kind"`
	Table      TableRef   `json:"table"`
	Join       []JoinPair `json:"join"`
	Constraint string     `json:"constraint"`
	// RowEstimate is the related table's estimate; RowsPerParent (one-to-many) is the average
	// number of child rows per parent row.
	RowEstimate   *int64  `json:"rowEstimate,omitempty"`
	RowsPerParent float64 `json:"rowsPerParent,omitempty"`
	// EmbedID is set when the spec already embeds this relation.
	EmbedID string `json:"embedId,omitempty"`
	// Virtual is the ID of the virtual relationship this candidate comes from (OC-042).
	Virtual string `json:"virtual,omitempty"`
}

// Candidates lists the embeddable relations of a table: its foreign keys (many-to-one) and the
// tables whose foreign keys reference it (one-to-many), from the discovery snapshot.
func Candidates(snap *schema.Snapshot, parent *schema.Table, s *Spec) []EmbedCandidate {
	out := []EmbedCandidate{}
	existing := func(kind string, t TableRef, join []JoinPair) string {
		if s == nil {
			return ""
		}
		for _, e := range s.Relations {
			if e.Kind == kind && e.Table == t && sameJoin(e.Join, join) {
				return e.ID
			}
		}
		return ""
	}
	for _, fk := range parent.ForeignKeys {
		c := EmbedCandidate{Kind: EmbedManyToOne, Table: TableRef{fk.RefSchema, fk.RefTable}, Constraint: fk.Name}
		for i := range fk.Columns {
			if i < len(fk.RefColumns) {
				c.Join = append(c.Join, JoinPair{Parent: fk.Columns[i], Child: fk.RefColumns[i]})
			}
		}
		if t := snap.Table(fk.RefSchema, fk.RefTable); t != nil {
			c.RowEstimate = t.RowEstimate
		}
		c.EmbedID = existing(c.Kind, c.Table, c.Join)
		out = append(out, c)
	}
	for _, ref := range parent.ReferencedBy {
		c := EmbedCandidate{Kind: EmbedOneToMany, Table: TableRef{ref.Schema, ref.Table}, Constraint: ref.Name}
		for i := range ref.Columns {
			if i < len(ref.RefColumns) {
				c.Join = append(c.Join, JoinPair{Parent: ref.RefColumns[i], Child: ref.Columns[i]})
			}
		}
		if t := snap.Table(ref.Schema, ref.Table); t != nil {
			c.RowEstimate = t.RowEstimate
			c.RowsPerParent = rowsPerParent(parent, t)
		}
		c.EmbedID = existing(c.Kind, c.Table, c.Join)
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == EmbedManyToOne
		}
		return out[i].Table.String() < out[j].Table.String()
	})
	return out
}

func sameJoin(a, b []JoinPair) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func rowsPerParent(parent, child *schema.Table) float64 {
	if parent.RowEstimate == nil || child.RowEstimate == nil || *parent.RowEstimate <= 0 {
		return 0
	}
	return float64(*child.RowEstimate) / float64(*parent.RowEstimate)
}

func avgRowBytes(t *schema.Table) float64 {
	if t.RowEstimate == nil || *t.RowEstimate <= 0 || t.SizeBytes <= 0 {
		return 0
	}
	return float64(t.SizeBytes) / float64(*t.RowEstimate)
}

// EmbedEstimates warns (never errors) about an embed that may produce huge arrays or documents
// (OC-021 AC6): more child rows per parent than the limit, or an estimated document over 10 MB.
func EmbedEstimates(e Relation, parent, child *schema.Table, path string) []Issue {
	if e.Kind != EmbedOneToMany || parent == nil || child == nil {
		return nil
	}
	var out []Issue
	per := rowsPerParent(parent, child)
	if limit := e.EffectiveLimit(); per > float64(limit) {
		out = append(out, Issue{
			Severity: SeverityWarning, Code: CodeEmbedCardinality, Path: path, FieldID: e.TargetFieldID,
			Message: fmt.Sprintf("%s has about %.0f rows per %s row, more than the limit of %d: arrays will be truncated.", e.Table, per, parent.Name, limit),
		})
	}
	items := min(per, float64(e.EffectiveLimit()))
	if est := avgRowBytes(parent) + items*avgRowBytes(child); est > EmbedDocWarnBytes {
		out = append(out, Issue{
			Severity: SeverityWarning, Code: CodeEmbedDocSize, Path: path, FieldID: e.TargetFieldID,
			Message: fmt.Sprintf("Documents may reach about %.1f MB with %s embedded (Couchbase allows 20 MB); lower the limit.", est/(1<<20), e.Table),
		})
	}
	return out
}

// RelatedTables resolves an embed's table (the snapshot, in the service).
type RelatedTables func(schemaName, table string) *schema.Table

// joinCompatible: the same type, or both integers, or both text-like.
func joinCompatible(a, b schema.Column) bool {
	if a.UDTName == b.UDTName {
		return true
	}
	family := func(c schema.Column) string {
		switch c.UDTName {
		case "int2", "int4", "int8":
			return "int"
		case "text", "varchar", "bpchar", "citext", "name":
			return "text"
		case "numeric", "float4", "float8":
			return "num"
		case "timestamp", "timestamptz":
			return "ts"
		}
		return c.UDTName
	}
	return family(a) == family(b)
}

// EmbedID returns a new embed id, "emb_" + 8 random characters.
func EmbedID() string { return "emb_" + strings.TrimPrefix(NewMappingID(), "map_")[:8] }
