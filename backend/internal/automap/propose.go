package automap

import (
	"crypto/sha1" //nolint:gosec // deterministic ids, not security
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/naming"
	"omnidatabus/internal/schema"
)

// Design-first mapping (OC-163): from a customer's document design and the connected source,
// propose the whole mapping at once — the driving table, the relation behind every object and
// array, and what fills every field — with a confidence and the reasons for each. The proposal
// is an ordinary spec plus one item per container and field; nothing is saved until the user
// applies it.

// Proposal item kinds and statuses.
const (
	ItemBinding = "binding"
	ItemField   = "field"

	StatusBound         = "bound"
	StatusFromParent    = "fromParent"
	StatusMapped        = "mapped"
	StatusConverted     = "converted"
	StatusLookup        = "lookup"
	StatusConstant      = "constant"
	StatusComputed      = "computed"
	StatusAggregate     = "aggregate"
	StatusNotPopulated  = "notPopulated"
	StatusUnresolved    = "unresolved"
	StatusAlreadyMapped = "alreadyMapped"

	BandHigh   = "high"
	BandMedium = "medium"
	BandLow    = "low"
)

// Unresolved reason codes (OC-169).
const (
	CodeNoColumn          = "NO_COLUMN"
	CodeNoPath            = "NO_PATH"
	CodeTypeMismatch      = "TYPE_MISMATCH"
	CodeDepthLimit        = "DEPTH_LIMIT"
	CodeReferenceTemplate = "REFERENCE_TEMPLATE"
	CodeUntypedItems      = "UNTYPED_ITEMS"
)

// DefaultMaxDepth is OC_MAX_RELATION_DEPTH's default (OC-043).
const DefaultMaxDepth = 4

// Driving-table weights (OC-163).
const (
	drivingFit  = 0.40
	drivingRch  = 0.35
	drivingKey  = 0.15
	drivingName = 0.10
)

// ProposeInput is what Propose reads. Table and Links describe the connected source (the
// project's discovery snapshot and relationship graph), never an uploaded file.
type ProposeInput struct {
	// Spec holds the design (DocSchema). With Base, its relations and mappings are kept and
	// only what's missing is proposed (OC-170); otherwise they're ignored.
	Spec *mapping.Spec
	Base bool
	// Driving fixes the driving table; nil chooses it (or keeps Spec.Root with Base).
	Driving *mapping.TableRef
	// NodeID limits what's added to the fields and containers under that node ("" = all).
	NodeID   string
	Tables   []mapping.TableRef
	Table    func(mapping.TableRef) *schema.Table
	Links    Links
	Dicts    *Dicts
	MaxDepth int
	// Values, when set, returns up to n distinct values of a text column of the connected
	// source (a variant discriminator, OC-171).
	Values func(t mapping.TableRef, column string, n int) []string
}

// Proposal is POST …/propose.
type Proposal struct {
	// Driving ranks the driving tables, best first; Spec drives from the first.
	Driving []TableSuggestion `json:"driving"`
	// Spec is the design with every proposal applied.
	Spec mapping.Spec `json:"spec"`
	// Items are the containers and fields, in design order, with what's proposed for each.
	Items    []ProposalItem `json:"items"`
	Coverage Coverage       `json:"coverage"`
}

// ProposalItem is what's proposed for one container or field.
type ProposalItem struct {
	FieldID    string   `json:"fieldId"`
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Status     string   `json:"status"`
	Band       string   `json:"band,omitempty"`
	Score      float64  `json:"score"`
	Summary    string   `json:"summary"`
	Reasons    []string `json:"reasons"`
	Required   bool     `json:"required,omitempty"`
	Code       string   `json:"code,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	// Alternatives are other ways to fill a container (OC-164).
	Alternatives []BindingCandidate `json:"alternatives,omitempty"`
	// What the item added to Spec, so a client can take it back.
	RelationIDs  []string `json:"relationIds,omitempty"`
	MappingIDs   []string `json:"mappingIds,omitempty"`
	AggregateIDs []string `json:"aggregateIds,omitempty"`
	// Fix is the expression a REFERENCE_TEMPLATE field could use (OC-169).
	Fix string `json:"fix,omitempty"`
}

// Coverage counts the design's fields by outcome.
type Coverage struct {
	Fields       int `json:"fields"`
	Proposed     int `json:"proposed"`
	NeedsReview  int `json:"needsReview"`
	NotPopulated int `json:"notPopulated"`
	Unresolved   int `json:"unresolved"`
	Containers   int `json:"containers"`
	Bound        int `json:"bound"`
}

func fieldBand(score float64) string {
	switch {
	case score >= DefaultAutoThreshold:
		return BandHigh
	case score >= DefaultSuggestThreshold:
		return BandMedium
	}
	return BandLow
}

func bindBand(score float64) string {
	switch {
	case score >= bandHigh:
		return BandHigh
	case score >= bandMedium:
		return BandMedium
	}
	return BandLow
}

func shortID(prefix string, parts ...string) string {
	sum := sha1.Sum([]byte(strings.Join(parts, "\x00"))) //nolint:gosec // see import
	return prefix + hex.EncodeToString(sum[:])[:10]
}

// proposer carries one Propose call.
type proposer struct {
	in       ProposeInput
	sg       *suggester
	spec     *mapping.Spec
	driving  mapping.TableRef
	items    []ProposalItem
	itemOf   map[string]int // field id → items index
	keepRels map[string]bool
	mapped   map[string]bool // fields that already have a decision (Base)
	paths    map[string]string
	keyPfx   string
	under    map[string]bool // fields under NodeID (nil = all)
	// onlyLookup, while a variant's fields are proposed, limits the lookups to its own table.
	onlyLookup func(Link) bool
	// candCache holds a container's candidates from a table: ranking and the walk ask twice.
	candCache map[string][]BindingCandidate
}

// cands is sg.candidates, computed once per container, table and position.
func (p *proposer) cands(c *docschema.DocField, t mapping.TableRef, atRoot bool) []BindingCandidate {
	k := c.ID + "|" + t.String() + "|" + strconv.FormatBool(atRoot)
	if cs, ok := p.candCache[k]; ok {
		return cs
	}
	if p.candCache == nil {
		p.candCache = map[string][]BindingCandidate{}
	}
	cs := p.sg.candidates(c, t, atRoot)
	p.candCache[k] = cs
	return cs
}

// Propose proposes the whole mapping of a design against the connected source (OC-163).
func Propose(in ProposeInput) Proposal {
	if in.Dicts == nil {
		in.Dicts = DefaultDicts()
	}
	if in.MaxDepth <= 0 {
		in.MaxDepth = DefaultMaxDepth
	}
	spec := deepSpec(in.Spec)
	spec.Mode = mapping.ModeComposed
	if !in.Base {
		spec.Relations, spec.FieldMappings, spec.Aggregates = nil, nil, nil
		clearBindings(&spec.DocSchema.Root)
		if in.Driving == nil {
			spec.Root = nil
		}
	}
	spec.Normalize()
	p := &proposer{in: in, spec: spec, itemOf: map[string]int{}, keepRels: map[string]bool{}, mapped: map[string]bool{}, paths: spec.DocSchema.Paths()}
	p.sg = &suggester{in: SuggestInput{Spec: spec, Table: in.Table, Links: in.Links, Dicts: in.Dicts, Tables: in.Tables}, cols: map[string][]colVariants{}, leaves: map[string][]variant{}, bound: map[string]*mapping.Relation{}}
	for _, m := range spec.FieldMappings {
		if m.State != mapping.StateRejected {
			p.mapped[m.TargetFieldID] = true
		}
	}
	for _, a := range spec.Aggregates {
		p.mapped[a.TargetFieldID] = true
	}
	for i := range spec.Relations {
		r := &spec.Relations[i]
		p.keepRels[r.ID] = true
		if !r.LookupOnly && r.TargetFieldID != "" {
			p.sg.bound[r.TargetFieldID] = r
		}
	}
	if in.NodeID != "" {
		p.under = map[string]bool{}
		if n := spec.DocSchema.Find(in.NodeID); n != nil {
			markUnder(n, p.under)
		}
	}
	root := &spec.DocSchema.Root
	hints := docschema.ParseHints(root.Description)
	if pfx, _, ok := strings.Cut(hints.KeyTemplate, "::"); ok {
		p.keyPfx = pfx
	}

	var ranked []TableSuggestion
	switch {
	case in.Driving != nil:
		p.driving = *in.Driving
		ranked = []TableSuggestion{{Table: p.driving, Score: 1, Reasons: []string{"chosen"}}}
	case in.Base && spec.Root != nil:
		p.driving = spec.Root.Table
		ranked = []TableSuggestion{{Table: p.driving, Score: 1, Reasons: []string{"the mapping's driving table"}}}
	default:
		ranked = p.rankDriving(root, hints)
		if len(ranked) == 0 {
			return Proposal{Driving: []TableSuggestion{}, Spec: *spec, Items: []ProposalItem{}}
		}
		p.driving = ranked[0].Table
	}
	spec.Root = &mapping.Root{Table: p.driving}
	p.sg.in.Driving = p.driving
	// The design has its own type field (a const, OC-166): the tool's would collide with it.
	if spec.TypeField.Enabled {
		for _, c := range root.Children {
			if c.Name == spec.TypeField.Name {
				spec.TypeField.Enabled = false
			}
		}
	}
	if t := in.Table(p.driving); t != nil && spec.Key.Template == "" && p.keyPfx != "" && len(t.PrimaryKey) == 1 {
		spec.Key.Template = p.keyPfx + "::{" + t.PrimaryKey[0] + "}"
	}

	p.scope(root, "", p.driving, nil, 0, nil)
	spec.Normalize()
	out := Proposal{Driving: ranked, Spec: *spec, Items: p.ordered(), Coverage: p.coverage()}
	return out
}

func markUnder(f *docschema.DocField, out map[string]bool) {
	out[f.ID] = true
	for i := range f.Children {
		markUnder(&f.Children[i], out)
	}
	if f.Items != nil {
		markUnder(f.Items, out)
	}
}

func clearBindings(f *docschema.DocField) {
	f.Binding = nil
	for i := range f.Children {
		clearBindings(&f.Children[i])
	}
	if f.Items != nil {
		clearBindings(f.Items)
	}
}

func deepSpec(s *mapping.Spec) *mapping.Spec {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	var out mapping.Spec
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return &out
}

func (p *proposer) allowed(fieldID string) bool { return p.under == nil || p.under[fieldID] }

func (p *proposer) add(it ProposalItem) {
	if it.Reasons == nil {
		it.Reasons = []string{}
	}
	if i, ok := p.itemOf[it.FieldID]; ok {
		p.items[i] = it
		return
	}
	p.itemOf[it.FieldID] = len(p.items)
	p.items = append(p.items, it)
}

// ordered returns the items in design order.
func (p *proposer) ordered() []ProposalItem {
	order := map[string]int{}
	n := 0
	p.spec.DocSchema.Walk(func(f *docschema.DocField, _ string, _ *docschema.DocField) {
		order[f.ID] = n
		n++
	})
	out := append([]ProposalItem{}, p.items...)
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].FieldID] < order[out[j].FieldID] })
	return out
}

func (p *proposer) coverage() Coverage {
	var c Coverage
	for _, it := range p.items {
		if it.Kind == ItemBinding {
			c.Containers++
			if it.Status == StatusBound || it.Status == StatusFromParent || it.Status == StatusAlreadyMapped {
				c.Bound++
			}
			continue
		}
		c.Fields++
		switch it.Status {
		case StatusNotPopulated:
			c.NotPopulated++
		case StatusUnresolved:
			c.Unresolved++
		default:
			c.Proposed++
			if it.Band != BandHigh && it.Status != StatusAlreadyMapped {
				c.NeedsReview++
			}
		}
	}
	return c
}

// ── Driving table ────────────────────────────────────────────────────────────

func (p *proposer) rankDriving(root *docschema.DocField, h docschema.Hints) []TableSuggestion {
	fields := own(root)
	// Containers the design leaves empty say nothing about the table.
	var conts []*docschema.DocField
	for _, c := range containers(root) {
		if !docschema.ParseHints(c.Description).NotPopulated {
			conts = append(conts, c)
		}
	}
	title := strings.TrimSuffix(strings.TrimSuffix(p.spec.DocSchema.Root.Name, "Document"), "Doc")
	var titleVs []variant
	if t := documentTitle(p.spec); t != "" {
		title = t
		titleVs = p.sg.leaf(title, "")
	}
	var out []TableSuggestion
	for _, t := range p.shortlist(fields, titleVs) {
		tb := p.in.Table(t)
		if tb == nil {
			continue
		}
		fit, matched := p.sg.fit(fields, p.sg.columns(t, nil), "")
		if matched == 0 && len(fields) > 0 && len(conts) == 0 {
			continue
		}
		reach, reached := neutralFit, 0
		if len(conts) > 0 {
			// A container filled through a relationship of the table is strong evidence; one that
			// only groups the table's own columns, half as strong.
			weight := 0.0
			for _, c := range conts {
				if cs := p.cands(c, t, true); len(cs) > 0 && cs[0].Score >= bandMedium {
					reached++
					if cs[0].FromParent {
						weight += 0.5
					} else {
						weight++
					}
				}
			}
			reach = weight / float64(len(conts))
		}
		key, keyReason := 0.0, ""
		if p.keyPfx != "" && len(tb.PrimaryKey) == 1 {
			if naming.Singular(strings.ToLower(t.Name)) == naming.Singular(strings.ToLower(p.keyPfx)) {
				key, keyReason = 1, fmt.Sprintf("key: %s ↔ %s.%s", h.KeyTemplate, t.Name, tb.PrimaryKey[0])
			} else if strings.Contains(h.KeyTemplate, "{uuid}") && tb.Column(tb.PrimaryKey[0]) != nil && tb.Column(tb.PrimaryKey[0]).UDTName == "uuid" {
				key, keyReason = 0.5, "key: a uuid primary key"
			}
		}
		name := 0.0
		if len(titleVs) > 0 {
			name, _ = p.sg.in.Dicts.nameScore(p.sg.in.Dicts.sourceVariants(SourceCol{Name: t.Name}), titleVs)
		}
		if matched == 0 && reached == 0 {
			continue // nothing of the design comes from this table
		}
		score := round2(drivingFit*fit + drivingRch*reach + drivingKey*key + drivingName*name)
		reasons := []string{fmt.Sprintf("fields %d/%d", matched, len(fields))}
		if len(conts) > 0 {
			reasons = append(reasons, fmt.Sprintf("reach %d/%d containers", reached, len(conts)))
		}
		if keyReason != "" {
			reasons = append(reasons, keyReason)
		}
		if name >= fieldMatch {
			reasons = append(reasons, fmt.Sprintf("name: %s ~ %s (%.2f)", title, t.Name, name))
		}
		out = append(out, TableSuggestion{Table: t, Score: score, Reasons: reasons})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Table.String() < out[j].Table.String()
	})
	return out[:min(len(out), 3)]
}

// drivingShortlist bounds how many tables get the full (containers × candidates) evaluation.
const drivingShortlist = 20

// shortlist picks the tables worth ranking in full by a cheap pre-score: how well the root's own
// fields and the design's name match the table, and whether it has relationships to fill
// containers with. Small sources are ranked in full.
func (p *proposer) shortlist(fields []docschema.DocField, titleVs []variant) []mapping.TableRef {
	if len(p.in.Tables) <= drivingShortlist {
		return p.in.Tables
	}
	type scored struct {
		t     mapping.TableRef
		score float64
	}
	var all []scored
	for _, t := range p.in.Tables {
		if p.in.Table(t) == nil {
			continue
		}
		fit, matched := p.sg.fit(fields, p.sg.columns(t, nil), "")
		if matched == 0 {
			fit = 0
		}
		name := 0.0
		if len(titleVs) > 0 {
			name, _ = p.sg.in.Dicts.nameScore(p.sg.in.Dicts.sourceVariants(SourceCol{Name: t.Name}), titleVs)
		}
		links := float64(min(len(p.in.Links(t)), 10)) / 10
		all = append(all, scored{t, fit + 0.5*name + 0.3*links})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].t.String() < all[j].t.String()
	})
	out := make([]mapping.TableRef, 0, drivingShortlist)
	for _, s := range all[:min(len(all), drivingShortlist)] {
		out = append(out, s.t)
	}
	return out
}

// documentTitle is the design's title from the imported JSON Schema ("CartDocument" → "Cart"),
// or the root's description's first word; "" when unknown.
func documentTitle(s *mapping.Spec) string {
	if s.DocSchema.Imported != nil && s.DocSchema.Imported.FileName != "" {
		n := s.DocSchema.Imported.FileName
		n = strings.TrimSuffix(strings.TrimSuffix(n, ".json"), ".schema")
		return n
	}
	return ""
}

// ── Containers ───────────────────────────────────────────────────────────────

// scope proposes the fields of node f (filled from table, through relation relID; "" is the
// root) and then its containers, depth levels below the root. via is the relation's junction.
func (p *proposer) scope(f *docschema.DocField, relID string, table mapping.TableRef, via *mapping.Via, depth int, parentNames []string) {
	// Fields first: they decide nothing below, and lookups they create are in this scope.
	p.fields(f, relID, table, via, parentNames)
	for _, c := range containers(f) {
		p.container(c, relID, table, depth, parentNames)
	}
}

func (p *proposer) container(c *docschema.DocField, relID string, scopeTable mapping.TableRef, depth int, parentNames []string) {
	path := p.paths[c.ID]
	names := append(append([]string{}, parentNames...), c.Name)
	itemPath := path
	if c.Type == docschema.Array {
		itemPath += "[]"
	}
	if r := p.sg.bound[c.ID]; r != nil {
		// Already bound (Base, OC-170): keep it, propose below it.
		p.add(ProposalItem{FieldID: c.ID, Path: itemPath, Kind: ItemBinding, Status: StatusAlreadyMapped, Band: BandHigh, Score: 1, Summary: "← " + r.Table.Name, Reasons: []string{"already bound"}})
		p.scope(c, r.ID, r.Table, r.Via, depth+1, names)
		return
	}
	if h := docschema.ParseHints(c.Description); h.NotPopulated {
		p.notPopulatedTree(c, itemPath)
		return
	}
	if !p.allowed(c.ID) {
		p.walkUnresolved(c, names)
		return
	}
	if c.Type == docschema.Array && c.Items != nil && c.Items.HasVariants() && p.variants(c, relID, scopeTable, depth, names) {
		return
	}
	if c.Type == docschema.Array && c.Items != nil && c.Items.Type == docschema.Object && len(c.Items.Children) == 0 {
		p.add(ProposalItem{
			FieldID: c.ID, Path: itemPath, Kind: ItemBinding, Status: StatusUnresolved, Band: BandLow, Code: CodeUntypedItems,
			Summary: "items have no fields", Reasons: []string{"the design gives the items no fields: import their designs (variants)"},
		})
		return
	}
	cands := append([]BindingCandidate(nil), p.cands(c, scopeTable, relID == "")...)
	// Grouping the driving table is an explicit choice (OC-063), never proposed here.
	cands = filterCandidates(cands, func(bc BindingCandidate) bool { return !bc.Group })
	if depth+1 > p.in.MaxDepth {
		cands = filterCandidates(cands, func(bc BindingCandidate) bool { return bc.FromParent })
		if len(cands) == 0 {
			p.add(ProposalItem{
				FieldID: c.ID, Path: itemPath, Kind: ItemBinding, Status: StatusUnresolved, Band: BandLow, Code: CodeDepthLimit,
				Summary: "deepest level reached", Reasons: []string{fmt.Sprintf("Deepest level reached (%d of %d). An administrator can raise OC_MAX_RELATION_DEPTH up to 6.", depth, p.in.MaxDepth)},
			})
			p.walkUnresolved(c, names)
			return
		}
	}
	if len(cands) == 0 || cands[0].Score < bandMedium {
		it := ProposalItem{
			FieldID: c.ID, Path: itemPath, Kind: ItemBinding, Status: StatusUnresolved, Band: BandLow, Code: CodeNoPath,
			Summary: "no related table fits", Reasons: []string{"no relationship from " + scopeTable.Name + " reaches a table that fits"},
		}
		if len(cands) > 0 {
			it.Alternatives = cands[:min(len(cands), 3)]
			it.Score = cands[0].Score
			if cands[0].Table != nil {
				it.Candidates = []string{cands[0].Table.String()}
			}
		}
		p.add(it)
		p.walkUnresolved(c, names)
		return
	}
	best := cands[0]
	it := ProposalItem{FieldID: c.ID, Path: itemPath, Kind: ItemBinding, Band: bindBand(best.Score), Score: best.Score, Reasons: best.Reasons, Alternatives: cands[1:min(len(cands), 3)]}
	if len(cands) > 1 && cands[1].Score >= best.Score-0.05 && cands[1].Score >= bandMedium {
		it.Reasons = append(append([]string{}, it.Reasons...), "close alternative: "+describeCandidate(cands[1]))
		if it.Band == BandHigh {
			it.Band = BandMedium
		}
	}
	if best.FromParent {
		it.Status, it.Summary = StatusFromParent, "fields of "+scopeTable.Name
		p.add(it)
		p.scope(c, relID, scopeTable, nil, depth, names)
		return
	}
	rel := mapping.Relation{
		ID: shortID("rel_", c.ID, best.Table.String(), fmt.Sprint(best.Join)), Kind: best.Kind, Table: *best.Table, Join: best.Join, Via: best.Via,
		TargetFieldID: c.ID, Shape: best.Shape, ValueColumn: best.ValueColumn,
	}
	if relID != "" {
		pid := relID
		rel.ParentID = &pid
	}
	adapter := best.Adapter
	if adapter == docschema.AdapterFirst {
		if o := p.firstOrder(*best.Table); o != nil {
			rel.OrderBy = []mapping.OrderColumn{*o}
			it.Reasons = append(it.Reasons, "first by "+o.Column)
		}
	}
	if rel.Many() && adapter != docschema.AdapterFirst {
		rel.Limit = mapping.DefaultEmbedLimit
	}
	p.spec.Relations = append(p.spec.Relations, rel)
	c.Binding = &docschema.Binding{RelationID: rel.ID, Adapter: adapter}
	p.sg.bound[c.ID] = &p.spec.Relations[len(p.spec.Relations)-1]
	it.Status, it.Summary, it.RelationIDs = StatusBound, bindSummary(best), []string{rel.ID}
	p.add(it)
	p.scope(c, rel.ID, rel.Table, rel.Via, depth+1, names)
}

func filterCandidates(cs []BindingCandidate, keep func(BindingCandidate) bool) []BindingCandidate {
	out := cs[:0:0]
	for _, c := range cs {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

func describeCandidate(c BindingCandidate) string {
	if c.FromParent {
		return "the parent's own columns"
	}
	var j []string
	for _, p := range c.Join {
		j = append(j, p.Parent+" = "+c.Table.Name+"."+p.Child)
	}
	return c.Table.Name + " (" + strings.Join(j, ", ") + ")"
}

// bindSummary reads like the review's tree row: "↙ customers 1:N · first".
func bindSummary(c BindingCandidate) string {
	dir := "↗"
	if c.Kind != mapping.EmbedManyToOne || c.Cardinality == mapping.CardinalityOneToOne && c.Direction == mapping.DirectionReferencedBy {
		dir = "↙"
	}
	card := map[string]string{mapping.CardinalityOneToOne: "1:1", mapping.CardinalityManyToOne: "N:1", mapping.CardinalityOneToMany: "1:N"}[c.Cardinality]
	if c.Kind == mapping.RelationManyToMany {
		card, dir = "M:N", "↔"
	}
	if card == "" {
		card = map[string]string{mapping.EmbedManyToOne: "N:1", mapping.EmbedOneToMany: "1:N"}[c.Kind]
	}
	s := dir + " " + c.Table.Name + " " + card
	if c.Adapter != "" {
		s += " · " + c.Adapter
	}
	return s
}

// firstOrder is the column an object over a 1:N takes its first row by: the newest update, the
// newest row, else the primary key.
func (p *proposer) firstOrder(t mapping.TableRef) *mapping.OrderColumn {
	tb := p.in.Table(t)
	if tb == nil {
		return nil
	}
	for _, n := range []string{"updated_at", "modified_at", "created_at"} {
		if tb.Column(n) != nil {
			return &mapping.OrderColumn{Column: n, Desc: true}
		}
	}
	if len(tb.PrimaryKey) == 1 {
		return &mapping.OrderColumn{Column: tb.PrimaryKey[0]}
	}
	return nil
}

// notPopulatedTree marks a container the design leaves empty, and everything in it.
func (p *proposer) notPopulatedTree(c *docschema.DocField, path string) {
	p.add(ProposalItem{FieldID: c.ID, Path: path, Kind: ItemBinding, Status: StatusNotPopulated, Band: BandHigh, Score: 1, Summary: "not populated (design)", Reasons: []string{"the design says it isn't populated"}})
	var walk func(f *docschema.DocField)
	walk = func(f *docschema.DocField) {
		for _, l := range ownLeaves(f) {
			p.notPopulated(l, nil)
		}
		for _, k := range containers(f) {
			kp := p.paths[k.ID]
			if k.Type == docschema.Array {
				kp += "[]"
			}
			p.add(ProposalItem{FieldID: k.ID, Path: kp, Kind: ItemBinding, Status: StatusNotPopulated, Band: BandHigh, Score: 1, Summary: "not populated (design)", Reasons: []string{"inside " + c.Name + ", which isn't populated"}})
			walk(k)
		}
	}
	walk(c)
}

// walkUnresolved lists the fields under a container nothing fills.
func (p *proposer) walkUnresolved(c *docschema.DocField, names []string) {
	for _, l := range ownLeaves(c) {
		if p.mapped[l.ID] {
			continue
		}
		if h := docschema.ParseHints(l.Description); h.NotPopulated {
			p.notPopulated(l, nil)
			continue
		}
		p.add(ProposalItem{
			FieldID: l.ID, Path: p.paths[l.ID], Kind: ItemField, Status: StatusUnresolved, Band: BandLow, Code: CodeNoPath, Required: l.Required,
			Summary: "unresolved", Reasons: []string{c.Name + " has no source table"},
		})
	}
	for _, k := range containers(c) {
		kp := p.paths[k.ID]
		if k.Type == docschema.Array {
			kp += "[]"
		}
		if h := docschema.ParseHints(k.Description); h.NotPopulated {
			p.notPopulatedTree(k, kp)
			continue
		}
		p.add(ProposalItem{FieldID: k.ID, Path: kp, Kind: ItemBinding, Status: StatusUnresolved, Band: BandLow, Code: CodeNoPath, Summary: "unresolved", Reasons: []string{c.Name + " has no source table"}})
		p.walkUnresolved(k, append(names, k.Name))
	}
}

// ownLeaves are a container's own mappable fields: scalars and arrays of scalars (through an
// array's item object).
func ownLeaves(f *docschema.DocField) []*docschema.DocField {
	kids := f.Children
	if f.Type == docschema.Array && f.Items != nil {
		kids = f.Items.Children
	}
	var out []*docschema.DocField
	for i := range kids {
		k := &kids[i]
		if k.Type.Scalar() || (k.Type == docschema.Array && k.Items != nil && k.Items.Type.Scalar()) {
			out = append(out, k)
		}
	}
	return out
}

// ── Fields ───────────────────────────────────────────────────────────────────

// scopeCol is a column a field of the scope can read: the scope's own row, its junction's
// payload, or a lookup (a one-to-one or many-to-one table next to it).
type scopeCol struct {
	name   string
	col    *schema.Column
	table  mapping.TableRef
	vs     []variant
	lookup *Link  // nil: the scope's own row
	from   string // "via" for junction columns
	weight float64
}

// fieldChoice is how one field is filled.
type fieldChoice struct {
	f       *docschema.DocField
	col     *scopeCol
	score   float64
	steps   []mapping.Step
	status  string
	reasons []string
	conv    string
	// needs are lookups the choice reads besides its column (a derived value's table).
	needs []*Link
}

func (p *proposer) scopeColumns(relID string, table mapping.TableRef, via *mapping.Via) []scopeCol {
	var out []scopeCol
	tb := p.in.Table(table)
	if tb == nil {
		return nil
	}
	for i := range tb.Columns {
		c := &tb.Columns[i]
		out = append(out, scopeCol{name: c.Name, col: c, table: table, vs: p.sg.in.Dicts.sourceVariants(SourceCol{Name: c.Name, Table: table.Name, IsPK: c.IsPrimaryKey}), weight: 1})
	}
	if via != nil {
		if jt := p.in.Table(via.Table); jt != nil {
			for _, n := range via.Columns {
				if c := jt.Column(n); c != nil {
					out = append(out, scopeCol{name: n, col: c, table: via.Table, vs: p.sg.in.Dicts.sourceVariants(SourceCol{Name: n, Table: via.Table.Name}), from: mapping.FromVia, weight: 1})
				}
			}
		}
	}
	// Lookups: one-to-one tables first (their rows belong to this one), then many-to-one.
	back := p.backTable(relID)
	for _, l := range p.in.Links(table) {
		l := l
		if l.Kind != mapping.EmbedManyToOne || l.Via != nil || l.Table == table {
			continue
		}
		if p.onlyLookup != nil && !p.onlyLookup(l) {
			continue // a variant reads its own table only (OC-171)
		}
		if back != nil && l.Table == *back && l.Cardinality != mapping.CardinalityOneToOne {
			continue // the way back to the parent: its columns are $parent's
		}
		lt := p.in.Table(l.Table)
		if lt == nil {
			continue
		}
		w := 0.92
		if l.Cardinality == mapping.CardinalityOneToOne {
			w = 0.97
		}
		for i := range lt.Columns {
			c := &lt.Columns[i]
			if joinChild(l.Join, c.Name) {
				continue // the key it's looked up by
			}
			out = append(out, scopeCol{name: c.Name, col: c, table: l.Table, vs: p.sg.in.Dicts.sourceVariants(SourceCol{Name: c.Name, Table: l.Table.Name}), lookup: &l, weight: w})
		}
	}
	return out
}

func joinChild(j []mapping.JoinPair, col string) bool {
	for _, p := range j {
		if p.Child == col {
			return true
		}
	}
	return false
}

// backTable is the table of the relation's parent scope (the row that embedded this one).
func (p *proposer) backTable(relID string) *mapping.TableRef {
	if relID == "" {
		return nil
	}
	r := p.spec.RelationByID(relID)
	if r == nil {
		return nil
	}
	if pr := p.spec.RelationByID(r.Parent()); pr != nil {
		return &pr.Table
	}
	return &p.driving
}

var refTemplate = regexp.MustCompile(`^"([A-Za-z][A-Za-z0-9_]*)::[0-9a-fA-F-]{8,}"$`)

// fields proposes what fills the own fields of container f (and of the objects under it that
// only group its columns, proposed as fromParent later through scope's recursion).
func (p *proposer) fields(f *docschema.DocField, relID string, table mapping.TableRef, via *mapping.Via, parentNames []string) {
	leaves := ownLeaves(f)
	if len(leaves) == 0 {
		return
	}
	cols := p.scopeColumns(relID, table, via)
	tb := p.in.Table(table)
	parentName := ""
	if len(parentNames) > 0 {
		parentName = parentNames[len(parentNames)-1]
	}
	var free []*docschema.DocField
	chosen := map[string]*fieldChoice{}
	// "value/valueString mirror label - customizations has a single name column": label reads
	// customizations.name (and value, valueString copy it below).
	mirrorTarget, mirrorCol := "", ""
	for _, d := range []string{f.Description, itemsDescription(f)} {
		if c := mirroredColumn(d); c != "" {
			for _, of := range mirrorsOf(d) {
				mirrorTarget, mirrorCol = of, c
			}
		}
	}
	for _, l := range leaves {
		switch {
		case p.mapped[l.ID]:
			p.add(ProposalItem{FieldID: l.ID, Path: p.paths[l.ID], Kind: ItemField, Status: StatusAlreadyMapped, Band: BandHigh, Score: 1, Summary: "already mapped", Reasons: []string{"already mapped"}})
			continue
		case !p.allowed(l.ID):
			continue
		}
		h := docschema.ParseHints(l.Description)
		if h.NotPopulated {
			p.notPopulated(l, []string{"the design says it isn't populated"})
			continue
		}
		if v, ok := p.constantOf(l); ok {
			p.constant(l, v, 0.95, "the design fixes the value (const)")
			continue
		}
		if h.Always != "" {
			if v, ok := literal(h.Always, l.Type); ok {
				p.constant(l, v, 0.75, "the design says it's always "+h.Always)
				continue
			}
		}
		if h.Aggregate != "" && p.aggregate(l, h, relID, table) {
			continue
		}
		if l.Type == docschema.String && tb != nil && len(tb.PrimaryKey) == 1 {
			if m := refTemplate.FindStringSubmatch(string(l.Example)); m != nil {
				p.reference(l, m[1], tb.PrimaryKey[0])
				continue
			}
		}
		if c := p.combined(l, cols); c != nil {
			chosen[l.ID] = c
			continue
		}
		free = append(free, l)
	}

	// Score every free field against every column; assign best pairs first, each column once.
	type pair struct {
		f     *docschema.DocField
		c     int
		score float64
		ch    fieldChoice
	}
	var pairs []pair
	mismatch := map[string]string{}
	best3 := map[string][]scoredName{}
	for _, l := range free {
		tvs := p.sg.leaf(l.Name, parentName)
		h := docschema.ParseHints(l.Description)
		if l.Name == mirrorTarget {
			h.Sources = append(h.Sources, mirrorCol)
		}
		for i := range cols {
			c := &cols[i]
			ns, sig := p.sg.in.Dicts.nameScore(c.vs, tvs)
			hinted := hintNames(h.Sources, c)
			if hinted {
				ns = math.Max(ns, 0.97)
			}
			if ns == 0 {
				continue
			}
			ts, steps, conv, ok := convert(c.col, l)
			if !ok {
				if ns >= fieldMatch && mismatch[l.ID] == "" {
					mismatch[l.ID] = fmt.Sprintf("%s.%s is %s: no safe conversion to %s", c.table.Name, c.name, c.col.UDTName, l.Type)
				}
				continue
			}
			score := ns * (0.8 + 0.2*ts) * c.weight
			if hinted {
				score = math.Max(score, 0.95)
			}
			score = math.Round(score*1e4) / 1e4
			best3[l.ID] = append(best3[l.ID], scoredName{c.table.Name + "." + c.name, score})
			if score < fieldMatch {
				continue
			}
			reasons := []string{fmt.Sprintf("%s ~ %s.%s (%d%%)", l.Name, c.table.Name, c.name, int(math.Round(score*100)))}
			if sig.Name != "" {
				reasons[0] += ": " + strings.ToLower(strings.ReplaceAll(sig.Name, "_", " "))
			}
			if hinted {
				reasons = append(reasons, "the design names "+c.table.Name+"."+c.name)
			}
			if c.lookup != nil {
				reasons = append(reasons, "looked up in "+c.table.Name+" ("+map[bool]string{true: "1:1", false: "N:1"}[c.lookup.Cardinality == mapping.CardinalityOneToOne]+")")
			}
			if conv != "" {
				reasons = append(reasons, conv)
			}
			st := StatusMapped
			switch {
			case c.lookup != nil:
				st = StatusLookup
			case len(steps) > 0:
				st = StatusConverted
			}
			pairs = append(pairs, pair{f: l, c: i, score: score, ch: fieldChoice{f: l, col: c, score: score, steps: steps, status: st, reasons: reasons, conv: conv}})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		if pairs[i].f.Required != pairs[j].f.Required {
			return pairs[i].f.Required
		}
		return pairs[i].c < pairs[j].c
	})
	usedCol := map[int]bool{}
	for _, pr := range pairs {
		if chosen[pr.f.ID] != nil || usedCol[pr.c] {
			continue
		}
		ch := pr.ch
		chosen[pr.f.ID], usedCol[pr.c] = &ch, true
	}
	// One column, several fields (OC-167): the design says some fields mirror another, or a
	// field's best column is taken and still fits well.
	mirrors := mirrorsOf(f.Description)
	for k, v := range mirrorsOf(itemsDescription(f)) {
		mirrors[k] = v
	}
	byName := map[string]*docschema.DocField{}
	for _, l := range leaves {
		byName[l.Name] = l
	}
	for _, l := range free {
		if chosen[l.ID] != nil {
			continue
		}
		if of := mirrors[l.Name]; of != "" && byName[of] != nil && chosen[byName[of].ID] != nil {
			ch := *chosen[byName[of].ID]
			ch.f, ch.score = l, math.Min(ch.score, 0.8)
			ch.reasons = []string{"the design says " + l.Name + " mirrors " + of, "same column as " + of}
			chosen[l.ID] = &ch
			continue
		}
		for _, pr := range pairs {
			if pr.f == l && pr.score >= 0.8 {
				ch := pr.ch
				ch.reasons = append(ch.reasons, "shares the column with another field")
				chosen[l.ID] = &ch
				break
			}
		}
	}
	// A design value the column can't hold, from a one-to-one table (OC-165): "saved" when a
	// saved_orders row exists, else the column.
	for _, l := range leaves {
		ch := chosen[l.ID]
		if ch == nil || ch.col == nil || len(l.Enum) == 0 || len(ch.col.col.EnumValues) == 0 || ch.col.lookup != nil {
			continue
		}
		if d := p.derived(l, ch, cols); d != nil {
			chosen[l.ID] = d
		}
	}

	lookups := map[string]string{} // lookup table → relation id
	for _, l := range leaves {
		ch := chosen[l.ID]
		if ch == nil {
			if _, done := p.itemOf[l.ID]; done || p.mapped[l.ID] || !p.allowed(l.ID) {
				continue
			}
			it := ProposalItem{FieldID: l.ID, Path: p.paths[l.ID], Kind: ItemField, Status: StatusUnresolved, Band: BandLow, Code: CodeNoColumn, Required: l.Required, Summary: "unresolved"}
			if mismatch[l.ID] != "" {
				it.Code, it.Reasons = CodeTypeMismatch, []string{mismatch[l.ID]}
			} else {
				it.Reasons = []string{"no column in " + table.Name + " or the tables next to it fits " + l.Name}
			}
			it.Candidates = top3(best3[l.ID])
			if len(it.Candidates) == 0 {
				// Nothing alike by name: the row's unused columns of the same type are the likely ones.
				for i := range cols {
					c := &cols[i]
					if c.lookup != nil || usedCol[i] {
						continue
					}
					if ts, _, _, ok := convert(c.col, l); ok && ts == 1 {
						it.Candidates = append(it.Candidates, c.table.Name+"."+c.name)
					}
					if len(it.Candidates) == 3 {
						break
					}
				}
			}
			p.add(it)
			continue
		}
		p.apply(l, ch, relID, lookups)
	}
}

type scoredName struct {
	name  string
	score float64
}

func top3(xs []scoredName) []string {
	sort.SliceStable(xs, func(i, j int) bool { return xs[i].score > xs[j].score })
	var out []string
	seen := map[string]bool{}
	for _, x := range xs {
		if !seen[x.name] {
			seen[x.name] = true
			out = append(out, x.name)
		}
		if len(out) == 3 {
			break
		}
	}
	return out
}

// hintNames reports whether the description names this column (table.column).
func hintNames(sources []string, c *scopeCol) bool {
	ref := strings.ToLower(c.table.Name + "." + c.name)
	for _, s := range sources {
		if s == ref {
			return true
		}
	}
	return false
}

var (
	mirrorRe    = regexp.MustCompile(`([A-Za-z0-9_]+(?:/[A-Za-z0-9_]+)*)\s+(?:mirror|mirrors|are the same as|is the same as)\s+([A-Za-z0-9_]+)`)
	singleColRe = regexp.MustCompile(`([A-Za-z0-9_]+) has (?:a single|one|only one|only a) ([A-Za-z0-9_]+) column`)
)

// mirrorsOf reads "value/valueString mirror label" from a container's description.
func mirrorsOf(desc string) map[string]string {
	out := map[string]string{}
	for _, m := range mirrorRe.FindAllStringSubmatch(desc, -1) {
		for _, n := range strings.Split(m[1], "/") {
			out[n] = m[2]
		}
	}
	return out
}

// mirroredColumn reads "customizations has a single name column" next to a mirror: the
// mirrored field and its copies come from that column (table.column, lower case).
func mirroredColumn(desc string) string {
	if !mirrorRe.MatchString(desc) {
		return ""
	}
	if m := singleColRe.FindStringSubmatch(desc); m != nil {
		return strings.ToLower(m[1] + "." + m[2])
	}
	return ""
}

func itemsDescription(f *docschema.DocField) string {
	if f.Items != nil {
		return f.Items.Description
	}
	return ""
}

// apply writes a choice into the spec and records its item.
func (p *proposer) apply(l *docschema.DocField, ch *fieldChoice, relID string, lookups map[string]string) {
	it := ProposalItem{FieldID: l.ID, Path: p.paths[l.ID], Kind: ItemField, Status: ch.status, Score: ch.score, Band: fieldBand(ch.score), Reasons: ch.reasons, Required: l.Required}
	m := mapping.FieldMapping{TargetFieldID: l.ID, Transforms: append([]mapping.Step{}, ch.steps...), Origin: mapping.OriginAuto, State: mapping.StateActive, Confidence: ch.score, Reason: "proposed", NeedsReview: it.Band != BandHigh}
	if ch.col != nil {
		src := mapping.SourceRef{Column: ch.col.name, From: ch.col.from}
		if relID != "" {
			src.Relation = relID
		}
		if ch.col.lookup != nil {
			lid := p.lookupRelation(ch.col.lookup, relID, lookups)
			src.Relation = lid
			it.RelationIDs = []string{lid}
		}
		m.Sources = []mapping.SourceRef{src}
		it.Summary = "← " + ch.col.table.Name + "." + ch.col.name + stepSummary(ch.steps)
		m.ID = mappingID(src.Relation+"."+src.Column, l.ID)
	} else {
		m.Sources = []mapping.SourceRef{}
		m.ID = mappingID("expr", l.ID)
		it.Summary = stepSummary(ch.steps)
	}
	for _, r := range ch.lookupsNeeded() {
		lid := p.lookupRelation(r, relID, lookups)
		it.RelationIDs = append(it.RelationIDs, lid)
	}
	p.spec.FieldMappings = append(p.spec.FieldMappings, m)
	it.MappingIDs = []string{m.ID}
	p.add(it)
}

func (ch *fieldChoice) lookupsNeeded() []*Link { return ch.needs }

// centsExpr turns an amount with cents into whole cents; nulls stay null.
const centsExpr = "value == nil ? nil : value * 100"

// convert is how a column's values become a field's (OC-167): the type fit (1 exact … 0 no
// safe conversion), the steps, and a reason when it converts.
func convert(c *schema.Column, l *docschema.DocField) (float64, []mapping.Step, string, bool) {
	if l.Type == docschema.Array {
		if c.IsArray {
			return 1, nil, "", true
		}
		return 0, nil, "", false
	}
	if c.IsArray {
		return 0, nil, "", false
	}
	scale := 0
	if c.NumericScale != nil {
		scale = *c.NumericScale
	}
	if c.JSONType == schema.TypeNumber && scale == 2 {
		switch l.Type {
		case docschema.Integer:
			// Dollars with cents into a whole number: cents, never a truncated amount.
			return 0.85, []mapping.Step{{Op: "expression", Args: map[string]any{"expr": centsExpr}}, {Op: "round", Args: map[string]any{"digits": 0}}},
				"× 100: " + c.UDTName + " amounts (2 decimals) → whole cents", true
		case docschema.String:
			return 0.85, []mapping.Step{{Op: "toString"}}, c.UDTName + " → text (\"3.95\")", true
		}
	}
	class, step := mapping.Compat(c.JSONType, l.Type)
	ts := mapping.TypeScore(class)
	if ts == 0 {
		return 0, nil, "", false
	}
	var steps []mapping.Step
	conv := ""
	if step != nil {
		steps = []mapping.Step{*step}
		conv = c.JSONType + " → " + string(l.Type) + " (" + step.Op + ")"
	}
	if class == mapping.ClassLossy {
		// Never a silent wrong conversion (OC-167 AC4): the match stays below high, and says why.
		conv = c.JSONType + " → " + string(l.Type) + ": may lose data"
		if step != nil {
			conv += " (" + step.Op + ")"
		}
	}
	return ts, steps, conv, true
}

func stepSummary(steps []mapping.Step) string {
	var parts []string
	for _, s := range steps {
		switch s.Op {
		case "expression":
			e, _ := s.Args["expr"].(string)
			if e == centsExpr {
				parts = append(parts, "× 100")
			} else {
				parts = append(parts, "= "+e)
			}
		case "round":
		case "constant":
			b, _ := json.Marshal(s.Args["value"])
			parts = append(parts, "= "+string(b))
		default:
			parts = append(parts, s.Op)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

// lookupRelation is the lookup-only relation for link l in the scope relID (created once).
func (p *proposer) lookupRelation(l *Link, relID string, lookups map[string]string) string {
	k := relID + "|" + l.Table.String() + "|" + fmt.Sprint(l.Join)
	if id := lookups[k]; id != "" {
		return id
	}
	for _, r := range p.spec.Relations {
		if r.LookupOnly && r.Parent() == relID && r.Table == l.Table && fmt.Sprint(r.Join) == fmt.Sprint(l.Join) {
			lookups[k] = r.ID
			return r.ID
		}
	}
	id := shortID("rel_lk_", relID, l.Table.String(), fmt.Sprint(l.Join))
	r := mapping.Relation{ID: id, Kind: mapping.EmbedManyToOne, LookupOnly: true, Table: l.Table, Join: l.Join}
	if relID != "" {
		pid := relID
		r.ParentID = &pid
	}
	p.spec.Relations = append(p.spec.Relations, r)
	lookups[k] = id
	return id
}

func (p *proposer) notPopulated(l *docschema.DocField, reasons []string) {
	if p.mapped[l.ID] {
		return
	}
	if reasons == nil {
		reasons = []string{"the design says it isn't populated"}
	}
	m := mapping.FieldMapping{ID: mappingID("notPopulated", l.ID), TargetFieldID: l.ID, Sources: []mapping.SourceRef{}, Transforms: []mapping.Step{}, Origin: mapping.OriginAuto, State: mapping.StateNotPopulated, Confidence: 0.95, Reason: "design: not populated"}
	p.spec.FieldMappings = append(p.spec.FieldMappings, m)
	p.add(ProposalItem{FieldID: l.ID, Path: p.paths[l.ID], Kind: ItemField, Status: StatusNotPopulated, Band: BandHigh, Score: 0.95, Summary: "not populated (design)", Reasons: reasons, MappingIDs: []string{m.ID}})
}

func (p *proposer) constant(l *docschema.DocField, v any, score float64, reason string) {
	m := mapping.FieldMapping{ID: mappingID("constant", l.ID), TargetFieldID: l.ID, Sources: []mapping.SourceRef{}, Transforms: []mapping.Step{{Op: "constant", Args: map[string]any{"value": v}}}, Origin: mapping.OriginAuto, State: mapping.StateActive, Confidence: score, Reason: "proposed", NeedsReview: score < DefaultAutoThreshold}
	p.spec.FieldMappings = append(p.spec.FieldMappings, m)
	b, _ := json.Marshal(v)
	p.add(ProposalItem{FieldID: l.ID, Path: p.paths[l.ID], Kind: ItemField, Status: StatusConstant, Band: fieldBand(score), Score: score, Summary: "= " + string(b), Reasons: []string{reason}, MappingIDs: []string{m.ID}, Required: l.Required})
}

// constantOf is the design's const for a field (OC-059 constant hints).
func (p *proposer) constantOf(l *docschema.DocField) (any, bool) {
	for _, h := range p.spec.DocSchema.ConstantHints {
		if h.FieldID == l.ID && h.Reason == "schema-const" {
			var v any
			d := json.NewDecoder(strings.NewReader(string(h.Value)))
			d.UseNumber()
			if d.Decode(&v) != nil {
				return nil, false
			}
			if n, ok := v.(json.Number); ok {
				if i, err := n.Int64(); err == nil {
					return i, true
				}
				f, _ := n.Float64()
				return f, true
			}
			return v, true
		}
	}
	return nil, false
}

// literal parses "1", "true", "cafe" as a value of the field's type.
func literal(s string, t docschema.FieldType) (any, bool) {
	switch t {
	case docschema.Integer:
		var i int64
		if _, err := fmt.Sscan(s, &i); err == nil {
			return i, true
		}
	case docschema.Number:
		var f float64
		if _, err := fmt.Sscan(s, &f); err == nil {
			return f, true
		}
	case docschema.Boolean:
		switch strings.ToLower(s) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	case docschema.String:
		return strings.Trim(s, `"'`), true
	}
	return nil, false
}

// aggregate fills a field the design says is a total over a child table (OC-167):
// "the sum of collected_payments.tip_in_cents".
func (p *proposer) aggregate(l *docschema.DocField, h docschema.Hints, relID string, table mapping.TableRef) bool {
	if !l.Type.Scalar() {
		return false
	}
	for _, src := range h.Sources {
		tname, col, _ := strings.Cut(src, ".")
		for _, lk := range p.in.Links(table) {
			if lk.Kind != mapping.EmbedOneToMany || !strings.EqualFold(lk.Table.Name, tname) {
				continue
			}
			ct := p.in.Table(lk.Table)
			if ct == nil || (ct.Column(col) == nil && h.Aggregate != "count") {
				continue
			}
			// Reuse a relation already reading those rows in this scope, else a summary-only one.
			var rid string
			for _, r := range p.spec.Relations {
				if r.Parent() == relID && r.Table == lk.Table && fmt.Sprint(r.Join) == fmt.Sprint(lk.Join) && r.Kind == mapping.EmbedOneToMany {
					rid = r.ID
				}
			}
			var rels []string
			if rid == "" {
				rid = shortID("rel_ag_", relID, lk.Table.String(), fmt.Sprint(lk.Join))
				r := mapping.Relation{ID: rid, Kind: mapping.EmbedOneToMany, Table: lk.Table, Join: lk.Join}
				if relID != "" {
					pid := relID
					r.ParentID = &pid
				}
				p.spec.Relations = append(p.spec.Relations, r)
				rels = []string{rid}
			}
			a := mapping.Aggregate{ID: shortID("agg_", l.ID), RelationID: rid, Fn: h.Aggregate, TargetFieldID: l.ID}
			if h.Aggregate != "count" {
				a.Column = col
			}
			p.spec.Aggregates = append(p.spec.Aggregates, a)
			p.add(ProposalItem{
				FieldID: l.ID, Path: p.paths[l.ID], Kind: ItemField, Status: StatusAggregate, Band: BandMedium, Score: 0.8,
				Summary: "= " + h.Aggregate + "(" + lk.Table.Name + "." + col + ")", Reasons: []string{"the design says it's the " + h.Aggregate + " of " + src},
				RelationIDs: rels, AggregateIDs: []string{a.ID}, Required: l.Required,
			})
			return true
		}
	}
	return false
}

// reference fills a field whose example is another document's key ("tax_data::<uuid>"):
// that prefix and this row's key (OC-169 REFERENCE_TEMPLATE). The document's own key prefix
// makes it the document's id.
func (p *proposer) reference(l *docschema.DocField, prefix, pk string) {
	expr := fmt.Sprintf("concat('%s::', row.%s)", prefix, pk)
	m := mapping.FieldMapping{
		ID: mappingID("expr", l.ID), TargetFieldID: l.ID, Sources: []mapping.SourceRef{}, Transforms: []mapping.Step{{Op: "expression", Args: map[string]any{"expr": expr}}},
		Origin: mapping.OriginAuto, State: mapping.StateActive, Confidence: 0.7, Reason: "proposed", NeedsReview: true,
	}
	reason := "the example looks like a " + prefix + " document key"
	score := 0.7
	if prefix == p.keyPfx {
		reason, score = "the example is this document's key ("+prefix+"::…)", 0.8
		m.Confidence = score
	}
	p.spec.FieldMappings = append(p.spec.FieldMappings, m)
	p.add(ProposalItem{
		FieldID: l.ID, Path: p.paths[l.ID], Kind: ItemField, Status: StatusComputed, Band: fieldBand(score), Score: score, Code: CodeReferenceTemplate,
		Summary: "= " + expr, Reasons: []string{reason}, Fix: expr, MappingIDs: []string{m.ID}, Required: l.Required,
	})
}

var nameWords = map[string]bool{"name": true, "fullname": true, "displayname": true}

// combined proposes first + last name for a "name" field when the scope has both (OC-167).
func (p *proposer) combined(l *docschema.DocField, cols []scopeCol) *fieldChoice {
	if l.Type != docschema.String || !nameWords[strings.ToLower(strings.ReplaceAll(l.Name, "_", ""))] {
		return nil
	}
	var first, last *scopeCol
	for i := range cols {
		c := &cols[i]
		if c.lookup != nil {
			continue
		}
		switch strings.ToLower(c.name) {
		case "first_name", "firstname", "given_name":
			first = c
		case "last_name", "lastname", "family_name", "surname":
			last = c
		}
		if strings.ToLower(c.name) == "name" || strings.ToLower(c.name) == "full_name" {
			return nil // a real name column wins
		}
	}
	if first == nil || last == nil {
		return nil
	}
	expr := fmt.Sprintf("concat(row.%s, ' ', row.%s)", first.name, last.name)
	return &fieldChoice{
		f: l, score: 0.7, steps: []mapping.Step{{Op: "expression", Args: map[string]any{"expr": expr}}}, status: StatusComputed,
		reasons: []string{"no " + l.Name + " column: " + first.name + " and " + last.name + " combined"},
	}
}

// derived turns an enum field whose design has a value the column hasn't into a choice on a
// one-to-one table (OC-165): status = saved_orders.cart_id != nil ? 'saved' : row.status.
func (p *proposer) derived(l *docschema.DocField, ch *fieldChoice, cols []scopeCol) *fieldChoice {
	have := map[string]bool{}
	for _, v := range ch.col.col.EnumValues {
		have[v] = true
	}
	for _, raw := range l.Enum {
		var v string
		if json.Unmarshal(raw, &v) != nil || have[v] {
			continue
		}
		for i := range cols {
			c := &cols[i]
			if c.lookup == nil || c.lookup.Cardinality != mapping.CardinalityOneToOne || !strings.Contains(strings.ToLower(c.table.Name), strings.ToLower(v)) {
				continue
			}
			key := c.lookup.Join[0].Child
			expr := fmt.Sprintf("%s.%s != nil ? '%s' : row.%s", c.table.Name, key, v, ch.col.name)
			d := &fieldChoice{
				f: l, score: 0.7, steps: []mapping.Step{{Op: "expression", Args: map[string]any{"expr": expr}}}, status: StatusComputed,
				reasons: []string{fmt.Sprintf("the design allows '%s', which %s.%s can't hold", v, ch.col.table.Name, ch.col.name), "'" + v + "' when a " + c.table.Name + " row exists"},
			}
			d.needs = []*Link{c.lookup}
			return d
		}
	}
	return nil
}
