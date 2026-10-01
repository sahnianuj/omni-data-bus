package mapping

import (
	"omnidatabus/internal/docschema"
)

// Design replacement strategies (OC-015 AC7).
const (
	StrategyReplace = "replace"
	StrategyMerge   = "merge"
	// StrategyReconcile follows a revised design, keeping the IDs (and so the mappings and
	// bindings) of the fields that still exist (OC-061).
	StrategyReconcile = "reconcile"
)

// ReattachByPath moves field mappings from an old design onto a new one by field path. Mappings
// whose path no longer exists are returned as orphans (and dropped from kept).
func ReattachByPath(old, next *docschema.Schema, mappings []FieldMapping) (kept, orphans []FieldMapping) {
	oldPaths := old.Paths()
	newIDs := map[string]string{}
	for id, p := range next.Paths() {
		newIDs[p] = id
	}
	kept, orphans = []FieldMapping{}, []FieldMapping{}
	for _, m := range mappings {
		p, ok := oldPaths[m.TargetFieldID]
		if !ok {
			orphans = append(orphans, m)
			continue
		}
		if id, ok := newIDs[p]; ok {
			m.TargetFieldID = id
			kept = append(kept, m)
		} else {
			orphans = append(orphans, m)
		}
	}
	return kept, orphans
}

// MergeDesigns keeps every existing field (and its ID) and adds the incoming fields whose paths
// are new, recursively. Where both define a field, the existing type wins.
func MergeDesigns(existing, incoming *docschema.Schema) docschema.Schema {
	out := docschema.Schema{Root: cloneField(existing.Root), Source: existing.Source}
	mergeChildren(&out.Root, &incoming.Root)
	return out
}

func mergeChildren(dst, src *docschema.DocField) {
	byName := map[string]int{}
	for i, c := range dst.Children {
		byName[c.Name] = i
	}
	for _, c := range src.Children {
		i, ok := byName[c.Name]
		if !ok {
			dst.Children = append(dst.Children, cloneField(c))
			continue
		}
		d := &dst.Children[i]
		switch {
		case d.Type == docschema.Object && c.Type == docschema.Object:
			mergeChildren(d, &c)
		case d.Type == docschema.Array && c.Type == docschema.Array && d.Items != nil && c.Items != nil &&
			d.Items.Type == docschema.Object && c.Items.Type == docschema.Object:
			mergeChildren(d.Items, c.Items)
		}
	}
}

func cloneField(f docschema.DocField) docschema.DocField {
	c := f
	if f.Children != nil {
		c.Children = make([]docschema.DocField, len(f.Children))
		for i := range f.Children {
			c.Children[i] = cloneField(f.Children[i])
		}
	}
	if f.Items != nil {
		it := cloneField(*f.Items)
		c.Items = &it
	}
	return c
}

// Prune drops what points at fields that are gone: field mappings, relations bound to them
// (with the relations nested under those, and the mappings reading them) and aggregates.
// It returns the field mappings it dropped.
func Prune(s *Spec) (orphans []FieldMapping) {
	exists := map[string]bool{}
	s.DocSchema.Walk(func(f *docschema.DocField, _ string, _ *docschema.DocField) { exists[f.ID] = true })
	gone := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, r := range s.Relations {
			if gone[r.ID] {
				continue
			}
			if (r.TargetFieldID != "" && !exists[r.TargetFieldID]) || gone[r.Parent()] {
				gone[r.ID], changed = true, true
			}
		}
	}
	rels := s.Relations[:0]
	for _, r := range s.Relations {
		if !gone[r.ID] {
			rels = append(rels, r)
		}
	}
	s.Relations = rels
	kept := []FieldMapping{}
	for _, m := range s.FieldMappings {
		drop := !exists[m.TargetFieldID]
		for _, src := range m.Sources {
			drop = drop || gone[src.Relation]
		}
		if drop {
			orphans = append(orphans, m)
		} else {
			kept = append(kept, m)
		}
	}
	s.FieldMappings = kept
	aggs := s.Aggregates[:0]
	for _, a := range s.Aggregates {
		if exists[a.TargetFieldID] && !gone[a.RelationID] {
			aggs = append(aggs, a)
		}
	}
	s.Aggregates = aggs
	return orphans
}
