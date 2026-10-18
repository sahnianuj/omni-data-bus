package pos

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
)

// Report is how much of a spec agrees with Expected, per category (OC-162).
type Report struct {
	Totals map[string]Count `json:"totals"`
	// Misses are the paths that disagree, with what was found, by category.
	Misses map[string][]string `json:"misses"`
	// WrongHigh are high-confidence proposals that disagree (OC-173's gate fails on any).
	WrongHigh []string `json:"wrongHigh"`
}

// Count is correct out of total.
type Count struct {
	Correct int `json:"correct"`
	Total   int `json:"total"`
}

// Ratio is Correct/Total (1 when there is nothing to score).
func (c Count) Ratio() float64 {
	if c.Total == 0 {
		return 1
	}
	return float64(c.Correct) / float64(c.Total)
}

// HighConfidence is the mapping confidence at and above which a proposal counts as high.
const HighConfidence = 0.85

// Score compares spec with the expected mapping. A field counts as proposed when it has an
// active or pending mapping, an aggregate, or a not-populated state.
func Score(spec *mapping.Spec, exp Expected) Report {
	r := Report{Totals: map[string]Count{}, Misses: map[string][]string{}}
	for _, c := range Categories {
		r.Totals[c] = Count{}
	}
	hit := func(cat, path string, ok bool, found string) {
		c := r.Totals[cat]
		c.Total++
		if ok {
			c.Correct++
		} else {
			r.Misses[cat] = append(r.Misses[cat], path+": "+found)
		}
		r.Totals[cat] = c
	}
	driving := ""
	if spec.Root != nil {
		driving = spec.Root.Table.String()
	}
	hit(CatDriving, "$", driving == exp.Driving, driving)

	byPath := map[string]*docschema.DocField{}
	spec.DocSchema.Walk(func(f *docschema.DocField, p string, _ *docschema.DocField) { byPath[p] = f })
	relOf := map[string]*mapping.Relation{}
	for i := range spec.Relations {
		relOf[spec.Relations[i].ID] = &spec.Relations[i]
	}
	boundTo := map[string]*mapping.Relation{}
	for i := range spec.Relations {
		if r := &spec.Relations[i]; !r.LookupOnly && r.TargetFieldID != "" {
			boundTo[r.TargetFieldID] = r
		}
	}
	scopes := mapping.FieldScopes(spec)
	tableOf := func(relID string) string {
		if relID == "" {
			return driving
		}
		if rel := relOf[relID]; rel != nil {
			return rel.Table.String()
		}
		return "?"
	}
	notPopulated := map[string]bool{}
	for _, m := range spec.FieldMappings {
		if m.State == mapping.StateNotPopulated {
			notPopulated[m.TargetFieldID] = true
		}
	}

	// Containers.
	npContainer := map[string]bool{}
	for _, bp := range exp.bOrder {
		b := exp.Bindings[bp]
		node := byPath[strings.TrimSuffix(bp, "[]")]
		if node == nil {
			hit(CatBindings, bp, false, "no such node")
			continue
		}
		rel := boundTo[node.ID]
		switch {
		case b.NotPopulated:
			npContainer[strings.TrimSuffix(bp, "[]")] = true
			ok := rel == nil && (notPopulated[node.ID] || allNotPopulated(node, notPopulated))
			hit(CatBindings, bp, ok, describeRel(rel))
		case b.FromParent:
			hit(CatBindings, bp, rel == nil, describeRel(rel))
		default:
			ok := rel != nil && rel.Table.String() == b.Table && rel.Kind == b.Kind
			if ok && b.Adapter != "" {
				ok = node.Binding != nil && node.Binding.Adapter == b.Adapter
			}
			if ok && b.Variants != "" {
				ok = hasVariants(rel)
			}
			hit(CatBindings, bp, ok, describeRel(rel))
		}
	}

	// Fields.
	active := map[string]mapping.FieldMapping{}
	for _, m := range spec.FieldMappings {
		if m.State == mapping.StateActive || m.State == mapping.StatePending || m.State == mapping.StateNotPopulated {
			if _, ok := active[m.TargetFieldID]; !ok || m.State == mapping.StateActive {
				active[m.TargetFieldID] = m
			}
		}
	}
	aggOf := map[string]mapping.Aggregate{}
	for _, a := range spec.Aggregates {
		aggOf[a.TargetFieldID] = a
	}
	for _, fp := range exp.order {
		f := exp.Fields[fp]
		cat := f.Category()
		node := byPath[fp]
		if node == nil {
			hit(cat, fp, false, "no such field")
			continue
		}
		m, has := active[node.ID]
		agg, hasAgg := aggOf[node.ID]
		found := describeMapping(m, has, agg, hasAgg, tableOf, scopes[node.ID])
		var ok bool
		switch cat {
		case CatNotPopulated:
			ok = (has && m.State == mapping.StateNotPopulated) || inNotPopulated(fp, npContainer)
		case CatUnresolved:
			ok = !has && !hasAgg
		case CatConstant:
			ok = has && isConstant(m, f.Constant)
		case CatComputed:
			if f.Aggregate != "" {
				ok = hasAgg && aggregateMatches(agg, f.Aggregate, relOf)
			} else {
				ok = has && expressionMatches(m, f.Expression)
			}
		default:
			ok = has && sourceMatches(m, f, tableOf, scopes[node.ID])
		}
		hit(cat, fp, ok, found)
		if !ok && has && m.State != mapping.StateNotPopulated && m.Confidence >= HighConfidence && m.Origin != mapping.OriginManual {
			r.WrongHigh = append(r.WrongHigh, fp+": "+found)
		}
	}
	return r
}

// hasVariants reports whether an array's items come from several tables (OC-171).
func hasVariants(r *mapping.Relation) bool { return r != nil && len(r.Variants) > 0 }

func allNotPopulated(f *docschema.DocField, np map[string]bool) bool {
	kids := f.Children
	if f.Items != nil {
		kids = f.Items.Children
	}
	if len(kids) == 0 {
		return false
	}
	for i := range kids {
		k := &kids[i]
		if k.Type.Scalar() {
			if !np[k.ID] {
				return false
			}
		} else if !np[k.ID] && !allNotPopulated(k, np) {
			return false
		}
	}
	return true
}

func inNotPopulated(path string, np map[string]bool) bool {
	for p := range np {
		if strings.HasPrefix(path, p+".") || strings.HasPrefix(path, p+"[].") {
			return true
		}
	}
	return false
}

func describeRel(r *mapping.Relation) string {
	if r == nil {
		return "unbound"
	}
	return r.Table.String() + " " + r.Kind
}

func describeMapping(m mapping.FieldMapping, has bool, agg mapping.Aggregate, hasAgg bool, tableOf func(string) string, scope string) string {
	if hasAgg {
		return agg.Fn + "(" + tableOf(agg.RelationID) + "." + agg.Column + ")"
	}
	if !has {
		return "nothing"
	}
	if m.State == mapping.StateNotPopulated {
		return "not populated"
	}
	var parts []string
	for _, s := range m.Sources {
		rel := s.Relation
		if rel == "" {
			rel = scope
		}
		parts = append(parts, tableOf(rel)+"."+s.Column)
	}
	for _, t := range m.Transforms {
		parts = append(parts, t.Op+fmt.Sprint(t.Args))
	}
	return strings.Join(parts, " ")
}

func isConstant(m mapping.FieldMapping, want any) bool {
	for _, t := range m.Transforms {
		if t.Op == "constant" {
			return fmt.Sprint(t.Args["value"]) == fmt.Sprint(want)
		}
	}
	return false
}

var colRef = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)`)

// expressionMatches: the mapping is an expression that reads every column the expected one
// reads (the exact spelling may differ).
func expressionMatches(m mapping.FieldMapping, want string) bool {
	got := ""
	for _, t := range m.Transforms {
		if t.Op == "expression" {
			got, _ = t.Args["expr"].(string)
		}
	}
	if got == "" {
		return false
	}
	for _, ref := range colRef.FindAllStringSubmatch(want, -1) {
		if !strings.Contains(got, ref[2]) {
			return false
		}
	}
	for _, lit := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(want, -1) {
		if !strings.Contains(got, lit[1]) {
			return false
		}
	}
	return true
}

func aggregateMatches(a mapping.Aggregate, want string, relOf map[string]*mapping.Relation) bool {
	m := regexp.MustCompile(`^(\w+)\((\w+)\.(\w+)\)$`).FindStringSubmatch(want)
	if m == nil {
		return false
	}
	rel := relOf[a.RelationID]
	return rel != nil && a.Fn == m[1] && rel.Table.Name == m[2] && a.Column == m[3]
}

func sourceMatches(m mapping.FieldMapping, f ExpField, tableOf func(string) string, scope string) bool {
	if len(m.Sources) != 1 {
		return false
	}
	s := m.Sources[0]
	rel := s.Relation
	if rel == "" {
		rel = scope
	}
	table, col, _ := strings.Cut(f.Source, ".")
	if s.Column != col || !strings.HasSuffix(tableOf(rel), "."+table) {
		return false
	}
	switch f.Transform {
	case "":
		return true
	case "cents":
		for _, t := range m.Transforms {
			if t.Op == "expression" {
				if e, _ := t.Args["expr"].(string); strings.Contains(strings.ReplaceAll(e, " ", ""), "*100") {
					return true
				}
			}
		}
		return false
	default:
		for _, t := range m.Transforms {
			if t.Op == f.Transform {
				return true
			}
		}
		return false
	}
}

// String renders the report as a table, then the misses.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %8s\n", "category", "correct")
	for _, c := range Categories {
		t := r.Totals[c]
		fmt.Fprintf(&b, "%-22s %3d/%-3d %4.0f%%\n", c, t.Correct, t.Total, 100*t.Ratio())
	}
	cats := make([]string, 0, len(r.Misses))
	for c := range r.Misses {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		fmt.Fprintf(&b, "\n%s misses:\n", c)
		for _, m := range r.Misses[c] {
			fmt.Fprintf(&b, "  %s\n", m)
		}
	}
	if len(r.WrongHigh) > 0 {
		fmt.Fprintf(&b, "\nwrong high-confidence:\n")
		for _, m := range r.WrongHigh {
			fmt.Fprintf(&b, "  %s\n", m)
		}
	}
	return b.String()
}

// GateMin are the design-first proposal's gate thresholds (OC-173, bench/gates-2.5.0.json G10).
var GateMin = map[string]float64{"driving": 1, "bindings": 0.95, "fields": 0.90, "computed": 0.70}

// Quality is the report as gate values: each category's ratio, "fields" for the direct,
// converted, constant and not-populated fields together, and the wrong high-confidence count.
func (r Report) Quality() map[string]float64 {
	sum := func(cats ...string) float64 {
		var c Count
		for _, k := range cats {
			c.Correct += r.Totals[k].Correct
			c.Total += r.Totals[k].Total
		}
		return c.Ratio()
	}
	return map[string]float64{
		"driving":   r.Totals[CatDriving].Ratio(),
		"bindings":  r.Totals[CatBindings].Ratio(),
		"fields":    sum(CatDirect, CatConverted, CatConstant, CatNotPopulated),
		"computed":  r.Totals[CatComputed].Ratio(),
		"wrongHigh": float64(len(r.WrongHigh)),
	}
}

// Gate lists what fails the G10 gate (nil: it passes).
func (r Report) Gate() []string {
	q := r.Quality()
	var out []string
	for _, k := range []string{"driving", "bindings", "fields", "computed"} {
		if q[k] < GateMin[k] {
			out = append(out, fmt.Sprintf("%s %.0f%% < %.0f%%", k, 100*q[k], 100*GateMin[k]))
		}
	}
	if q["wrongHigh"] > 0 {
		out = append(out, fmt.Sprintf("%d wrong high-confidence proposal(s): %s", len(r.WrongHigh), strings.Join(r.WrongHigh, "; ")))
	}
	return out
}
