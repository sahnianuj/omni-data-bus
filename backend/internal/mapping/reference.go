package mapping

import (
	"fmt"
	"sort"
	"strings"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

// Conformance to an imported design (OC-061): the customer's fields, recorded at import
// (docSchema.imported.fields), against what the mapping now produces.
const (
	// CodeReferenceFieldUnmapped: a field of the imported design is missing, or has no mapping,
	// constant, aggregate or binding. Info (customer designs often hold fields the source doesn't
	// fill, OC-075); an error when the mapping is strict.
	CodeReferenceFieldUnmapped = "REFERENCE_FIELD_UNMAPPED"
	// CodeReferenceTypeMismatch: the design's field has another type than the imported one.
	CodeReferenceTypeMismatch = "REFERENCE_TYPE_MISMATCH"
	// CodeReferenceExtraField: a field added in the tool that isn't in the imported design.
	CodeReferenceExtraField = "REFERENCE_EXTRA_FIELD"
	// CodeReferenceNullable: a required imported field is filled from a nullable column
	// without a "default" transform.
	CodeReferenceNullable = "REFERENCE_NULLABLE"
)

// ReferenceLeaf reports whether a contract or design field is checked on its own: scalars,
// and arrays of scalars (filled whole by a binding or a literal array); not containers.
func referenceLeaf(path string, t docschema.FieldType, items *docschema.DocField) bool {
	if strings.HasSuffix(path, "[]") {
		return false
	}
	if t == docschema.Array {
		return items != nil && items.Type.Scalar()
	}
	return t.Scalar()
}

func validateReference(s *Spec, ctx Context) []Issue {
	imp := s.DocSchema.Imported
	if imp == nil || len(imp.Fields) == 0 {
		return nil
	}
	loose := SeverityInfo
	if s.ReferenceStrict {
		loose = SeverityError
	}
	design := map[string]*docschema.DocField{}
	s.DocSchema.Walk(func(f *docschema.DocField, p string, _ *docschema.DocField) { design[p] = f })
	mustMap := RequiredToMap(s)
	contract := map[string]docschema.ContractField{}
	for _, c := range imp.Fields {
		contract[c.Path] = c
	}
	// Which fields something fills.
	filled := map[string]*FieldMapping{}
	for i := range s.FieldMappings {
		if m := &s.FieldMappings[i]; m.Active() {
			filled[m.TargetFieldID] = m
		}
	}
	other := map[string]bool{}
	for _, a := range s.Aggregates {
		other[a.TargetFieldID] = true
	}
	for _, r := range s.Relations {
		if !r.LookupOnly && r.TargetFieldID != "" {
			other[r.TargetFieldID] = true
		}
	}

	scopes := FieldScopes(s)
	var out []Issue
	for _, c := range imp.Fields {
		// Array items are checked through their array (their type is the array's items).
		if strings.HasSuffix(c.Path, "[]") {
			if f := design[c.Path]; f != nil && f.Type != c.Type {
				out = append(out, Issue{
					Severity: SeverityError, Code: CodeReferenceTypeMismatch, Path: c.Path, NodePath: c.Path, FieldID: f.ID,
					Message: c.Path + " holds " + string(f.Type) + " here but " + string(c.Type) + " in the imported design.",
				})
			}
			continue
		}
		f := design[c.Path]
		if f == nil {
			if c.Type.Scalar() || c.Type == docschema.Array {
				parent, name := splitPath(c.Path)
				fix := &Fix{Kind: "restore-field", Label: "Add " + name + " back"}
				if pf := design[parent]; pf != nil || parent == "" {
					scope := ""
					if pf != nil {
						scope = scopes[pf.ID]
						if r := relationFilling(s, pf.ID); r != "" {
							scope = r
						}
					}
					if col := matchingColumn(s, ctx, scope, name); col != "" {
						fix.Label += " (from " + col + ")"
						fix.RelationID, fix.Columns = scope, []string{col}
					}
				}
				out = append(out, Issue{
					Severity: loose, Code: CodeReferenceFieldUnmapped, Path: c.Path, NodePath: c.Path, Fix: fix,
					Message: c.Path + " is in the design you imported but missing here; it may have been replaced when an object was embedded. Add it back.",
				})
			}
			continue
		}
		if f.Type != c.Type {
			out = append(out, Issue{
				Severity: SeverityError, Code: CodeReferenceTypeMismatch, Path: c.Path, NodePath: c.Path, FieldID: f.ID,
				Message: c.Path + " is " + string(f.Type) + " here but " + string(c.Type) + " in the imported design.",
			})
			continue
		}
		if !referenceLeaf(c.Path, f.Type, f.Items) {
			continue
		}
		m := filled[f.ID]
		if m == nil {
			// A required field left unmapped is REQUIRED_FIELD_UNMAPPED (an error) already.
			if !other[f.ID] && !mustMap(docschema.Leaf{FieldID: f.ID, Path: c.Path, Required: f.Required}) {
				is := Issue{
					Severity: loose, Code: CodeReferenceFieldUnmapped, Path: c.Path, NodePath: c.Path, FieldID: f.ID,
					Message: c.Path + " is in the design you imported but nothing fills it. Drag a source column onto it, or give it a constant.",
				}
				_, name := splitPath(c.Path)
				if col := matchingColumn(s, ctx, scopes[f.ID], name); col != "" {
					is.Fix = &Fix{Kind: "map-field", Label: "Map from " + col, RelationID: scopes[f.ID], Columns: []string{col}}
				}
				out = append(out, is)
			}
			continue
		}
		if c.Required && nullableSource(s, ctx, m) {
			out = append(out, Issue{
				Severity: SeverityWarning, Code: CodeReferenceNullable, Path: c.Path, NodePath: c.Path, FieldID: f.ID,
				Message: c.Path + " is required in the imported design, but " + m.Sources[0].Column + " can be null; add a \"Default if null\" transform.",
			})
		}
	}
	return append(out, extraBranches(s, design, contract)...)
}

// extraBranches reports fields added in the tool, one issue per branch (OC-070): the topmost
// field the imported design doesn't have, with how many fields it holds. Adding fields is
// normal (an embedded product, say), so it's info unless the mapping must match exactly.
func extraBranches(s *Spec, design map[string]*docschema.DocField, contract map[string]docschema.ContractField) []Issue {
	sev := SeverityInfo
	if s.ReferenceStrict {
		sev = SeverityError
	}
	// The branch of p: its shortest prefix path that isn't in the design's contract.
	branch := func(p string) string {
		for i := 0; i < len(p); i++ {
			if p[i] != '.' && !strings.HasPrefix(p[i:], "[]") {
				continue
			}
			q := p[:i]
			if _, ok := contract[q]; !ok && design[q] != nil {
				return q
			}
		}
		return p
	}
	leaves := map[string]int{}
	for p, f := range design {
		if _, ok := contract[p]; ok || !referenceLeaf(p, f.Type, f.Items) {
			continue
		}
		if s.TypeField.Enabled && p == s.TypeField.Name {
			continue
		}
		leaves[branch(p)]++
	}
	from := "the design you imported"
	if name := s.DocSchema.Imported.FileName; name != "" {
		from += " from " + name
	}
	paths := make([]string, 0, len(leaves))
	for p := range leaves {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]Issue, 0, len(paths))
	for _, p := range paths {
		what := p
		if n := leaves[p]; n > 1 || !referenceLeaf(p, design[p].Type, design[p].Items) {
			what = fmt.Sprintf("%s (%d field%s)", p, n, map[bool]string{true: "s"}[n != 1])
		}
		out = append(out, Issue{
			Severity: sev, Code: CodeReferenceExtraField, Path: p, NodePath: p, FieldID: design[p].ID,
			Message: what + " isn't in " + from + ". Add it to the design to keep it, or remove it.",
			Fix:     &Fix{Kind: "add-to-design", Label: "Add to design"},
		})
	}
	return out
}

// splitPath splits a design path into its parent's path and the field's name:
// "items[].productId" → "items[]", "productId".
func splitPath(p string) (parent, name string) {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "", p
}

// relationFilling is the relation bound to field id (its rows fill it), or "".
func relationFilling(s *Spec, id string) string {
	for _, r := range s.Relations {
		if !r.LookupOnly && r.TargetFieldID == id {
			return r.ID
		}
	}
	return ""
}

// matchingColumn is the column of scope's table (the root table, or a relation's) whose name
// matches a field's, ignoring case and separators (productId ↔ product_id), or "" (OC-071).
func matchingColumn(s *Spec, ctx Context, scope, field string) string {
	t := ctx.Table
	if scope != "" {
		r := s.RelationByID(scope)
		if r == nil {
			return ""
		}
		switch {
		case ctx.Related != nil && ctx.Related(r.Table.Schema, r.Table.Name) != nil:
			t = ctx.Related(r.Table.Schema, r.Table.Name)
		case t == nil || t.Name != r.Table.Name:
			return ""
		}
	}
	if t == nil {
		return ""
	}
	norm := func(n string) string {
		return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(n))
	}
	for _, c := range t.Columns {
		if norm(c.Name) == norm(field) {
			return c.Name
		}
	}
	return ""
}

// nullableSource: a single-column mapping from a nullable column with no "default" step.
func nullableSource(s *Spec, ctx Context, m *FieldMapping) bool {
	if len(m.Sources) != 1 {
		return false
	}
	for _, st := range m.Transforms {
		if st.Op == "default" {
			return false
		}
	}
	src := m.Sources[0]
	var t *schema.Table
	switch {
	case src.Relation == "":
		t = ctx.Table
	case ctx.Related != nil:
		if r := s.RelationByID(src.Relation); r != nil {
			ref := r.Table
			if src.From == "via" && r.Via != nil {
				ref = r.Via.Table
			}
			t = ctx.Related(ref.Schema, ref.Name)
		}
	}
	if t == nil {
		return false
	}
	c := t.Column(src.Column)
	return c != nil && c.Nullable
}
