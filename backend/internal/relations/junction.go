package relations

import (
	"sort"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// Junction confidence (OC-042).
const (
	ConfidenceHigh   = "high"   // the junction's key is exactly its two foreign keys
	ConfidenceMedium = "medium" // a "payload" junction: a few columns besides the foreign keys
)

// MaxPayloadColumns is the most non-key columns a payload junction may have.
const MaxPayloadColumns = 4

// Junction is a many-to-many candidate: the root reaches Table through the junction Via,
// e.g. orders ↔ products via order_items (quantity, unit_price).
type Junction struct {
	Table      mapping.TableRef  `json:"table"`
	Via        mapping.TableRef  `json:"via"`
	ParentJoin []mapping.ViaJoin `json:"parentJoin"`
	ChildJoin  []mapping.ViaJoin `json:"childJoin"`
	// PayloadColumns are the junction's columns that aren't join or key columns.
	PayloadColumns []string `json:"payloadColumns"`
	Confidence     string   `json:"confidence"`
	// Indexed reports whether the junction's parent join columns lead an index.
	Indexed bool `json:"indexed"`
}

// Junctions finds the junction tables of root: a table J with a foreign key to root and
// another to a table B (B may be root itself through a second, distinct foreign key, e.g.
// friend(user_a, user_b)), where J's primary key or a unique constraint is exactly the union of
// those foreign keys' columns (high confidence), or J has at most 4 other columns besides its
// key (a payload junction such as order_items, medium confidence).
func Junctions(snap *schema.Snapshot, root mapping.TableRef) []Junction {
	out := []Junction{}
	rt := snap.Table(root.Schema, root.Name)
	if rt == nil {
		return out
	}
	seen := map[string]bool{}
	for _, ref := range rt.ReferencedBy {
		j := snap.Table(ref.Schema, ref.Table)
		if j == nil {
			continue
		}
		var up *schema.ForeignKey
		for i := range j.ForeignKeys {
			if j.ForeignKeys[i].Name == ref.Name {
				up = &j.ForeignKeys[i]
			}
		}
		if up == nil {
			continue
		}
		for i := range j.ForeignKeys {
			down := &j.ForeignKeys[i]
			if down.Name == up.Name || sameColumns(down.Columns, up.Columns) {
				continue // the same foreign key, or a self-join on the same columns
			}
			jc, ok := classify(j, up, down)
			if !ok {
				continue
			}
			c := Junction{
				Table: mapping.TableRef{Schema: down.RefSchema, Name: down.RefTable}, Via: mapping.TableRef{Schema: ref.Schema, Name: ref.Table},
				Confidence: jc, PayloadColumns: payload(j, up, down),
			}
			for n := range up.Columns {
				if n < len(up.RefColumns) {
					c.ParentJoin = append(c.ParentJoin, mapping.ViaJoin{Parent: up.RefColumns[n], Via: up.Columns[n]})
				}
			}
			for n := range down.Columns {
				if n < len(down.RefColumns) {
					c.ChildJoin = append(c.ChildJoin, mapping.ViaJoin{Via: down.Columns[n], Child: down.RefColumns[n]})
				}
			}
			c.Indexed = mapping.LeadingIndex(j, up.Columns)
			k := c.Via.String() + "|" + up.Name + "|" + down.Name
			if !seen[k] {
				seen[k] = true
				out = append(out, c)
			}
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Confidence != out[b].Confidence {
			return out[a].Confidence == ConfidenceHigh
		}
		return out[a].Table.String()+out[a].Via.String() < out[b].Table.String()+out[b].Via.String()
	})
	return out
}

// classify applies the junction rules; ok is false when J isn't a junction of up and down.
func classify(j *schema.Table, up, down *schema.ForeignKey) (string, bool) {
	union := append(append([]string(nil), up.Columns...), down.Columns...)
	if sameColumns(j.PrimaryKey, union) {
		return ConfidenceHigh, true
	}
	for _, u := range j.UniqueConstraints {
		if sameColumns(u, union) {
			return ConfidenceHigh, true
		}
	}
	if len(payload(j, up, down)) <= MaxPayloadColumns {
		return ConfidenceMedium, true
	}
	return "", false
}

// payload are J's columns outside both foreign keys and its primary key.
func payload(j *schema.Table, up, down *schema.ForeignKey) []string {
	skip := map[string]bool{}
	for _, c := range append(append(append([]string(nil), up.Columns...), down.Columns...), j.PrimaryKey...) {
		skip[c] = true
	}
	out := []string{}
	for _, c := range j.Columns {
		if !skip[c.Name] {
			out = append(out, c.Name)
		}
	}
	return out
}

func sameColumns(a, b []string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, c := range a {
		set[c] = true
	}
	for _, c := range b {
		if !set[c] {
			return false
		}
	}
	return len(set) == len(b)
}
