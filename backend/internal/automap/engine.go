package automap

import (
	"crypto/sha1" //nolint:gosec // deterministic mapping IDs, not security
	"encoding/hex"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// SourceCol is one source column (OC-017 inputs).
type SourceCol struct {
	Name      string
	JSONType  string
	Ordinal   int
	IsPK      bool
	Table     string
	EmbedPath string
}

// TargetLeaf is one mappable target field.
type TargetLeaf struct {
	FieldID     string
	Path        string
	Name        string
	Type        docschema.FieldType
	ParentNames []string
	Required    bool
}

// Thresholds.
const (
	DefaultAutoThreshold    = 0.85
	DefaultSuggestThreshold = 0.60
	structuralBonusValue    = 0.03
)

// Modes.
const (
	ModeFill  = "fill"
	ModeReset = "reset"
)

// Options tune a run.
type Options struct {
	AutoThreshold    float64
	SuggestThreshold float64
	Dicts            *Dicts
	Mode             string // fill (default) | reset
	IncludeManual    bool   // reset also clears manual mappings
}

// Explanation says why a mapping was proposed (OC-017 AC7).
type Explanation struct {
	Score     float64  `json:"score"`
	NameScore float64  `json:"nameScore"`
	TypeScore float64  `json:"typeScore"`
	TypeClass string   `json:"typeClass"`
	Signals   []Signal `json:"signals"`
	Reason    string   `json:"reason"`
}

// Result is the updated mappings with stats and explanations.
type Result struct {
	FieldMappings []mapping.FieldMapping `json:"fieldMappings"`
	Stats         mapping.Stats          `json:"stats"`
	Explain       map[string]Explanation `json:"explain"`
}

type candidate struct {
	src      int
	tgt      int
	score    float64
	explain  Explanation
	class    string
	transfer *mapping.Step
}

// Columns converts a snapshot table into source columns.
func Columns(t *schema.Table) []SourceCol {
	out := make([]SourceCol, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = SourceCol{Name: c.Name, JSONType: c.JSONType, Ordinal: c.Ordinal, IsPK: c.IsPrimaryKey, Table: t.Name}
	}
	return out
}

// Leaves converts a design's mappable fields.
func Leaves(s *docschema.Schema) []TargetLeaf {
	var out []TargetLeaf
	for _, l := range s.Leaves() {
		out = append(out, TargetLeaf{FieldID: l.FieldID, Path: l.Path, Name: l.Name, Type: l.Type, ParentNames: l.ParentNames, Required: l.Required})
	}
	return out
}

// mappingID is deterministic, so identical inputs yield identical output.
func mappingID(col, fieldID string) string {
	sum := sha1.Sum([]byte(col + "\x00" + fieldID)) //nolint:gosec // see import
	return "map_" + hex.EncodeToString(sum[:])[:10]
}

// Run auto-maps a spec against source columns. Fill mode keeps every existing decision;
// reset rebuilds auto and suggested mappings but keeps manual ones (unless IncludeManual) and
// never re-proposes rejected pairs. The spec is not modified.
func Run(spec *mapping.Spec, cols []SourceCol, opts Options) Result {
	if opts.AutoThreshold == 0 {
		opts.AutoThreshold = DefaultAutoThreshold
	}
	if opts.SuggestThreshold == 0 {
		opts.SuggestThreshold = DefaultSuggestThreshold
	}
	if opts.Dicts == nil {
		opts.Dicts = DefaultDicts()
	}
	d := opts.Dicts
	// Fields inside embeds are filled from the embedded tables (RunEmbed), never from the
	// table's own columns.
	inEmbed := mapping.RelationFields(spec)
	var leaves []TargetLeaf
	for _, l := range Leaves(&spec.DocSchema) {
		if inEmbed[l.FieldID] == "" {
			leaves = append(leaves, l)
		}
	}

	// Keep existing decisions; collect what's taken and what's blocklisted.
	kept := []mapping.FieldMapping{}
	blocked := map[[2]string]bool{}
	usedSrc, usedTgt := map[string]bool{}, map[string]bool{}
	for _, m := range spec.FieldMappings {
		if m.Computed != nil {
			// A computed field (OC-136) is the user's design: never replaced, even by a reset
			// that includes manual mappings, and never mapped onto (OC-139).
			kept = append(kept, m)
			usedTgt[m.TargetFieldID] = true
			continue
		}
		if mapping.UsesAnyRelation(m) {
			kept = append(kept, m) // the embed's own run owns it
			continue
		}
		if m.State == mapping.StateRejected {
			for _, s := range m.Sources {
				blocked[[2]string{s.Column, m.TargetFieldID}] = true
			}
			kept = append(kept, m)
			continue
		}
		if opts.Mode == ModeReset && (m.Origin != mapping.OriginManual || opts.IncludeManual) {
			continue
		}
		kept = append(kept, m)
		usedTgt[m.TargetFieldID] = true
		for _, s := range m.Sources {
			usedSrc[s.Column] = true
		}
	}

	// Score every free pair.
	svs := make([][]variant, len(cols))
	srcTokens := make([][]string, len(cols))
	for i, c := range cols {
		svs[i] = d.sourceVariants(c)
		srcTokens[i] = d.Tokens(c.Name)
	}
	tvs := make([][]variant, len(leaves))
	parentTokens := make([][]string, len(leaves))
	for j, l := range leaves {
		tvs[j] = d.targetVariants(l)
		for _, p := range l.ParentNames {
			parentTokens[j] = append(parentTokens[j], d.Tokens(p)...)
		}
	}
	// Type classes per (source type, target type), computed once.
	type pairKey struct {
		src string
		dst docschema.FieldType
	}
	type compat struct {
		class string
		step  *mapping.Step
	}
	compatCache := map[pairKey]compat{}
	lookup := func(src string, dst docschema.FieldType) compat {
		k := pairKey{src, dst}
		if c, ok := compatCache[k]; ok {
			return c
		}
		class, step := mapping.Compat(src, dst)
		compatCache[k] = compat{class, step}
		return compatCache[k]
	}
	score := func(i int) []candidate {
		var out []candidate
		c := cols[i]
		if usedSrc[c.Name] || spec.Ignored(c.Name) {
			return nil
		}
		for j, l := range leaves {
			if usedTgt[l.FieldID] || blocked[[2]string{c.Name, l.FieldID}] {
				continue
			}
			cc := lookup(c.JSONType, l.Type)
			class, step := cc.class, cc.step
			ts := mapping.TypeScore(class)
			if ts == 0 {
				continue
			}
			ns, sig := d.nameScore(svs[i], tvs[j])
			if ns == 0 {
				continue
			}
			score := ns * (0.8 + 0.2*ts)
			signals := []Signal{sig}
			if sig.Source == "prefix-stripped" {
				signals = append(signals, Signal{Name: SignalPrefixStripped, Score: -0.02})
			}
			if d.structuralBonus(srcTokens[i], parentTokens[j]) {
				score = math.Min(1, score+structuralBonusValue)
				signals = append(signals, Signal{Name: SignalStructural, Score: structuralBonusValue})
			}
			if score < opts.SuggestThreshold {
				continue
			}
			score = math.Round(score*1e4) / 1e4
			e := Explanation{Score: score, NameScore: math.Round(ns*1e4) / 1e4, TypeScore: ts, TypeClass: class, Signals: signals}
			e.Reason = reason(d, c, l, sig, class, score)
			var own *mapping.Step
			if step != nil {
				cp := *step // candidates must not share the cached step
				own = &cp
			}
			out = append(out, candidate{src: i, tgt: j, score: score, explain: e, class: class, transfer: own})
		}
		return out
	}
	// Score rows in parallel for large inputs; the sort below makes the result independent of
	// scheduling.
	for _, c := range cols {
		for _, l := range leaves {
			lookup(c.JSONType, l.Type) // fill the cache up front: workers only read it
		}
	}
	perRow := make([][]candidate, len(cols))
	workers := 1
	if len(cols)*len(leaves) > 20_000 {
		workers = min(runtime.GOMAXPROCS(0), 8)
	}
	var wg sync.WaitGroup
	next := make(chan int, len(cols))
	for i := range cols {
		next <- i
	}
	close(next)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				perRow[i] = score(i)
			}
		}()
	}
	wg.Wait()
	var cands []candidate
	for _, rc := range perRow {
		cands = append(cands, rc...)
	}

	// Stable order: score desc, required target first, source ordinal, target path.
	sort.SliceStable(cands, func(a, b int) bool {
		x, y := cands[a], cands[b]
		if x.score != y.score {
			return x.score > y.score
		}
		if leaves[x.tgt].Required != leaves[y.tgt].Required {
			return leaves[x.tgt].Required
		}
		if cols[x.src].Ordinal != cols[y.src].Ordinal {
			return cols[x.src].Ordinal < cols[y.src].Ordinal
		}
		if cols[x.src].Name != cols[y.src].Name {
			return cols[x.src].Name < cols[y.src].Name
		}
		return leaves[x.tgt].Path < leaves[y.tgt].Path
	})

	res := Result{Explain: map[string]Explanation{}}
	takenSrc, takenTgt := map[int]bool{}, map[int]bool{}
	var added []mapping.FieldMapping
	for _, c := range cands {
		if takenSrc[c.src] || takenTgt[c.tgt] {
			continue
		}
		takenSrc[c.src], takenTgt[c.tgt] = true, true
		col, leaf := cols[c.src], leaves[c.tgt]
		fm := mapping.FieldMapping{
			ID: mappingID(col.Name, leaf.FieldID), TargetFieldID: leaf.FieldID,
			Sources: []mapping.SourceRef{{Column: col.Name}}, Transforms: []mapping.Step{},
			Confidence: c.score, Reason: strings.ToLower(strings.ReplaceAll(c.explain.Signals[0].Name, "_", "-")),
		}
		if c.score >= opts.AutoThreshold {
			fm.Origin, fm.State = mapping.OriginAuto, mapping.StateActive
		} else {
			fm.Origin, fm.State = mapping.OriginSuggested, mapping.StatePending
		}
		if c.transfer != nil {
			fm.Transforms = append(fm.Transforms, *c.transfer)
			fm.NeedsReview = c.class == mapping.ClassLossy
		}
		added = append(added, fm)
		res.Explain[fm.ID] = c.explain
	}
	res.FieldMappings = append(append(make([]mapping.FieldMapping, 0, len(kept)+len(added)), kept...), added...)
	out := *spec
	out.FieldMappings = res.FieldMappings
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	res.Stats = mapping.ComputeStats(&out, names)
	return res
}

var signalText = map[string]string{
	SignalExact:           "Names are identical",
	SignalNormalizedEqual: "Names match ignoring case and separators",
	SignalExpandedEqual:   "Names match after normalizing",
	SignalSynonym:         "Names are synonyms",
	SignalTokenOverlap:    "Names share words",
	SignalFuzzy:           "Names are spelled alike",
}

var classText = map[string]string{
	mapping.ClassExact:    "types match",
	mapping.ClassLossless: "is lossless",
	mapping.ClassLossy:    "may lose data or fail to parse",
}

// reason is the human explanation, e.g. "Names match after expanding 'cust' → 'customer' (93%);
// integer → string is lossless".
func reason(d *Dicts, c SourceCol, l TargetLeaf, sig Signal, class string, score float64) string {
	text := signalText[sig.Name]
	switch sig.Name {
	case SignalExpandedEqual:
		if exp := append(d.expansions(c.Name), d.expansions(l.Name)...); len(exp) > 0 {
			text = "Names match after expanding " + strings.Join(exp, ", ")
		} else {
			text = "Names match after singularizing"
		}
	case SignalSynonym, SignalTokenOverlap:
		if sig.Details != "" {
			text += " (" + sig.Details + ")"
		}
	}
	if sig.Source == "prefix-stripped" {
		text += " after removing the table prefix"
	}
	if sig.Target == "path" || sig.Target == "path-without-root" {
		text += " via the path " + l.Path
	}
	typeText := fmt.Sprintf("%s → %s %s", c.JSONType, l.Type, classText[class])
	if class == mapping.ClassExact {
		typeText = "types match"
	}
	return fmt.Sprintf("%s (%d%%); %s", text, int(math.Round(score*100)), typeText)
}

// Explain recomputes the explanation of every single-source auto or suggested mapping (and
// accepted suggestions) so the UI can show why a pair was proposed after a reload.
func Explain(spec *mapping.Spec, cols []SourceCol, d *Dicts) map[string]Explanation {
	if d == nil {
		d = DefaultDicts()
	}
	byCol := map[string]SourceCol{}
	for _, c := range cols {
		byCol[c.Name] = c
	}
	byField := map[string]TargetLeaf{}
	for _, l := range Leaves(&spec.DocSchema) {
		byField[l.FieldID] = l
	}
	out := map[string]Explanation{}
	for _, m := range spec.FieldMappings {
		proposed := m.Origin == mapping.OriginAuto || m.Origin == mapping.OriginSuggested || m.Reason == "accepted-suggestion"
		if !proposed || len(m.Sources) != 1 || m.Sources[0].Relation != "" {
			continue
		}
		c, okc := byCol[m.Sources[0].Column]
		l, okl := byField[m.TargetFieldID]
		if !okc || !okl {
			continue
		}
		class, _ := mapping.Compat(c.JSONType, l.Type)
		ts := mapping.TypeScore(class)
		ns, sig := d.NameScore(c, l)
		score := ns * (0.8 + 0.2*ts)
		signals := []Signal{sig}
		var pt []string
		for _, p := range l.ParentNames {
			pt = append(pt, d.Tokens(p)...)
		}
		if d.structuralBonus(d.Tokens(c.Name), pt) {
			score = math.Min(1, score+structuralBonusValue)
			signals = append(signals, Signal{Name: SignalStructural, Score: structuralBonusValue})
		}
		score = math.Round(score*1e4) / 1e4
		out[m.ID] = Explanation{
			Score: score, NameScore: math.Round(ns*1e4) / 1e4, TypeScore: ts, TypeClass: class, Signals: signals,
			Reason: reason(d, c, l, sig, class, score),
		}
	}
	return out
}
