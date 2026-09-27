package mapping

import (
	"fmt"
	"sort"
	"strings"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

// Embed kinds (OC-021).
const (
	EmbedManyToOne = "many-to-one" // FK on the parent → a nested object
	EmbedOneToMany = "one-to-many" // child rows referencing the parent → an array of objects
)

// Embed limits and empty-array handling.
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

// Embed issue codes.
const (
	CodeEmbedInvalid      = "EMBED_INVALID"
	CodeEmbedTableMissing = "EMBED_TABLE_MISSING"
	CodeEmbedJoinInvalid  = "EMBED_JOIN_INVALID"
	CodeEmbedTargetKind   = "EMBED_TARGET_KIND"
	CodeEmbedDuplicate    = "EMBED_DUPLICATE_TARGET"
	CodeEmbedCardinality  = "EMBED_CARDINALITY"
	CodeEmbedDocSize      = "EMBED_DOC_SIZE"
	CodeEmbedScope        = "EMBED_SCOPE"
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
}

// Embed denormalizes a related table into the document (OC-021): many-to-one as the object
// field TargetFieldID, one-to-many as the array field TargetFieldID (items are objects). Field
// mappings inside it use sources {embed: ID, column: …}.
type Embed struct {
	ID            string        `json:"id"`
	Kind          string        `json:"kind"`
	Table         TableRef      `json:"table"`
	Join          []JoinPair    `json:"join"`
	OrderBy       []OrderColumn `json:"orderBy,omitempty"`
	Limit         int           `json:"limit,omitempty"` // one-to-many; 0 = DefaultEmbedLimit
	TargetFieldID string        `json:"targetFieldId"`
	Empty         string        `json:"empty,omitempty"` // one-to-many without rows: array (default) | omit
}

// EffectiveLimit is the per-parent item limit of a one-to-many embed.
func (e Embed) EffectiveLimit() int {
	if e.Limit <= 0 {
		return DefaultEmbedLimit
	}
	return e.Limit
}

// EmbedByID returns the embed with id, or nil.
func (s *Spec) EmbedByID(id string) *Embed {
	for i := range s.Embeds {
		if s.Embeds[i].ID == id {
			return &s.Embeds[i]
		}
	}
	return nil
}

// EmbedContainer is the object whose children are an embed's fields: the target object
// (many-to-one) or the target array's item object (one-to-many); nil when the design doesn't
// have the right shape.
func EmbedContainer(d *docschema.Schema, e Embed) *docschema.DocField {
	f := d.Find(e.TargetFieldID)
	if f == nil {
		return nil
	}
	switch e.Kind {
	case EmbedManyToOne:
		if f.Type == docschema.Object {
			return f
		}
	case EmbedOneToMany:
		if f.Type == docschema.Array && f.Items != nil && f.Items.Type == docschema.Object {
			return f.Items
		}
	}
	return nil
}

// EmbedFields maps every field inside an embed (not the embed's own target field) to the
// embed's id.
func EmbedFields(s *Spec) map[string]string {
	out := map[string]string{}
	for _, e := range s.Embeds {
		c := EmbedContainer(&s.DocSchema, e)
		if c == nil {
			continue
		}
		var walk func(f *docschema.DocField)
		walk = func(f *docschema.DocField) {
			for i := range f.Children {
				out[f.Children[i].ID] = e.ID
				walk(&f.Children[i])
			}
			if f.Items != nil && f != c {
				out[f.Items.ID] = e.ID
				walk(f.Items)
			}
		}
		walk(c)
	}
	return out
}

// Scoped is the sub-spec of one embed: its fields as the document root and its field mappings
// with plain column sources (the transform compiler and auto-map run on it).
func Scoped(s *Spec, e Embed) Spec {
	out := Spec{NullHandling: s.NullHandling, OmitUnmapped: s.OmitUnmapped, DocSchema: docschema.Schema{Root: docschema.NewRoot(), Source: s.DocSchema.Source}}
	if c := EmbedContainer(&s.DocSchema, e); c != nil {
		out.DocSchema.Root.Children = c.Children
	}
	for _, m := range s.FieldMappings {
		if !UsesEmbed(m, e.ID) {
			continue
		}
		m.Sources = append([]SourceRef(nil), m.Sources...)
		for i := range m.Sources {
			m.Sources[i].Embed = ""
		}
		out.FieldMappings = append(out.FieldMappings, m)
	}
	out.Normalize()
	return out
}

// UsesEmbed reports whether a mapping reads from embed id.
func UsesEmbed(m FieldMapping, id string) bool {
	for _, s := range m.Sources {
		if s.Embed == id {
			return true
		}
	}
	return false
}

// EmbedSources reports whether a mapping reads from any embed.
func EmbedSources(m FieldMapping) bool {
	for _, s := range m.Sources {
		if s.Embed != "" {
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
}

// Candidates lists the embeddable relations of a table: its foreign keys (many-to-one) and the
// tables whose foreign keys reference it (one-to-many), from the discovery snapshot.
func Candidates(snap *schema.Snapshot, parent *schema.Table, s *Spec) []EmbedCandidate {
	out := []EmbedCandidate{}
	existing := func(kind string, t TableRef, join []JoinPair) string {
		if s == nil {
			return ""
		}
		for _, e := range s.Embeds {
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
func EmbedEstimates(e Embed, parent, child *schema.Table, path string) []Issue {
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

// ValidateEmbeds checks each embed (OC-021 AC7): its table and join columns exist with
// compatible types, the target field has the right kind, no field is embedded twice, and the
// order columns exist. Size and cardinality estimates are warnings.
func ValidateEmbeds(s *Spec, parent *schema.Table, related RelatedTables) []Issue {
	var out []Issue
	paths := s.DocSchema.Paths()
	targets := map[string]string{}
	ids := map[string]bool{}
	for _, e := range s.Embeds {
		path := paths[e.TargetFieldID]
		if path == "" {
			path = "embeds." + e.ID
		}
		bad := func(code, msg string) {
			out = append(out, Issue{Severity: SeverityError, Code: code, Path: path, FieldID: e.TargetFieldID, Message: "Embed " + e.ID + ": " + msg})
		}
		if e.ID == "" || ids[e.ID] {
			bad(CodeEmbedInvalid, "every embed needs a unique id.")
		}
		ids[e.ID] = true
		if e.Kind != EmbedManyToOne && e.Kind != EmbedOneToMany {
			bad(CodeEmbedInvalid, "kind must be many-to-one or one-to-many.")
			continue
		}
		if prev, dup := targets[e.TargetFieldID]; dup {
			bad(CodeEmbedDuplicate, "field "+path+" is already filled by embed "+prev+".")
		}
		targets[e.TargetFieldID] = e.ID
		if s.DocSchema.Find(e.TargetFieldID) == nil {
			bad(CodeUnknownTargetField, "the target field isn't in the design.")
		} else if EmbedContainer(&s.DocSchema, e) == nil {
			want := "an object"
			if e.Kind == EmbedOneToMany {
				want = "an array of objects"
			}
			bad(CodeEmbedTargetKind, path+" must be "+want+" for a "+e.Kind+" embed.")
		}
		if e.Kind == EmbedOneToMany && (e.Limit < 0 || e.Limit > MaxEmbedLimit) {
			bad(CodeEmbedInvalid, fmt.Sprintf("limit must be between 1 and %d.", MaxEmbedLimit))
		}
		if e.Empty != "" && e.Empty != EmptyArray && e.Empty != EmptyOmit {
			bad(CodeEmbedInvalid, "empty must be array or omit.")
		}
		var child *schema.Table
		if related != nil {
			child = related(e.Table.Schema, e.Table.Name)
		}
		if child == nil {
			bad(CodeEmbedTableMissing, "table "+e.Table.String()+" isn't in the latest schema snapshot.")
			continue
		}
		if len(e.Join) == 0 {
			bad(CodeEmbedJoinInvalid, "set the join columns.")
		}
		for _, j := range e.Join {
			pc, cc := parent.Column(j.Parent), child.Column(j.Child)
			switch {
			case pc == nil:
				bad(CodeEmbedJoinInvalid, "join column "+j.Parent+" isn't in "+parent.Name+".")
			case cc == nil:
				bad(CodeEmbedJoinInvalid, "join column "+j.Child+" isn't in "+e.Table.String()+".")
			case !joinCompatible(*pc, *cc):
				bad(CodeEmbedJoinInvalid, fmt.Sprintf("%s (%s) and %s (%s) have incompatible types.", j.Parent, pc.UDTName, j.Child, cc.UDTName))
			}
		}
		for _, o := range e.OrderBy {
			if child.Column(o.Column) == nil {
				bad(CodeEmbedJoinInvalid, "order column "+o.Column+" isn't in "+e.Table.String()+".")
			}
		}
		out = append(out, EmbedEstimates(e, parent, child, path)...)
	}
	// Mappings inside embeds read that embed's table, and fill that embed's fields.
	inEmbed := EmbedFields(s)
	for _, m := range s.FieldMappings {
		if m.State == StateRejected {
			continue
		}
		path := paths[m.TargetFieldID]
		for _, src := range m.Sources {
			want := inEmbed[m.TargetFieldID]
			switch {
			case src.Embed == "" && want != "":
				out = append(out, Issue{
					Severity: SeverityError, Code: CodeEmbedScope, Path: path, FieldID: m.TargetFieldID, FieldMappingID: m.ID,
					Message: path + " is inside embed " + want + ": map it from that table's columns.",
				})
			case src.Embed != "" && src.Embed != want:
				out = append(out, Issue{
					Severity: SeverityError, Code: CodeEmbedScope, Path: path, FieldID: m.TargetFieldID, FieldMappingID: m.ID,
					Message: "Columns of embed " + src.Embed + " can only fill that embed's fields.",
				})
			}
		}
	}
	return out
}

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
