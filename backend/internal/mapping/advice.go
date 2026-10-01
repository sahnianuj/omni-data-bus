package mapping

import (
	"fmt"
	"strings"

	"omnidatabus/internal/schema"
)

// CodeRelationNoIndex warns that a relation's lookups will scan the child table (OC-042); it's
// an error for an explicit sorted-merge relation (OC-046).
const CodeRelationNoIndex = "RELATION_NO_INDEX"

// JoinCompatible reports whether two columns can be joined: the same type, or both integers,
// both text-like, both numeric or both timestamps (OC-020 coercion families).
func JoinCompatible(a, b schema.Column) bool { return joinCompatible(a, b) }

// LeadingIndex reports whether columns (in any order) are the leading columns of one of the
// table's indexes or its primary key.
func LeadingIndex(t *schema.Table, columns []string) bool {
	if t == nil || len(columns) == 0 {
		return false
	}
	leads := func(idx []string) bool {
		if len(idx) < len(columns) {
			return false
		}
		want := map[string]bool{}
		for _, c := range columns {
			want[c] = true
		}
		for _, c := range idx[:len(columns)] {
			if !want[c] {
				return false
			}
		}
		return true
	}
	if leads(t.PrimaryKey) {
		return true
	}
	for _, ix := range t.Indexes {
		if leads(ix.Columns) {
			return true
		}
	}
	return false
}

// IndexAdvice is the RELATION_NO_INDEX message for a lookup on table.columns ("" when they're
// indexed).
func IndexAdvice(t *schema.Table, columns []string) string {
	if t == nil || LeadingIndex(t, columns) {
		return ""
	}
	cols := strings.Join(columns, ", ")
	return fmt.Sprintf("Lookups on %s.%s will scan the table; consider `CREATE INDEX ON %s(%s)`.", t.Name, cols, t.Name, cols)
}

// lookupColumns are the columns a one-to-many or many-to-many relation looks its rows up by:
// the child's join columns, or the junction's parent-side columns.
func lookupColumns(e Relation) []string {
	var out []string
	if e.Kind == RelationManyToMany && e.Via != nil {
		for _, j := range e.Via.ParentJoin {
			out = append(out, j.Via)
		}
		return out
	}
	for _, j := range e.Join {
		out = append(out, j.Child)
	}
	return out
}

// relationIndexAdvice checks that each one-to-many or many-to-many relation's lookups use an
// index (OC-042 index advice): a warning, or an error for an explicit sorted-merge relation.
func relationIndexAdvice(s *Spec, tables RelationTables, paths map[string]string) []Issue {
	var out []Issue
	for _, e := range s.Relations {
		if !e.Many() {
			continue
		}
		t := tables.Table[e.ID]
		if e.Kind == RelationManyToMany {
			t = tables.Junction[e.ID]
		}
		msg := IndexAdvice(t, lookupColumns(e))
		if msg == "" {
			continue
		}
		sev := SeverityWarning
		if e.Strategy == StrategySortedMerge {
			sev, msg = SeverityError, msg+" Sorted-merge needs that index; add it or choose batched."
		}
		path := paths[e.TargetFieldID]
		if path == "" {
			path = "relations." + e.ID
		}
		out = append(out, Issue{Severity: sev, Code: CodeRelationNoIndex, Path: path, FieldID: e.TargetFieldID, Message: msg})
	}
	return out
}
