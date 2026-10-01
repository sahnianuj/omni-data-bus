package automap

import (
	"fmt"
	"sort"
	"strings"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// Binding suggestion weights and bands (OC-060).
const (
	bindWeightName = 0.35
	bindWeightFit  = 0.45
	bindWeightCard = 0.20
	// fieldMatch is the name score a field needs to count as matching a column.
	fieldMatch = DefaultSuggestThreshold
	// neutralFit scores a container with no scalar fields of its own (only nested containers):
	// its name and cardinality decide.
	neutralFit = 0.5
	// parentName is the name score of "map from parent": it has no table name to compare.
	parentName = 0.5
)

// Confidence bands of a binding suggestion.
const (
	ConfidenceHigh       = "high"       // ≥ 0.75
	ConfidenceMedium     = "medium"     // 0.50–0.75
	ConfidenceUnresolved = "unresolved" // below: the best candidate is shown, not suggested
	bandHigh             = 0.75
	bandMedium           = 0.50
)

// Link is a relation a node can be bound to from a table: a foreign-key or virtual hop, or a
// many-to-many through a junction (OC-042/044).
type Link struct {
	Kind    string
	Table   mapping.TableRef
	Join    []mapping.JoinPair
	Via     *mapping.Via
	Virtual bool
}

// Links lists the links out of a table.
type Links func(from mapping.TableRef) []Link

// BindingCandidate is one way to fill a node: a relation (with the adapter its cardinality
// needs), or FromParent: no relation, the node groups its parent scope's columns.
type BindingCandidate struct {
	FromParent  bool               `json:"fromParent,omitempty"`
	Kind        string             `json:"kind,omitempty"`
	Table       *mapping.TableRef  `json:"table,omitempty"`
	Join        []mapping.JoinPair `json:"join,omitempty"`
	Via         *mapping.Via       `json:"via,omitempty"`
	Virtual     bool               `json:"virtual,omitempty"`
	Adapter     string             `json:"adapter,omitempty"`
	Shape       string             `json:"shape,omitempty"`
	ValueColumn string             `json:"valueColumn,omitempty"`
	// Group: fill the array with the group's rows, grouping the driving table by GroupBy
	// (OC-063), ordered by OrderBy.
	Group   bool                  `json:"group,omitempty"`
	GroupBy []string              `json:"groupBy,omitempty"`
	OrderBy []mapping.OrderColumn `json:"orderBy,omitempty"`
	Score   float64               `json:"score"`
	Reasons []string              `json:"reasons"`
}

// BindingSuggestion is the proposal for one unbound object or array.
type BindingSuggestion struct {
	NodeID       string             `json:"nodeId"`
	Path         string             `json:"path"`
	Confidence   string             `json:"confidence"`
	Candidate    BindingCandidate   `json:"candidate"`
	Alternatives []BindingCandidate `json:"alternatives"`
}

// TableSuggestion ranks a table as the driving table.
type TableSuggestion struct {
	Table   mapping.TableRef `json:"table"`
	Score   float64          `json:"score"`
	Reasons []string         `json:"reasons"`
}

// Suggestions is POST …/suggest-bindings.
type Suggestions struct {
	Root        []TableSuggestion   `json:"root"`
	Suggestions []BindingSuggestion `json:"suggestions"`
}

// SuggestInput is what SuggestBindings reads.
type SuggestInput struct {
	Spec    *mapping.Spec
	Driving mapping.TableRef
	Table   func(mapping.TableRef) *schema.Table
	// Tables are the candidates for the driving table (Root).
	Tables []mapping.TableRef
	Links  Links
	Dicts  *Dicts
	// NodeID limits the result to one node ("" = every unbound node).
	NodeID string
	// Root also ranks the driving table from the root's fields.
	Root bool
}

// SuggestBindings proposes the driving table and a binding for every unbound object and array
// of a composed design (OC-060), top-down: each proposal (when not unresolved) is the scope its
// children are scored in, as if accepted. Deterministic: names, field fit and cardinality only.
func SuggestBindings(in SuggestInput) Suggestions {
	if in.Dicts == nil {
		in.Dicts = DefaultDicts()
	}
	sg := &suggester{in: in, cols: map[string][]colVariants{}, leaves: map[string][]variant{}, bound: map[string]*mapping.Relation{}}
	for i := range in.Spec.Relations {
		r := &in.Spec.Relations[i]
		if !r.LookupOnly && r.TargetFieldID != "" {
			sg.bound[r.TargetFieldID] = r
		}
	}
	out := Suggestions{Root: []TableSuggestion{}, Suggestions: []BindingSuggestion{}}
	root := &in.Spec.DocSchema.Root
	if in.Root {
		out.Root = sg.rankRoot(root)
	}
	driving := in.Driving
	sg.walk(root, "", &driving, &out)
	return out
}

type colVariants struct {
	name string
	vs   []variant
}

type suggester struct {
	in     SuggestInput
	cols   map[string][]colVariants
	leaves map[string][]variant // target variants by name and parent (cached: fields are scored against every candidate)
	bound  map[string]*mapping.Relation
}

// leaf is a field's target variants, as a child of parent ("" at the root).
func (sg *suggester) leaf(name, parent string) []variant {
	k := parent + "\x00" + name
	vs, ok := sg.leaves[k]
	if !ok {
		l := TargetLeaf{Name: name}
		if parent != "" {
			l.ParentNames = []string{parent}
		}
		vs = sg.in.Dicts.targetVariants(l)
		sg.leaves[k] = vs
	}
	return vs
}

// columns are a table's column variants (cached), plus junction payload columns.
func (sg *suggester) columns(t mapping.TableRef, via *mapping.Via) []colVariants {
	k := t.String()
	cv, ok := sg.cols[k]
	if !ok {
		if tb := sg.in.Table(t); tb != nil {
			for _, c := range tb.Columns {
				cv = append(cv, colVariants{name: c.Name, vs: sg.in.Dicts.sourceVariants(SourceCol{Name: c.Name, Table: t.Name, IsPK: c.IsPrimaryKey})})
			}
		}
		sg.cols[k] = cv
	}
	if via == nil || len(via.Columns) == 0 {
		return cv
	}
	out := append([]colVariants(nil), cv...)
	for _, c := range via.Columns {
		out = append(out, colVariants{name: c, vs: sg.in.Dicts.sourceVariants(SourceCol{Name: c, Table: via.Table.Name})})
	}
	return out
}

// own is a container's scalar fields in its own scope (not inside nested containers).
func own(f *docschema.DocField) []docschema.DocField {
	kids := f.Children
	if f.Type == docschema.Array && f.Items != nil {
		kids = f.Items.Children
	}
	var out []docschema.DocField
	for _, c := range kids {
		if c.Type.Scalar() {
			out = append(out, c)
		}
	}
	return out
}

// containers are a container's object and array children (through an array's item object).
func containers(f *docschema.DocField) []*docschema.DocField {
	kids := f.Children
	if f.Type == docschema.Array && f.Items != nil {
		kids = f.Items.Children
	}
	var out []*docschema.DocField
	for i := range kids {
		if !kids[i].Type.Scalar() {
			out = append(out, &kids[i])
		}
	}
	return out
}

// fit is how well fields match columns: the mean best name score of each field, counting only
// scores ≥ fieldMatch; matched is how many fields reached it.
func (sg *suggester) fit(fields []docschema.DocField, cols []colVariants, parent string) (score float64, matched int) {
	if len(fields) == 0 {
		return neutralFit, 0
	}
	total := 0.0
	for _, f := range fields {
		tvs := sg.leaf(f.Name, parent)
		best := 0.0
		for _, c := range cols {
			if s, _ := sg.in.Dicts.nameScore(c.vs, tvs); s > best {
				best = s
			}
		}
		if best >= fieldMatch {
			total += best
			matched++
		}
	}
	return total / float64(len(fields)), matched
}

// cardinality scores a node type against a relation kind, with the adapter a mismatch needs.
func cardinality(t docschema.FieldType, kind string) (score float64, adapter, reason string) {
	many := kind != mapping.EmbedManyToOne
	switch {
	case t == docschema.Array && many, t == docschema.Object && !many:
		return 1, "", kind + " ✓"
	case t == docschema.Object:
		return 0.5, docschema.AdapterFirst, "object ← " + kind + ": first row"
	default:
		return 0.5, docschema.AdapterWrap, "array ← " + kind + ": 0 or 1 item"
	}
}

// valueColumn picks the column a scalar array takes its values from: the best name match for
// the item (the array's name), else the first text column; key and join columns never qualify.
func (sg *suggester) valueColumn(node string, l Link) (string, float64) {
	tb := sg.in.Table(l.Table)
	if tb == nil {
		return "", 0
	}
	skip := map[string]bool{}
	for _, k := range tb.PrimaryKey {
		skip[k] = true
	}
	for _, j := range l.Join {
		skip[j.Child] = true
	}
	if l.Via != nil {
		for _, j := range l.Via.ChildJoin {
			skip[j.Child] = true
		}
	}
	tvs := sg.leaf(node, "")
	best, name := 0.0, ""
	for _, c := range sg.columns(l.Table, nil) {
		if skip[c.name] {
			continue
		}
		if s, _ := sg.in.Dicts.nameScore(c.vs, tvs); s > best {
			best, name = s, c.name
		}
	}
	if best >= fieldMatch {
		return name, best
	}
	for _, c := range tb.Columns {
		if c.JSONType == schema.TypeString && !skip[c.Name] && !c.IsPrimaryKey {
			return c.Name, fieldMatch
		}
	}
	return "", 0
}

func (sg *suggester) candidates(f *docschema.DocField, scope mapping.TableRef, atRoot bool) []BindingCandidate {
	fields := own(f)
	scalarArray := f.Type == docschema.Array && f.Items != nil && f.Items.Type.Scalar()
	var out []BindingCandidate
	for _, l := range sg.in.Links(scope) {
		if sg.in.Table(l.Table) == nil {
			continue
		}
		name, _ := sg.in.Dicts.nameScore(sg.in.Dicts.sourceVariants(SourceCol{Name: l.Table.Name, Table: scope.Name}), sg.leaf(f.Name, ""))
		card, adapter, cardReason := cardinality(f.Type, l.Kind)
		table := l.Table
		c := BindingCandidate{Kind: l.Kind, Table: &table, Join: l.Join, Via: l.Via, Virtual: l.Virtual, Adapter: adapter}
		var fit float64
		var fitReason string
		switch {
		case scalarArray:
			col, s := sg.valueColumn(f.Name, l)
			if col == "" {
				continue
			}
			fit, c.Shape, c.ValueColumn = s, "scalars", col
			fitReason = "values from " + col
		default:
			var matched int
			fit, matched = sg.fit(fields, sg.columns(l.Table, l.Via), f.Name)
			fitReason = fmt.Sprintf("fields %d/%d", matched, len(fields))
		}
		c.Score = round2(bindWeightName*name + bindWeightFit*fit + bindWeightCard*card)
		c.Reasons = []string{fmt.Sprintf("name: %s ~ %s (%.2f)", f.Name, l.Table.Name, name), fitReason, cardReason}
		if l.Via != nil {
			c.Reasons = append(c.Reasons, "through "+l.Via.Table.Name)
		}
		out = append(out, c)
	}
	if atRoot && f.Type == docschema.Array && !sg.in.Spec.Grouped() {
		if c, ok := sg.groupCandidate(f, fields, scope); ok {
			out = append(out, c)
		}
	}
	// An object can also just group fields of its parent's rows.
	if f.Type == docschema.Object && len(fields) > 0 {
		fit, matched := sg.fit(fields, sg.columns(scope, nil), f.Name)
		if matched > 0 {
			out = append(out, BindingCandidate{
				FromParent: true,
				Score:      round2(bindWeightName*parentName + bindWeightFit*fit + bindWeightCard),
				Reasons:    []string{fmt.Sprintf("fields %d/%d from %s", matched, len(fields), scope.Name), "groups the parent's columns"},
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return candidateKey(out[i]) < candidateKey(out[j])
	})
	return out
}

func candidateKey(c BindingCandidate) string {
	if c.FromParent {
		return "~parent"
	}
	return c.Table.String() + "|" + c.Kind + "|" + fmt.Sprint(c.Join)
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func band(score float64) string {
	switch {
	case score >= bandHigh:
		return ConfidenceHigh
	case score >= bandMedium:
		return ConfidenceMedium
	}
	return ConfidenceUnresolved
}

// walk visits f's containers in scope (nil: unknown, nothing is suggested below).
func (sg *suggester) walk(f *docschema.DocField, path string, scope *mapping.TableRef, out *Suggestions) {
	for _, c := range containers(f) {
		p := c.Name
		if path != "" {
			p = path + "." + c.Name
		}
		if c.Type == docschema.Array {
			p += "[]"
		}
		if r := sg.bound[c.ID]; r != nil {
			t := r.Table
			sg.walk(c, p, &t, out)
			continue
		}
		if scope == nil {
			sg.walk(c, p, nil, out)
			continue
		}
		cands := sg.candidates(c, *scope, path == "")
		if len(cands) == 0 {
			if sg.in.NodeID == "" || sg.in.NodeID == c.ID {
				out.Suggestions = append(out.Suggestions, BindingSuggestion{NodeID: c.ID, Path: p, Confidence: ConfidenceUnresolved, Alternatives: []BindingCandidate{}})
			}
			sg.walk(c, p, nil, out)
			continue
		}
		best := cands[0]
		s := BindingSuggestion{NodeID: c.ID, Path: p, Confidence: band(best.Score), Candidate: best, Alternatives: cands[1:min(len(cands), 3)]}
		if sg.in.NodeID == "" || sg.in.NodeID == c.ID {
			out.Suggestions = append(out.Suggestions, s)
		}
		switch {
		case s.Confidence == ConfidenceUnresolved:
			sg.walk(c, p, nil, out)
		case best.FromParent:
			sg.walk(c, p, scope, out)
		default:
			sg.walk(c, p, best.Table, out)
		}
	}
}

// groupCandidate proposes filling a root array with the rows of a group of the driving table
// (OC-063): the array's fields are the table's own columns, and the root's fields name key
// columns to group on (order_detail: orderId at the root, lineNo… in products[]).
func (sg *suggester) groupCandidate(f *docschema.DocField, fields []docschema.DocField, driving mapping.TableRef) (BindingCandidate, bool) {
	t := sg.in.Table(driving)
	if t == nil || len(fields) == 0 {
		return BindingCandidate{}, false
	}
	cols := sg.columns(driving, nil)
	best := func(fd docschema.DocField, parent string) string {
		tvs := sg.leaf(fd.Name, parent)
		top, name := 0.0, ""
		for _, c := range cols {
			if s, _ := sg.in.Dicts.nameScore(c.vs, tvs); s > top {
				top, name = s, c.name
			}
		}
		if top >= fieldMatch {
			return name
		}
		return ""
	}
	inArray := map[string]bool{}
	for _, fd := range fields {
		if c := best(fd, f.Name); c != "" {
			inArray[c] = true
		}
	}
	pk := map[string]bool{}
	for _, k := range t.PrimaryKey {
		pk[k] = true
	}
	var groupBy []string
	for _, fd := range own(&sg.in.Spec.DocSchema.Root) {
		c := best(fd, "")
		if c == "" || inArray[c] || contains(groupBy, c) {
			continue
		}
		if pk[c] || strings.HasSuffix(c, "_id") || c == "id" {
			groupBy = append(groupBy, c)
		}
	}
	if len(groupBy) == 0 {
		return BindingCandidate{}, false
	}
	// Grouping on the whole primary key is a document per row: nothing to group.
	if covered := len(t.PrimaryKey) > 0; covered {
		for _, k := range t.PrimaryKey {
			covered = covered && contains(groupBy, k)
		}
		if covered {
			return BindingCandidate{}, false
		}
	}
	fit, matched := sg.fit(fields, cols, f.Name)
	if matched < 2 {
		return BindingCandidate{}, false
	}
	var order []mapping.OrderColumn
	for _, k := range t.PrimaryKey {
		if !contains(groupBy, k) {
			order = append(order, mapping.OrderColumn{Column: k})
		}
	}
	table := driving
	join := make([]mapping.JoinPair, len(groupBy))
	for i, c := range groupBy {
		join[i] = mapping.JoinPair{Parent: c, Child: c}
	}
	return BindingCandidate{
		Group: true, GroupBy: groupBy, OrderBy: order, Kind: mapping.EmbedOneToMany, Table: &table, Join: join,
		Score: round2(bindWeightName*parentName + bindWeightFit*fit + bindWeightCard),
		Reasons: []string{
			"one document per distinct " + strings.Join(groupBy, ", "),
			fmt.Sprintf("fields %d/%d from %s", matched, len(fields), driving.Name),
			"the group's rows",
		},
	}, true
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// rankRoot ranks tables as the driving table by the root's own fields (top 3).
func (sg *suggester) rankRoot(root *docschema.DocField) []TableSuggestion {
	fields := own(root)
	if len(fields) == 0 {
		return []TableSuggestion{}
	}
	var out []TableSuggestion
	for _, t := range sg.in.Tables {
		fit, matched := sg.fit(fields, sg.columns(t, nil), "")
		if matched == 0 {
			continue
		}
		out = append(out, TableSuggestion{Table: t, Score: round2(fit), Reasons: []string{fmt.Sprintf("fields %d/%d", matched, len(fields))}})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Table.String() < out[j].Table.String()
	})
	return out[:min(len(out), 3)]
}
