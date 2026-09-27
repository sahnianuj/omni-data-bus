package mapping

import (
	"omnidatabus/internal/docschema"
)

// Design replacement strategies (OC-015 AC7).
const (
	StrategyReplace = "replace"
	StrategyMerge   = "merge"
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
