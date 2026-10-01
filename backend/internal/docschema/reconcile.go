package docschema

import (
	"bytes"
	"strings"

	"omnidatabus/internal/naming"
)

// RenameSimilarity is the name similarity a removed and an added sibling of the same type need
// to be proposed as a rename (OC-061).
const RenameSimilarity = 0.8

// Diff is what reconciling a revised design changes (OC-061). Paths are the current design's
// for removed fields and "from", the incoming design's otherwise.
type Diff struct {
	Added       []DiffField  `json:"added"`
	Removed     []DiffField  `json:"removed"`
	TypeChanged []DiffType   `json:"typeChanged"`
	Renamed     []DiffChange `json:"renamed"`
	Moved       []DiffChange `json:"moved"`
}

// DiffField is an added or removed field.
type DiffField struct {
	FieldID string    `json:"fieldId"`
	Path    string    `json:"path"`
	Type    FieldType `json:"type"`
}

// DiffType is a field whose type changed; it keeps its ID (and so its mappings).
type DiffType struct {
	FieldID string    `json:"fieldId"`
	Path    string    `json:"path"`
	From    FieldType `json:"from"`
	To      FieldType `json:"to"`
}

// DiffChange is a proposed rename or move: the field keeps its ID unless the proposal is
// declined, when it becomes a removal and an addition.
type DiffChange struct {
	FieldID string `json:"fieldId"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// Reconcile turns current into incoming — incoming's names, nesting, order, types, examples,
// reference and import record — while every field that still exists keeps its ID and binding,
// so its mappings and relations stay (OC-061). Fields match by path, level by level; an
// unmatched sibling pair of the same type with similar names (or the same example) is a rename,
// and a scalar with the same name and type under another parent is a move. Declined lists the
// current field IDs whose proposed rename or move is refused.
func Reconcile(current, incoming *Schema, declined []string) (Schema, Diff) {
	no := map[string]bool{}
	for _, id := range declined {
		no[id] = true
	}
	out := Schema{Root: cloneTree(incoming.Root), Source: incoming.Source, Reference: incoming.Reference, Imported: incoming.Imported}
	out.Root.ID = current.Root.ID
	cur := index(&current.Root)
	inc := index(&out.Root)
	pair := map[*DocField]*DocField{&out.Root: &current.Root} // incoming → current
	used := map[*DocField]bool{&current.Root: true}
	diff := Diff{Added: []DiffField{}, Removed: []DiffField{}, TypeChanged: []DiffType{}, Renamed: []DiffChange{}, Moved: []DiffChange{}}
	curPaths, incPaths := current.Paths(), map[*DocField]string{}
	out.Walk(func(f *DocField, p string, _ *DocField) { incPaths[f] = p })

	for depth := 1; depth < len(inc.byDepth); depth++ {
		var open []*DocField // incoming fields at this depth whose parent matched, still unmatched
		for _, f := range inc.byDepth[depth] {
			pc := pair[inc.parent[f]]
			if pc == nil {
				continue
			}
			if c := childNamed(pc, f.Name); c != nil && !used[c] {
				pair[f], used[c] = c, true
				continue
			}
			open = append(open, f)
		}
		// Renames: an unmatched current sibling of the same type with a similar name.
		for _, f := range open {
			if f.Name == "[]" {
				continue
			}
			pc := pair[inc.parent[f]]
			var best *DocField
			bestSim := 0.0
			for _, c := range children(pc) {
				if used[c] || no[c.ID] || c.Type != f.Type {
					continue
				}
				s := nameSimilarity(c.Name, f.Name)
				if len(c.Example) > 0 && bytes.Equal(c.Example, f.Example) {
					s = max(s, RenameSimilarity)
				}
				if s >= RenameSimilarity && s > bestSim {
					best, bestSim = c, s
				}
			}
			if best != nil {
				pair[f], used[best] = best, true
				diff.Renamed = append(diff.Renamed, DiffChange{FieldID: best.ID, From: curPaths[best.ID], To: incPaths[f]})
			}
		}
	}
	// Moves: a scalar with the same name and type under another parent.
	for _, f := range inc.all {
		if pair[f] != nil || !f.Type.Scalar() || f.Name == "[]" {
			continue
		}
		for _, c := range cur.all {
			if !used[c] && !no[c.ID] && c.Name == f.Name && c.Type == f.Type && c.Type.Scalar() {
				pair[f], used[c] = c, true
				diff.Moved = append(diff.Moved, DiffChange{FieldID: c.ID, From: curPaths[c.ID], To: incPaths[f]})
				break
			}
		}
	}

	// Matched fields take their current ID and binding.
	ids := map[string]string{} // incoming ID → current ID
	for _, f := range inc.all {
		c := pair[f]
		if c == nil {
			continue
		}
		ids[f.ID] = c.ID
		if f.Type != c.Type {
			diff.TypeChanged = append(diff.TypeChanged, DiffType{FieldID: c.ID, Path: incPaths[f], From: c.Type, To: f.Type})
		}
		f.ID = c.ID
		if (f.Type == Object || f.Type == Array) && f.Type == c.Type {
			f.Binding = c.Binding
		}
	}
	for _, f := range inc.all {
		if pair[f] == nil && f.Name != "[]" {
			diff.Added = append(diff.Added, DiffField{FieldID: f.ID, Path: incPaths[f], Type: f.Type})
		}
	}
	for _, c := range cur.all {
		if !used[c] && c.Name != "[]" {
			diff.Removed = append(diff.Removed, DiffField{FieldID: c.ID, Path: curPaths[c.ID], Type: c.Type})
		}
	}
	for _, h := range incoming.ConstantHints {
		if id, ok := ids[h.FieldID]; ok {
			h.FieldID = id
		}
		out.ConstantHints = append(out.ConstantHints, h)
	}
	return out, diff
}

type tree struct {
	all     []*DocField
	byDepth [][]*DocField
	parent  map[*DocField]*DocField
}

// index lists a tree's fields (array items included, named "[]") in pre-order and by depth.
func index(root *DocField) *tree {
	t := &tree{parent: map[*DocField]*DocField{}, byDepth: [][]*DocField{{root}}}
	var visit func(f *DocField, depth int)
	visit = func(f *DocField, depth int) {
		for _, c := range children(f) {
			t.parent[c] = f
			t.all = append(t.all, c)
			for len(t.byDepth) <= depth {
				t.byDepth = append(t.byDepth, nil)
			}
			t.byDepth[depth] = append(t.byDepth[depth], c)
			visit(c, depth+1)
		}
	}
	visit(root, 1)
	return t
}

func children(f *DocField) []*DocField {
	var out []*DocField
	if f.Type == Array && f.Items != nil {
		return append(out, f.Items)
	}
	for i := range f.Children {
		out = append(out, &f.Children[i])
	}
	return out
}

func childNamed(f *DocField, name string) *DocField {
	for _, c := range children(f) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func cloneTree(f DocField) DocField {
	c := f
	if f.Children != nil {
		c.Children = make([]DocField, len(f.Children))
		for i := range f.Children {
			c.Children[i] = cloneTree(f.Children[i])
		}
	}
	if f.Items != nil {
		it := cloneTree(*f.Items)
		c.Items = &it
	}
	return c
}

// nameSimilarity is the Dice coefficient of two names' words, where a word also matches a
// longer word it abbreviates (cust ~ customer): custName ~ customerName is 1.
func nameSimilarity(a, b string) float64 {
	wa, wb := words(a), words(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	taken := make([]bool, len(wb))
	matches := 0
	for _, x := range wa {
		for j, y := range wb {
			if !taken[j] && (x == y || len(x) >= 3 && len(y) >= 3 && (strings.HasPrefix(x, y) || strings.HasPrefix(y, x))) {
				taken[j] = true
				matches++
				break
			}
		}
	}
	return 2 * float64(matches) / float64(len(wa)+len(wb))
}

func words(name string) []string {
	ws := naming.Lower(name)
	for i, w := range ws {
		ws[i] = naming.Singular(w)
	}
	return ws
}
