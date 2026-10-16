// Package relations is a source's relationship graph (OC-042): foreign keys in both directions
// plus user-defined (virtual) relationships, multi-hop path search from a root table,
// junction-table detection, index advice and cardinality detection. The mapping canvas uses it to
// offer related tables; the relation tree it produces is resolved by internal/related.
package relations

import (
	"slices"
	"sort"
	"strings"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// Path search limits.
const (
	DefaultDepth = 4
	MaxDepth     = 6
	MaxPaths     = 200
)

// Hop is one step of a path: From's rows joined to To's.
type Hop struct {
	From mapping.TableRef   `json:"from"`
	To   mapping.TableRef   `json:"to"`
	Kind string             `json:"kind"` // many-to-one | one-to-many (as seen from From)
	Join []mapping.JoinPair `json:"join"` // parent = From's column, child = To's
	// Constraint is the foreign key's name; RelationshipID the virtual relationship's.
	Constraint     string `json:"constraint,omitempty"`
	RelationshipID string `json:"relationshipId,omitempty"`
	Virtual        bool   `json:"virtual,omitempty"`
	// Indexed reports whether To's join columns lead an index (the lookups use it).
	Indexed bool `json:"indexed"`
}

// Path is a chain of hops from the root table.
type Path struct {
	Hops []Hop `json:"hops"`
}

func (p Path) name() string {
	var b strings.Builder
	for _, h := range p.Hops {
		b.WriteString(h.To.String())
		b.WriteByte('/')
	}
	return b.String()
}

// quality counts hops backed by a foreign key and an index.
func (p Path) quality() int {
	n := 0
	for _, h := range p.Hops {
		if !h.Virtual {
			n++
		}
		if h.Indexed {
			n++
		}
	}
	return n
}

// Graph is a snapshot's tables and their relationships.
type Graph struct {
	snap *schema.Snapshot
	adj  map[string][]Hop
}

func key(t mapping.TableRef) string { return t.Schema + "." + t.Name }

// Build makes the graph: each foreign key gives a many-to-one hop from the referencing table
// and a one-to-many hop back; each virtual relationship gives hops in both directions by its
// cardinality (one-to-one relationships are objects both ways).
func Build(snap *schema.Snapshot, virtual []store.Relationship) *Graph {
	g := &Graph{snap: snap, adj: map[string][]Hop{}}
	add := func(h Hop) {
		if h.From == h.To && identity(h.Join) {
			return // a row joined to itself: nothing to embed
		}
		for _, o := range g.adj[key(h.From)] {
			if o.To == h.To && o.Kind == h.Kind && slices.Equal(o.Join, h.Join) {
				return // the same hop again (a duplicate, or a virtual copy of a foreign key)
			}
		}
		h.Indexed = mapping.LeadingIndex(snap.Table(h.To.Schema, h.To.Name), childCols(h.Join))
		g.adj[key(h.From)] = append(g.adj[key(h.From)], h)
	}
	for _, sc := range snap.Schemas {
		for _, t := range sc.Tables {
			self := mapping.TableRef{Schema: sc.Name, Name: t.Name}
			for _, fk := range t.ForeignKeys {
				ref := mapping.TableRef{Schema: fk.RefSchema, Name: fk.RefTable}
				var up, down []mapping.JoinPair
				for i := range fk.Columns {
					if i < len(fk.RefColumns) {
						up = append(up, mapping.JoinPair{Parent: fk.Columns[i], Child: fk.RefColumns[i]})
						down = append(down, mapping.JoinPair{Parent: fk.RefColumns[i], Child: fk.Columns[i]})
					}
				}
				add(Hop{From: self, To: ref, Kind: mapping.EmbedManyToOne, Join: up, Constraint: fk.Name})
				add(Hop{From: ref, To: self, Kind: mapping.EmbedOneToMany, Join: down, Constraint: fk.Name})
			}
		}
	}
	for _, r := range virtual {
		from := mapping.TableRef{Schema: r.From.Schema, Name: r.From.Table}
		to := mapping.TableRef{Schema: r.To.Schema, Name: r.To.Table}
		var fwd, back []mapping.JoinPair
		for i := range r.From.Columns {
			if i < len(r.To.Columns) {
				fwd = append(fwd, mapping.JoinPair{Parent: r.From.Columns[i], Child: r.To.Columns[i]})
				back = append(back, mapping.JoinPair{Parent: r.To.Columns[i], Child: r.From.Columns[i]})
			}
		}
		fk, bk := mapping.EmbedManyToOne, mapping.EmbedOneToMany
		switch r.Cardinality {
		case CardinalityOneToMany:
			fk, bk = mapping.EmbedOneToMany, mapping.EmbedManyToOne
		case CardinalityOneToOne:
			bk = mapping.EmbedManyToOne
		}
		add(Hop{From: from, To: to, Kind: fk, Join: fwd, RelationshipID: r.ID, Virtual: true})
		add(Hop{From: to, To: from, Kind: bk, Join: back, RelationshipID: r.ID, Virtual: true})
	}
	for k := range g.adj {
		sort.SliceStable(g.adj[k], func(i, j int) bool { return key(g.adj[k][i].To) < key(g.adj[k][j].To) })
	}
	return g
}

func identity(join []mapping.JoinPair) bool {
	for _, j := range join {
		if j.Parent != j.Child {
			return false
		}
	}
	return true
}

func childCols(join []mapping.JoinPair) []string {
	out := make([]string, len(join))
	for i, j := range join {
		out[i] = j.Child
	}
	return out
}

// Hops are root's direct relationships.
func (g *Graph) Hops(root mapping.TableRef) []Hop { return append([]Hop(nil), g.adj[key(root)]...) }

// Paths lists the ways out of root up to maxDepth hops (1..6, default 4), never revisiting a
// table on the same path. They're ranked by length, then by how many hops have a foreign key
// and an index, then by name, and capped at MaxPaths: a level is only expanded from the paths
// kept at the level before, so the search stays bounded on large schemas.
func (g *Graph) Paths(root mapping.TableRef, maxDepth int) []Path {
	if maxDepth <= 0 {
		maxDepth = DefaultDepth
	}
	maxDepth = min(maxDepth, MaxDepth)
	var out []Path
	frontier := []Path{{}}
	for depth := 1; depth <= maxDepth && len(out) < MaxPaths && len(frontier) > 0; depth++ {
		var level []Path
		for _, p := range frontier {
			at, seen := root, map[string]bool{key(root): true}
			for _, h := range p.Hops {
				at = h.To
				seen[key(h.To)] = true
			}
			for _, h := range g.adj[key(at)] {
				if seen[key(h.To)] {
					continue
				}
				hops := make([]Hop, len(p.Hops), len(p.Hops)+1)
				copy(hops, p.Hops)
				level = append(level, Path{Hops: append(hops, h)})
			}
		}
		sort.SliceStable(level, func(i, j int) bool {
			if qi, qj := level[i].quality(), level[j].quality(); qi != qj {
				return qi > qj
			}
			return level[i].name() < level[j].name()
		})
		level = level[:min(len(level), MaxPaths-len(out))]
		out = append(out, level...)
		frontier = level
	}
	return out
}
