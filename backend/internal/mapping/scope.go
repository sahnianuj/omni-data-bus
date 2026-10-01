package mapping

import (
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

// Relation kind aliases in spec v2 terms (1.0 named them embeds).
const (
	RelationManyToOne = EmbedManyToOne
	RelationOneToMany = EmbedOneToMany
)

// Array shapes (OC-045).
const (
	ShapeObjects = "objects" // default: an array of objects
	ShapeScalars = "scalars" // an array of one column's values
	ShapeKeyed   = "keyed"   // an object keyed by one column
)

// Overflow policies past a relation's limit, and for the document size guard (OC-046).
const (
	OverflowTruncate = "truncate"
	OverflowFail     = "fail"
	OverflowSplit    = "split"
)

// Resolution strategies (OC-046).
const (
	StrategyAuto        = "auto"
	StrategyBatched     = "batched"
	StrategySortedMerge = "sorted-merge"
)

// FromVia marks a junction column (SourceRef.From, OrderColumn.From, Filter.From, OC-044).
const FromVia = "via"

const viaColumnPrefix = "__v_"

// Aggregate functions (OC-045).
var aggregateFns = map[string]bool{"count": true, "sum": true, "min": true, "max": true, "avg": true, "first": true, "last": true}

// AggregateFn reports whether fn is a known aggregate function.
func AggregateFn(fn string) bool { return aggregateFns[fn] }

// Many reports whether the relation produces several rows per parent (an array, keyed object
// or aggregate input): one-to-many and many-to-many.
func (e Relation) Many() bool { return e.Kind == EmbedOneToMany || e.Kind == RelationManyToMany }

// EffectiveShape is the relation's array shape (objects when unset).
func (e Relation) EffectiveShape() string {
	if e.Shape == "" {
		return ShapeObjects
	}
	return e.Shape
}

// EffectiveOverflow is the relation's overflow policy (truncate when unset).
func (e Relation) EffectiveOverflow() string {
	if e.Overflow == "" {
		return OverflowTruncate
	}
	return e.Overflow
}

// ViaColumn is the name under which a junction column appears in a many-to-many relation's
// rows: they carry the target table's columns followed by these (OC-044).
func ViaColumn(name string) string { return viaColumnPrefix + name }

// ViaColumns are the junction columns a many-to-many relation's rows carry, in order: the
// parent join columns (to group rows by parent), the exposed payload columns, then the order
// columns read from the junction. Nil for other kinds.
func ViaColumns(e Relation) []string {
	if e.Via == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(c string) {
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, j := range e.Via.ParentJoin {
		add(j.Via)
	}
	for _, c := range e.Via.Columns {
		add(c)
	}
	for _, o := range e.OrderBy {
		if o.From == FromVia {
			add(o.Column)
		}
	}
	return out
}

// RowTable is the table a relation's rows are read as: its table, or for many-to-many its
// table's columns followed by the junction columns named ViaColumn(c) (OC-044). junction is
// the junction table (nil for other kinds; a missing junction column is left out).
func RowTable(e Relation, t, junction *schema.Table) *schema.Table {
	if e.Kind != RelationManyToMany || e.Via == nil || t == nil {
		return t
	}
	out := *t
	out.Columns = append([]schema.Column(nil), t.Columns...)
	for _, c := range ViaColumns(e) {
		if junction == nil {
			continue
		}
		if jc := junction.Column(c); jc != nil {
			v := *jc
			v.Name = ViaColumn(c)
			v.IsPrimaryKey, v.IsUnique = false, false
			out.Columns = append(out.Columns, v)
		}
	}
	return &out
}

// FieldScopes maps every designed field to the relation whose rows fill it ("" = the root
// row): the fields inside a relation's container (its object, its array's items, or its keyed
// object) belong to it, down to the containers of nested relations. A relation's own target
// field belongs to the scope it's placed in, which must be its parent's (OC-043).
func FieldScopes(s *Spec) map[string]string {
	byTarget := map[string]*Relation{}
	for i := range s.Relations {
		r := &s.Relations[i]
		if !r.LookupOnly && r.TargetFieldID != "" {
			byTarget[r.TargetFieldID] = r
		}
	}
	out := map[string]string{}
	var walk func(f *docschema.DocField, scope string)
	walk = func(f *docschema.DocField, scope string) {
		for i := range f.Children {
			c := &f.Children[i]
			out[c.ID] = scope
			inner := scope
			if r := byTarget[c.ID]; r != nil {
				inner = r.ID
			}
			walk(c, inner)
			if c.Items != nil {
				out[c.Items.ID] = inner
				walk(c.Items, inner)
			}
		}
	}
	walk(&s.DocSchema.Root, "")
	return out
}

// ScopeChain lists the relations whose rows a field in scope can read, nearest first: the
// scope's relation, then its ancestors up to (not including) the root. Their lookup-only
// children are in scope too (see InScope).
func ScopeChain(s *Spec, scope string) []string {
	var out []string
	seen := map[string]bool{}
	for id := scope; id != "" && !seen[id]; {
		seen[id] = true
		out = append(out, id)
		r := s.RelationByID(id)
		if r == nil {
			break
		}
		id = r.Parent()
	}
	return out
}

// InScope reports whether a field in scope may read relation rel's columns (architecture §6.1
// scope rule): the root table (""), a relation on the path from the root to the field, or a
// lookup-only relation attached to that path.
func InScope(s *Spec, scope, rel string) bool {
	if rel == "" {
		return true
	}
	chain := ScopeChain(s, scope)
	for _, id := range chain {
		if id == rel {
			return true
		}
	}
	r := s.RelationByID(rel)
	if r == nil || !r.LookupOnly {
		return false
	}
	p := r.Parent()
	if p == "" {
		return true
	}
	for _, id := range chain {
		if id == p {
			return true
		}
	}
	return false
}

// ScopeOf is the scope a mapping writes in: its target field's.
func ScopeOf(scopes map[string]string, m FieldMapping) string { return scopes[m.TargetFieldID] }

// ScopeEntry is one table a field may read (OC-047): the root table (RelationID ""), a relation
// bound on the path to the field, or a lookup-only relation attached to that path.
type ScopeEntry struct {
	RelationID string   `json:"relationId"`
	Table      TableRef `json:"table"`
	Lookup     bool     `json:"lookup,omitempty"`
	// Via is a many-to-many relation's junction; its payload columns are in scope too.
	Via *TableRef `json:"via,omitempty"`
}

// Scope lists what a field can be mapped from, outermost first: the root table, then each
// relation bound on the path from the root to the field, each followed by the lookup-only
// relations attached to it (the root's lookups follow the root). The composer's inspector lists
// only these tables' columns; web/src/features/composer/scope.ts mirrors it (shared fixtures in
// testdata/scope-cases.json).
func Scope(s *Spec, fieldID string) []ScopeEntry {
	scope, ok := FieldScopes(s)[fieldID]
	if !ok {
		return nil
	}
	chain := ScopeChain(s, scope)
	levels := []string{""}
	for i := len(chain) - 1; i >= 0; i-- {
		levels = append(levels, chain[i])
	}
	var out []ScopeEntry
	for _, id := range levels {
		if id == "" {
			e := ScopeEntry{}
			if s.Root != nil {
				e.Table = s.Root.Table
			}
			out = append(out, e)
		} else {
			r := s.RelationByID(id)
			if r == nil {
				continue
			}
			e := ScopeEntry{RelationID: id, Table: r.Table}
			if r.Via != nil && len(r.Via.Columns) > 0 {
				v := r.Via.Table
				e.Via = &v
			}
			out = append(out, e)
		}
		for _, r := range s.Relations {
			if r.LookupOnly && r.Parent() == id {
				out = append(out, ScopeEntry{RelationID: r.ID, Table: r.Table, Lookup: true})
			}
		}
	}
	return out
}
