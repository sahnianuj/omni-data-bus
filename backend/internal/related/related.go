// Package related resolves a mapping's relations (OC-021, extended by OC-043 – OC-046): for a
// batch of root rows it looks up each relation's rows with one query per relation (never one
// per row), level by level down the relation tree, and hangs them under their parent rows. The
// pipeline, the mapping preview and the headless runner all use it.
package related

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// DefaultConcurrency bounds the lookups of one depth that run at once.
const DefaultConcurrency = 4

// Resolution strategies (OC-046).
const (
	StrategyBatched     = mapping.StrategyBatched
	StrategySortedMerge = mapping.StrategySortedMerge
)

// Plan is how one relation's rows are read.
type Plan struct {
	ID string
	// Parent is the parent relation's ID ("" = the root table); Depth is 1 for its children.
	Parent string
	Depth  int
	Many   bool // one-to-many or many-to-many
	// ParentIdx are the join columns in the parent row; ChildIdx the same in the child row
	// (for many-to-many, the junction's parent join columns after the table's columns).
	ParentIdx []int
	ChildIdx  []int
	Query     source.RelatedQuery
	// Keep is the most rows kept per parent: limit+1 for an array (so the transform can tell it
	// was truncated), 1 for many-to-one, 0 for no limit (aggregates and split pages need every
	// row, OC-045/046).
	Keep int
	// DanglingIdx are a many-to-many row's target join columns: all null means the junction row's
	// target is missing (dropped and counted unless KeepDangling, OC-044).
	DanglingIdx  []int
	KeepDangling bool
	// DistinctIdx identify the target row for distinct many-to-many relations.
	DistinctIdx []int
	// Strategy is batched (keys = ANY) or sorted-merge (a key range, merged in order, OC-046).
	Strategy string
}

// Tables resolves the table each relation's rows are read as, by relation id (for
// transform.Options.Related): the relation's table, or for many-to-many its row table (the
// table's columns followed by the junction's, mapping.RowTable).
func Tables(s *mapping.Spec, rel mapping.RelatedTables) map[string]*schema.Table {
	out := map[string]*schema.Table{}
	if rel == nil {
		return out
	}
	for _, e := range s.Relations {
		t := rel(e.Table.Schema, e.Table.Name)
		if t == nil {
			continue
		}
		var junction *schema.Table
		if e.Via != nil {
			if junction = rel(e.Via.Table.Schema, e.Via.Table.Name); junction == nil {
				continue
			}
		}
		out[e.ID] = mapping.RowTable(e, t, junction)
	}
	return out
}

// ParentColumns are the columns of the mapped table that its relations join on (they must be
// read). Nested relations join on their parent relation's table instead.
func ParentColumns(s *mapping.Spec) []string {
	var out []string
	for _, e := range s.Relations {
		if e.Parent() == "" {
			out = append(out, parentJoinColumns(e)...)
		}
	}
	return out
}

// parentJoinColumns are the parent's columns a relation joins on.
func parentJoinColumns(e mapping.Relation) []string {
	var out []string
	if e.Kind == mapping.RelationManyToMany && e.Via != nil {
		for _, j := range e.Via.ParentJoin {
			out = append(out, j.Parent)
		}
		return out
	}
	for _, j := range e.Join {
		out = append(out, j.Parent)
	}
	return out
}

// Plans builds one plan per relation, parents before children. A relation's projection is the
// columns any mapping reads from it (item fields can read their ancestors', OC-043), its join,
// order, value, key and aggregate columns, and the columns its child relations join on (every
// column when an expression in its scope may read row.<any>).
func Plans(s *mapping.Spec, root *schema.Table, rel mapping.RelatedTables) ([]Plan, error) {
	order, depth, is := mapping.Depths(s)
	if is != nil {
		return nil, fmt.Errorf("%s", is.Message)
	}
	lookup := func(t mapping.TableRef) *schema.Table {
		if rel == nil {
			return nil
		}
		return rel(t.Schema, t.Name)
	}
	rows := Tables(s, rel)
	scopes := mapping.FieldScopes(s)
	var out []Plan
	for _, id := range order {
		e := *s.RelationByID(id)
		child := lookup(e.Table)
		if child == nil {
			return nil, fmt.Errorf("embedded table %s isn't in the latest schema snapshot", e.Table)
		}
		var junction *schema.Table
		if e.Kind == mapping.RelationManyToMany {
			if e.Via == nil {
				return nil, fmt.Errorf("relation %s: many-to-many without a junction", e.ID)
			}
			if junction = lookup(e.Via.Table); junction == nil {
				return nil, fmt.Errorf("junction table %s isn't in the latest schema snapshot", e.Via.Table)
			}
		}
		parent := root
		if pid := e.Parent(); pid != "" {
			if parent = rows[pid]; parent == nil {
				return nil, fmt.Errorf("relation %s: its parent %s isn't available", e.ID, pid)
			}
		}
		p, err := planOf(s, e, depth[id], parent, child, junction, scopes)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func planOf(s *mapping.Spec, e mapping.Relation, depth int, parent, child, junction *schema.Table, scopes map[string]string) (Plan, error) {
	p := Plan{ID: e.ID, Parent: e.Parent(), Depth: depth, Many: e.Many(), Keep: 1, Strategy: StrategyBatched, KeepDangling: e.KeepDangling}
	need := map[string]bool{}
	all := false
	for _, c := range s.Relations {
		if c.Parent() == e.ID {
			for _, col := range parentJoinColumns(c) {
				need[col] = true // a child relation joins on these columns of this table
			}
		}
	}
	for _, m := range s.FieldMappings {
		if !m.Active() {
			continue
		}
		for _, src := range m.Sources {
			if src.Relation == e.ID && src.From != mapping.FromVia {
				need[src.Column] = true
			}
		}
		if scopes[m.TargetFieldID] == e.ID || mapping.UsesRelation(m, e.ID) {
			for _, st := range m.Transforms {
				all = all || st.Op == "expression"
			}
		}
	}
	// Composer expressions may read this relation's row by name or as $parent (OC-047).
	names := []string{e.ID, e.Table.Name}
	if f := s.DocSchema.Find(e.TargetFieldID); f != nil {
		names = append(names, f.Name)
	}
	for _, src := range mapping.Expressions(s) {
		used := mapping.ExprIdentifiers(src)
		all = all || used["$parent"]
		for _, n := range names {
			all = all || used[n]
		}
	}
	for _, a := range s.Aggregates {
		if a.RelationID == e.ID && a.Column != "" && a.From != mapping.FromVia {
			need[a.Column] = true
		}
	}
	for _, c := range []string{e.ValueColumn, e.KeyColumn} {
		if c != "" {
			need[c] = true
		}
	}
	q := source.RelatedQuery{Table: source.TableRead{Schema: e.Table.Schema, Table: e.Table.Name, Columns: child.Columns}}
	if e.Kind == mapping.RelationManyToMany {
		via := mapping.ViaColumns(e)
		q.Via = &source.ViaQuery{
			Table:   source.TableRead{Schema: e.Via.Table.Schema, Table: e.Via.Table.Name, Columns: junction.Columns},
			Columns: via,
		}
		for _, j := range e.Via.ChildJoin {
			q.Via.ChildJoin = append(q.Via.ChildJoin, source.ViaJoin{Via: j.Via, Child: j.Child})
			ci := index(child, j.Child)
			if ci < 0 || junction.Column(j.Via) == nil {
				return p, fmt.Errorf("relation %s: join column %s → %s is missing", e.ID, j.Via, j.Child)
			}
			p.DanglingIdx = append(p.DanglingIdx, ci)
			need[j.Child] = true
		}
		for _, j := range e.Via.ParentJoin {
			pi := index(parent, j.Parent)
			vi := indexOf(via, j.Via)
			if pi < 0 || vi < 0 || junction.Column(j.Via) == nil {
				return p, fmt.Errorf("relation %s: join column %s → %s is missing", e.ID, j.Parent, j.Via)
			}
			p.ParentIdx, p.ChildIdx = append(p.ParentIdx, pi), append(p.ChildIdx, len(child.Columns)+vi)
			q.Join = append(q.Join, j.Via)
		}
		if e.Distinct {
			ident := child.PrimaryKey
			if len(ident) == 0 {
				for _, j := range e.Via.ChildJoin {
					ident = append(ident, j.Child)
				}
			}
			for _, c := range ident {
				p.DistinctIdx = append(p.DistinctIdx, index(child, c))
				need[c] = true
			}
		}
	} else {
		for _, j := range e.Join {
			pi, ci := index(parent, j.Parent), index(child, j.Child)
			if pi < 0 || ci < 0 {
				return p, fmt.Errorf("embed %s: join column %s → %s is missing", e.ID, j.Parent, j.Child)
			}
			p.ParentIdx, p.ChildIdx = append(p.ParentIdx, pi), append(p.ChildIdx, ci)
			q.Join = append(q.Join, j.Child)
			need[j.Child] = true
		}
	}
	rowTable := mapping.RowTable(e, child, junction)
	for _, f := range e.Filter {
		col := rowTable.Column(f.Column)
		if f.From == mapping.FromVia {
			col = rowTable.Column(mapping.ViaColumn(f.Column))
		}
		if col == nil {
			return p, fmt.Errorf("relation %s: filter column %s is missing", e.ID, f.Column)
		}
		c := *col
		c.Name = f.Column
		args, err := mapping.FilterArgs(c, f)
		if err != nil {
			return p, fmt.Errorf("relation %s: filter on %s: %w", e.ID, f.Column, err)
		}
		q.Filter = append(q.Filter, source.FilterCond{Column: f.Column, Op: f.Op, Args: args, Via: f.From == mapping.FromVia})
	}
	if p.Many {
		for _, o := range e.OrderBy {
			q.OrderBy = append(q.OrderBy, source.OrderColumn{Column: o.Column, Desc: o.Desc, Via: o.From == mapping.FromVia})
			if o.From != mapping.FromVia {
				need[o.Column] = true
			}
		}
		p.Keep = e.EffectiveLimit() + 1
		for _, a := range s.Aggregates {
			if a.RelationID == e.ID {
				p.Keep = 0 // aggregates see every row, before the limit (OC-045)
			}
		}
		if e.EffectiveOverflow() == mapping.OverflowSplit {
			p.Keep = 0 // pages hold the rows past the limit (OC-046)
		}
		// Small limits are applied in SQL to transfer less, unless rows may still be dropped
		// after the query (dangling or duplicate many-to-many rows).
		filtered := e.Kind == mapping.RelationManyToMany && (!e.KeepDangling || e.Distinct)
		if p.Keep > 0 && e.EffectiveLimit() <= mapping.SQLLimitThreshold && !filtered {
			q.PerKeyLimit = p.Keep
		}
	}
	q.Table.Project = make([]bool, len(child.Columns))
	for i, c := range child.Columns {
		q.Table.Project[i] = all || need[c.Name]
	}
	p.Query = q
	return p, nil
}

func index(t *schema.Table, col string) int {
	for i, c := range t.Columns {
		if c.Name == col {
			return i
		}
	}
	return -1
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// KeyOf renders the join columns of a row as a comparable string, normalizing driver types
// (an int4 parent column matches an int8 child column); ok is false when any part is null.
func KeyOf(row []any, idx []int) (string, bool) {
	var b strings.Builder
	for n, i := range idx {
		if i < 0 || i >= len(row) || row[i] == nil {
			return "", false
		}
		if n > 0 {
			b.WriteByte(0)
		}
		switch x := row[i].(type) {
		case int16:
			b.WriteString("i" + strconv.FormatInt(int64(x), 10))
		case int32:
			b.WriteString("i" + strconv.FormatInt(int64(x), 10))
		case int64:
			b.WriteString("i" + strconv.FormatInt(x, 10))
		case int:
			b.WriteString("i" + strconv.Itoa(x))
		case string:
			b.WriteString("s" + x)
		case [16]byte:
			b.WriteString("u" + hex.EncodeToString(x[:]))
		case []byte:
			b.WriteString("b" + hex.EncodeToString(x))
		case time.Time:
			b.WriteString("t" + strconv.FormatInt(x.UnixNano(), 10))
		default:
			fmt.Fprintf(&b, "v%v", x)
		}
	}
	return b.String(), true
}

// Row picks one parent row's related rows (nil when there are none).
func Row(all []source.Related, i int) source.Related {
	if i < len(all) {
		return all[i]
	}
	return nil
}
