package mapping

import (
	"errors"
	"fmt"
	"strings"

	"omnidatabus/internal/docschema"
)

// Document composer issue codes (OC-047, OC-049).
const (
	// CodeSourceOutOfScope: a mapping (or an expression) reads a relation that isn't bound on
	// the path to its field, nor looked up there.
	CodeSourceOutOfScope = "SOURCE_OUT_OF_SCOPE"
	// CodeBindingKindMismatch: a node's type doesn't take its relation's cardinality, e.g. an
	// object bound to one-to-many without the first adapter.
	CodeBindingKindMismatch = "BINDING_KIND_MISMATCH"
	// CodeBindingBroken: a relation's parent join no longer resolves (the driving table or the
	// schema changed). The relation is kept so it can be fixed.
	CodeBindingBroken = "BINDING_BROKEN"
	// CodeArrayUnboundEmpty: an unbound array has no element mappings, so it's always empty.
	CodeArrayUnboundEmpty = "ARRAY_UNBOUND_EMPTY"
	// CodeDocSizeRisk: documents may exceed 10 MB (the composer's name for EMBED_DOC_SIZE).
	CodeDocSizeRisk = "DOC_SIZE_RISK"
)

// Composed reports whether the spec is designed target-first in the document composer.
func (s *Spec) Composed() bool { return s.Mode == ModeComposed }

func adapterOf(f *docschema.DocField) string {
	if f == nil || f.Binding == nil {
		return ""
	}
	return f.Binding.Adapter
}

// Adapter is the composer adapter on a relation's target field ("" when none).
func Adapter(d *docschema.Schema, e Relation) string {
	if e.LookupOnly || e.TargetFieldID == "" {
		return ""
	}
	return adapterOf(d.Find(e.TargetFieldID))
}

// SyncBindings makes every node's binding mirror the relations (a relation's targetFieldId is
// the binding, OC-047): bound nodes get their relation's id, and a binding whose relation is
// gone is removed. Adapters stay with bound nodes.
func SyncBindings(s *Spec) {
	bound := map[string]string{}
	for _, r := range s.Relations {
		if !r.LookupOnly && r.TargetFieldID != "" {
			bound[r.TargetFieldID] = r.ID
		}
	}
	s.DocSchema.Walk(func(f *docschema.DocField, _ string, _ *docschema.DocField) {
		switch id, ok := bound[f.ID]; {
		case ok && f.Binding == nil:
			f.Binding = &docschema.Binding{RelationID: id}
		case ok:
			f.Binding.RelationID = id
		default:
			f.Binding = nil
		}
	})
}

// ErrNotComposed is returned when a canvas mapping tries to change its driving table.
var ErrNotComposed = errors.New("only a composed mapping can change its driving table")

// SetDrivingTable makes t the table that drives a composed mapping (one document per row,
// OC-047). Relations aren't touched: those whose parent join no longer resolves are reported
// as BINDING_BROKEN by validation, so they can be fixed rather than lost.
func SetDrivingTable(s *Spec, t TableRef) error {
	if !s.Composed() {
		return ErrNotComposed
	}
	if t.Schema == "" || t.Name == "" {
		return fmt.Errorf("the driving table needs a schema and a name")
	}
	s.Root = &Root{Table: t}
	return nil
}

// DrivingTable is the table whose rows become documents: the composed spec's root table, else
// the mapping's own table (fallback).
func DrivingTable(s *Spec, fallback TableRef) TableRef {
	if s.Composed() && s.Root != nil && s.Root.Table.Name != "" {
		return s.Root.Table
	}
	return fallback
}

// validateComposed checks the composer's own rules (OC-047, OC-049): known adapters, and
// unbound arrays that have nothing to fill them.
func validateComposed(s *Spec) []Issue {
	var out []Issue
	bound := map[string]bool{}
	for _, r := range s.Relations {
		if !r.LookupOnly && r.TargetFieldID != "" {
			bound[r.TargetFieldID] = true
		}
	}
	mapped := map[string]bool{}
	for _, m := range s.FieldMappings {
		if m.Active() {
			mapped[m.TargetFieldID] = true
		}
	}
	for _, a := range s.Aggregates {
		mapped[a.TargetFieldID] = true
	}
	s.DocSchema.Walk(func(f *docschema.DocField, path string, parent *docschema.DocField) {
		if parent != nil && parent.Type == docschema.Array {
			return // an array's item is filled with its array
		}
		switch a := adapterOf(f); a {
		case "", docschema.AdapterFirst, docschema.AdapterWrap:
		default:
			out = append(out, Issue{
				Severity: SeverityError, Code: CodeBindingKindMismatch, Path: path, FieldID: f.ID,
				Message: "Unknown adapter '" + a + "' on " + path + "; use first or wrap.",
			})
		}
		if f.Type == docschema.Array && !bound[f.ID] && !mapped[f.ID] {
			out = append(out, Issue{
				Severity: SeverityWarning, Code: CodeArrayUnboundEmpty, Path: path, FieldID: f.ID,
				Message: path + " isn't bound to a relation and has no element mappings, so it's always empty. Bind it to a table, or map its elements.",
			})
		}
	})
	return out
}

// bindingMismatch explains why a relation can't fill its node (BINDING_KIND_MISMATCH).
func bindingMismatch(s *Spec, e Relation, path string) string {
	f := s.DocSchema.Find(e.TargetFieldID)
	adapter := adapterOf(f)
	table := e.Table.Name
	switch {
	case adapter == docschema.AdapterFirst && !e.Many():
		return path + " uses the first adapter, but " + table + " is " + e.Kind + " (at most one row); remove the adapter."
	case adapter == docschema.AdapterFirst:
		return path + " uses the first adapter and must be an object."
	case adapter == docschema.AdapterWrap && e.Many():
		return path + " uses the wrap adapter, but " + table + " is " + e.Kind + "; an array takes its rows without an adapter."
	case adapter == docschema.AdapterWrap:
		return path + " uses the wrap adapter and must be an array of objects."
	case f != nil && f.Type == docschema.Object && e.Many():
		return path + " is an object, but " + table + " is " + e.Kind + "; bind it to an array, or use the first adapter to keep the first row."
	case f != nil && f.Type == docschema.Array && !e.Many():
		return path + " is an array, but " + table + " is " + e.Kind + "; bind it to an object, or use the wrap adapter for a 0- or 1-item array."
	}
	return path + " must be " + wantShape(e) + " for this relation."
}

// OutOfScopeMessage explains SOURCE_OUT_OF_SCOPE: "items[].productName reads products.name, but
// products isn't bound on the path to this field; bind it under items or add a lookup."
func OutOfScopeMessage(s *Spec, paths map[string]string, path, scope, rel, column string) string {
	table := rel
	if r := s.RelationByID(rel); r != nil {
		table = r.Table.Name
	}
	where := "at the root"
	if r := s.RelationByID(scope); r != nil && paths[r.TargetFieldID] != "" {
		where = "under " + paths[r.TargetFieldID]
	}
	return fmt.Sprintf("%s reads %s.%s, but %s isn't bound on the path to this field; bind it %s or add a lookup.", path, table, column, table, where)
}

// ExprIdentifiers are the names an expression's text uses outside string literals (e.g.
// $root, items, line_no). Plans use it to read every column of the tables an expression may
// reach through $root, $parent or a relation's name (OC-047); over-reading is harmless.
func ExprIdentifiers(src string) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			j := i + 1
			for j < len(src) && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			i = j + 1
		case c == '$' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i + 1
			for j < len(src) && (src[j] == '_' || src[j] == '$' || src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z' || src[j] >= '0' && src[j] <= '9') {
				j++
			}
			out[src[i:j]] = true
			i = j
		default:
			i++
		}
	}
	return out
}

// Expressions are the active mappings' expression sources.
func Expressions(s *Spec) []string {
	var out []string
	for _, m := range s.FieldMappings {
		if !m.Active() {
			continue
		}
		for _, st := range m.Transforms {
			if st.Op == "expression" {
				if src, ok := st.Args["expr"].(string); ok {
					out = append(out, src)
				}
			}
		}
	}
	return out
}

// CanonicalField is a design node without the composer's derived binding ids (a binding is
// its relation's targetFieldId), keeping adapters, which change the document. Plan hashes use
// it so a composed mapping and the equivalent canvas mapping hash equal (OC-047 AC5).
func CanonicalField(f docschema.DocField) docschema.DocField {
	if f.Binding != nil {
		if f.Binding.Adapter == "" {
			f.Binding = nil
		} else {
			f.Binding = &docschema.Binding{Adapter: f.Binding.Adapter}
		}
	}
	if f.Children != nil {
		kids := make([]docschema.DocField, len(f.Children))
		for i, c := range f.Children {
			kids[i] = CanonicalField(c)
		}
		f.Children = kids
	}
	if f.Items != nil {
		it := CanonicalField(*f.Items)
		f.Items = &it
	}
	return f
}

// Annotate sets each issue's node (OC-049): the design node it's on, and the relation when that
// node is bound (or the issue is about a relation without a field).
func Annotate(s *Spec, issues []Issue) {
	paths := s.DocSchema.Paths()
	bound := map[string]string{}
	for _, r := range s.Relations {
		if !r.LookupOnly && r.TargetFieldID != "" {
			bound[r.TargetFieldID] = r.ID
		}
	}
	known := map[string]bool{}
	for _, p := range paths {
		known[p] = true
	}
	for i := range issues {
		is := &issues[i]
		switch {
		case is.FieldID != "" && paths[is.FieldID] != "":
			is.NodePath = paths[is.FieldID]
			if r := bound[is.FieldID]; r != "" && is.RelationID == "" {
				is.RelationID = r
			}
		case strings.HasPrefix(is.Path, "relations."):
			is.RelationID = strings.TrimPrefix(is.Path, "relations.")
		case known[is.Path]:
			is.NodePath = is.Path
		}
	}
}
