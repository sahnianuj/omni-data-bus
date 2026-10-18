package automap

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// Variant shapes (OC-171): an array whose items take one of several shapes. Three source
// patterns are tried from the array's scope, and the one whose variants fit best wins:
//   - A, an exclusive arc: one item table whose rows set exactly one of several foreign keys,
//     each pointing at a variant's own table;
//   - B, a type column: one item table with a column whose values are the variants'
//     discriminators (with a one-to-one detail table for a variant that has one);
//   - C, a union: each variant has its own table referencing the parent.

type variantPlan struct {
	pattern string // arc | type | union
	link    Link   // A, B: the item rows
	parts   []variantPart
	score   float64
	reason  string
}

type variantPart struct {
	v      *docschema.DocField
	when   string // A, B: the row column; B: value
	value  string
	lookup *Link // A: the variant's table; B: its detail table (nil = none)
	union  *Link // C: the variant's own rows
	score  float64
}

func discriminatorText(v *docschema.DocField) string {
	if v.Variant == nil || len(v.Variant.Value) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(v.Variant.Value, &s) != nil {
		return string(v.Variant.Value)
	}
	return s
}

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fitOf is how well a variant's own fields match the columns of some tables, and how many.
func (p *proposer) fitOf(v *docschema.DocField, tables ...mapping.TableRef) (float64, int) {
	var cols []colVariants
	for _, t := range tables {
		cols = append(cols, p.sg.columns(t, nil)...)
	}
	return p.sg.fit(own(v), cols, v.Name)
}

// nameBonus scores a variant's name or discriminator against a table name ("SCC" ~ sccs).
func (p *proposer) nameBonus(v *docschema.DocField, t mapping.TableRef) float64 {
	best := 0.0
	for _, n := range []string{discriminatorText(v), v.Name, v.Variant.Title} {
		if n == "" {
			continue
		}
		s, _ := p.sg.in.Dicts.nameScore(p.sg.in.Dicts.sourceVariants(SourceCol{Name: t.Name}), p.sg.leaf(n, ""))
		if norm(t.Name) == norm(n)+"s" || norm(t.Name) == norm(n) {
			s = 1
		}
		best = max(best, s)
	}
	return best
}

// variants proposes an array of variants; false when no pattern fits (the caller reports it).
func (p *proposer) variants(c *docschema.DocField, relID string, scope mapping.TableRef, depth int, names []string) bool {
	if depth+1 > p.in.MaxDepth {
		return false
	}
	var vs []*docschema.DocField
	for i := range c.Items.Children {
		if c.Items.Children[i].Variant != nil {
			vs = append(vs, &c.Items.Children[i])
		}
	}
	var plans []variantPlan
	for _, l := range p.in.Links(scope) {
		if l.Kind != mapping.EmbedOneToMany || l.Via != nil {
			continue
		}
		t := p.in.Table(l.Table)
		if t == nil {
			continue
		}
		if pl, ok := p.arcPlan(vs, l, t); ok {
			plans = append(plans, pl)
		}
		if pl, ok := p.typePlan(vs, l, t); ok {
			plans = append(plans, pl)
		}
	}
	if pl, ok := p.unionPlan(vs, scope); ok {
		plans = append(plans, pl)
	}
	if len(plans) == 0 {
		return false
	}
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].score > plans[j].score })
	best := plans[0]
	if best.score < bandMedium {
		return false
	}
	p.applyVariants(c, relID, best, names)
	return true
}

// arcPlan: the item table's exclusive arc (or, without a CHECK, its nullable foreign keys)
// gives each variant its table.
func (p *proposer) arcPlan(vs []*docschema.DocField, l Link, t *schema.Table) (variantPlan, bool) {
	arcs := t.ExclusiveArcs
	confirmed := len(arcs) > 0
	if !confirmed {
		var nullable []string
		for _, fk := range t.ForeignKeys {
			if len(fk.Columns) == 1 && t.Column(fk.Columns[0]) != nil && t.Column(fk.Columns[0]).Nullable {
				nullable = append(nullable, fk.Columns[0])
			}
		}
		if len(nullable) >= 2 {
			arcs = [][]string{nullable}
		}
	}
	var best variantPlan
	for _, arc := range arcs {
		// Each arc column's table, by its foreign key.
		target := map[string]*Link{}
		for _, lk := range p.in.Links(l.Table) {
			if lk.Kind == mapping.EmbedManyToOne && len(lk.Join) == 1 && containsStr(arc, lk.Join[0].Parent) {
				x := lk
				target[lk.Join[0].Parent] = &x
			}
		}
		type pair struct {
			v     int
			col   string
			score float64
		}
		var pairs []pair
		for i, v := range vs {
			for col, lk := range target {
				fit, _ := p.fitOf(v, l.Table, lk.Table)
				pairs = append(pairs, pair{i, col, round2(0.6*fit + 0.4*p.nameBonus(v, lk.Table))})
			}
		}
		sort.SliceStable(pairs, func(i, j int) bool {
			if pairs[i].score != pairs[j].score {
				return pairs[i].score > pairs[j].score
			}
			return pairs[i].col < pairs[j].col
		})
		parts := make([]variantPart, len(vs))
		usedV, usedC := map[int]bool{}, map[string]bool{}
		for _, pr := range pairs {
			if usedV[pr.v] || usedC[pr.col] {
				continue
			}
			usedV[pr.v], usedC[pr.col] = true, true
			parts[pr.v] = variantPart{v: vs[pr.v], when: pr.col, lookup: target[pr.col], score: pr.score}
		}
		total := 0.0
		for i := range parts {
			if parts[i].v == nil {
				parts[i] = variantPart{v: vs[i]}
			}
			total += parts[i].score
		}
		pl := variantPlan{
			pattern: "arc", link: l, parts: parts, score: round2(total / float64(len(vs))),
			reason: fmt.Sprintf("each %s row sets one of %s", l.Table.Name, strings.Join(arc, ", ")),
		}
		if !confirmed {
			pl.score = round2(pl.score * 0.9)
			pl.reason += " (no CHECK says exactly one)"
		}
		if pl.score > best.score {
			best = pl
		}
	}
	return best, best.parts != nil
}

// typePlan: a column of the item table whose values are the variants' discriminators.
func (p *proposer) typePlan(vs []*docschema.DocField, l Link, t *schema.Table) (variantPlan, bool) {
	want := map[string]*docschema.DocField{}
	for _, v := range vs {
		d := discriminatorText(v)
		if d == "" {
			return variantPlan{}, false
		}
		want[norm(d)] = v
	}
	var best variantPlan
	for i := range t.Columns {
		c := &t.Columns[i]
		if c.JSONType != schema.TypeString {
			continue
		}
		values := c.EnumValues
		if len(values) == 0 && p.in.Values != nil {
			values = p.in.Values(l.Table, c.Name, 1000)
		}
		if len(values) == 0 {
			continue
		}
		byNorm := map[string]string{}
		for _, v := range values {
			byNorm[norm(v)] = v
		}
		matched := 0
		parts := make([]variantPart, 0, len(vs))
		for _, v := range vs {
			raw, ok := byNorm[norm(discriminatorText(v))]
			part := variantPart{v: v}
			if ok {
				matched++
				part.when, part.value = c.Name, raw
				// A one-to-one detail table of this variant (postal_addresses for postal).
				tables := []mapping.TableRef{l.Table}
				var detail *Link
				bestDetail := 0.0
				for _, lk := range p.in.Links(l.Table) {
					if lk.Cardinality != mapping.CardinalityOneToOne || lk.Direction != mapping.DirectionReferencedBy {
						continue
					}
					if f, n := p.fitOf(v, lk.Table); n > 0 && f > bestDetail {
						x := lk
						detail, bestDetail = &x, f
					}
				}
				if detail != nil {
					tables = append(tables, detail.Table)
					part.lookup = detail
				}
				fit, _ := p.fitOf(v, tables...)
				part.score = round2(0.5 + 0.5*fit)
			}
			parts = append(parts, part)
		}
		if matched < 2 {
			continue
		}
		total := 0.0
		for _, pt := range parts {
			total += pt.score
		}
		pl := variantPlan{
			pattern: "type", link: l, parts: parts, score: round2(total / float64(len(vs))),
			reason: fmt.Sprintf("%s.%s holds the variants' %s (%d/%d)", l.Table.Name, c.Name, vs[0].Variant.Field, matched, len(vs)),
		}
		if pl.score > best.score {
			best = pl
		}
	}
	return best, best.parts != nil
}

// unionPlan: each variant's best one-to-many table, all different.
func (p *proposer) unionPlan(vs []*docschema.DocField, scope mapping.TableRef) (variantPlan, bool) {
	pl := variantPlan{pattern: "union"}
	used := map[string]bool{}
	total := 0.0
	for _, v := range vs {
		var bestLink *Link
		bestScore := 0.0
		for _, l := range p.in.Links(scope) {
			if l.Kind != mapping.EmbedOneToMany || l.Via != nil || used[l.Table.String()] {
				continue
			}
			fit, n := p.fitOf(v, l.Table)
			if n == 0 {
				continue
			}
			s := round2(0.6*fit + 0.4*p.nameBonus(v, l.Table))
			if s > bestScore {
				x := l
				bestLink, bestScore = &x, s
			}
		}
		part := variantPart{v: v, score: bestScore, union: bestLink}
		if bestLink != nil {
			used[bestLink.Table.String()] = true
		}
		pl.parts = append(pl.parts, part)
		total += bestScore
	}
	if len(used) < 2 {
		return variantPlan{}, false
	}
	pl.score = round2(total / float64(len(vs)) * 0.95) // separate tables: a shared one is more likely
	pl.reason = fmt.Sprintf("one table per variant (%d of %d)", len(used), len(vs))
	return pl, true
}

func containsStr(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// applyVariants writes a variant plan into the spec and proposes each variant's fields.
func (p *proposer) applyVariants(c *docschema.DocField, relID string, pl variantPlan, names []string) {
	path := p.paths[c.ID] + "[]"
	var pid *string
	if relID != "" {
		x := relID
		pid = &x
	}
	it := ProposalItem{FieldID: c.ID, Path: path, Kind: ItemBinding, Status: StatusBound, Score: pl.score, Band: bindBand(pl.score), Reasons: []string{pl.reason}}
	if pl.pattern == "union" {
		it.Summary = fmt.Sprintf("union of %d tables", len(pl.parts))
		order := commonOrder(p, pl)
		for _, part := range pl.parts {
			if part.union == nil {
				p.unresolvedVariant(part, "no table of "+p.driving.Name+"'s relationships fits it")
				continue
			}
			rel := mapping.Relation{ID: shortID("rel_", part.v.ID, part.union.Table.String()), ParentID: pid, Kind: mapping.EmbedOneToMany, Table: part.union.Table, Join: part.union.Join, TargetFieldID: part.v.ID, Limit: mapping.DefaultEmbedLimit}
			if order != "" {
				rel.OrderBy = []mapping.OrderColumn{{Column: order}}
			}
			p.spec.Relations = append(p.spec.Relations, rel)
			it.RelationIDs = append(it.RelationIDs, rel.ID)
			p.add(ProposalItem{
				FieldID: part.v.ID, Path: path + "<" + part.v.Name + ">", Kind: ItemBinding, Status: StatusBound, Score: part.score, Band: bindBand(part.score),
				Summary: part.v.Name + " ← " + part.union.Table.Name, Reasons: []string{fmt.Sprintf("%s rows are its items", part.union.Table.Name)}, RelationIDs: []string{rel.ID},
			})
			p.fields(part.v, rel.ID, rel.Table, nil, append(names, part.v.Name))
		}
		if order != "" {
			it.Reasons = append(it.Reasons, "merged by "+order)
		}
		p.add(it)
		return
	}
	rel := mapping.Relation{ID: shortID("rel_", c.ID, pl.link.Table.String(), fmt.Sprint(pl.link.Join)), ParentID: pid, Kind: mapping.EmbedOneToMany, Table: pl.link.Table, Join: pl.link.Join, TargetFieldID: c.ID, Limit: mapping.DefaultEmbedLimit}
	for _, part := range pl.parts {
		if part.when != "" {
			rel.Variants = append(rel.Variants, mapping.RelationVariant{FieldID: part.v.ID, When: part.when, Value: part.value})
		}
	}
	p.spec.Relations = append(p.spec.Relations, rel)
	p.sg.bound[c.ID] = &p.spec.Relations[len(p.spec.Relations)-1]
	it.Summary = fmt.Sprintf("↙ %s 1:N · %d variants", pl.link.Table.Name, len(rel.Variants))
	it.RelationIDs = []string{rel.ID}
	p.add(it)
	// The items' own fields (shared by every variant), then each variant's.
	p.fields(c.Items, rel.ID, rel.Table, nil, names)
	for _, part := range pl.parts {
		if part.when == "" {
			p.unresolvedVariant(part, "no "+pl.link.Table.Name+" rows are of this variant")
			continue
		}
		vp := path + "<" + part.v.Name + ">"
		sum := part.v.Name + " ← " + part.when
		if part.value != "" {
			sum = part.v.Name + " ← " + part.when + " = '" + part.value + "'"
		}
		reasons := []string{}
		if part.lookup != nil {
			sum += " · " + part.lookup.Table.Name
			reasons = append(reasons, "its fields from "+part.lookup.Table.Name)
		}
		p.add(ProposalItem{FieldID: part.v.ID, Path: vp, Kind: ItemBinding, Status: StatusBound, Score: part.score, Band: bindBand(part.score), Summary: sum, Reasons: reasons, RelationIDs: []string{rel.ID}})
		own := part.lookup
		p.onlyLookup = func(l Link) bool {
			return own != nil && l.Table == own.Table && fmt.Sprint(l.Join) == fmt.Sprint(own.Join)
		}
		p.fields(part.v, rel.ID, rel.Table, nil, append(names, part.v.Name))
		p.onlyLookup = nil
	}
}

func (p *proposer) unresolvedVariant(part variantPart, why string) {
	p.add(ProposalItem{FieldID: part.v.ID, Path: p.paths[part.v.ID], Kind: ItemBinding, Status: StatusUnresolved, Band: BandLow, Code: CodeNoPath, Summary: "unresolved", Reasons: []string{why}})
	p.walkUnresolved(part.v, nil)
}

// commonOrder is a column every union table has that orders rows in time.
func commonOrder(p *proposer, pl variantPlan) string {
	for _, n := range []string{"created_at", "occurred_at", "at", "updated_at"} {
		all := true
		for _, part := range pl.parts {
			if part.union == nil {
				continue
			}
			t := p.in.Table(part.union.Table)
			all = all && t != nil && t.Column(n) != nil
		}
		if all {
			return n
		}
	}
	return ""
}
