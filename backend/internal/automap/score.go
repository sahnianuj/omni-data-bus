package automap

import (
	"strings"
)

// Signals (OC-017 step 2).
const (
	SignalExact           = "EXACT"
	SignalNormalizedEqual = "NORMALIZED_EQUAL"
	SignalExpandedEqual   = "EXPANDED_EQUAL"
	SignalSynonym         = "SYNONYM"
	SignalTokenOverlap    = "TOKEN_OVERLAP"
	SignalFuzzy           = "FUZZY"
	SignalPrefixStripped  = "PREFIX_STRIPPED"
	SignalStructural      = "STRUCTURAL_BONUS"
)

// Signal is one reason a pair scored.
type Signal struct {
	Name    string  `json:"name"`
	Score   float64 `json:"score"`
	Source  string  `json:"sourceVariant,omitempty"`
	Target  string  `json:"targetVariant,omitempty"`
	Details string  `json:"details,omitempty"`
}

// nameMatch is the best signal over all variant pairs.
type nameMatch struct {
	score  float64
	signal Signal
}

// compare scores one pair of variants; the result is the strongest signal that fired.
func (d *Dicts) compare(s, t variant) nameMatch {
	best := nameMatch{}
	try := func(name string, score float64, details string) {
		if s.stripped {
			score -= 0.02
		}
		if score > best.score {
			best = nameMatch{score: score, signal: Signal{Name: name, Score: score, Source: s.kind, Target: t.kind, Details: details}}
		}
	}
	switch {
	case s.raw != "" && t.raw != "" && s.raw == t.raw:
		try(SignalExact, 1.0, "")
		return best
	case s.basic == t.basic:
		try(SignalNormalizedEqual, 0.97, "")
		return best
	case s.expanded == t.expanded && s.expanded != "":
		try(SignalExpandedEqual, 0.93, "")
		return best
	case s.syn >= 0 && s.syn == t.syn:
		try(SignalSynonym, 0.88, "'"+s.expanded+"' ~ '"+t.expanded+"'")
		return best
	}
	if j := jaccard(s.set, t.set); j >= 0.5 {
		try(SignalTokenOverlap, 0.55+0.35*j, "shared words: "+strings.Join(intersect(s.set, t.set), ", "))
	}
	// Fuzzy (typos) can reach at most 0.85: only compute it when it could win, and only for
	// spellings that are close in length and share the first letter.
	if best.score < 0.85 && fuzzyCandidate(s.joined, t.joined) && jaroBound(&s, &t) >= 0.83 {
		if jw := jaroWinkler(s.joined, t.joined); jw >= 0.90 {
			try(SignalFuzzy, 0.85*jw, "")
		}
	}
	return best
}

func fuzzyCandidate(a, b string) bool {
	if a == "" || b == "" || a[0] != b[0] {
		return false
	}
	la, lb := len(a), len(b)
	if la > lb {
		la, lb = lb, la
	}
	return float64(la)/float64(lb) >= 0.75
}

// jaroBound is an upper bound of the Jaro similarity from shared character counts: a
// Jaro-Winkler of 0.90 needs a Jaro of at least 0.833, so pairs below it skip the full metric.
func jaroBound(s, t *variant) float64 {
	common := 0
	for i := range s.hist {
		common += int(min(s.hist[i], t.hist[i]))
	}
	m := float64(common)
	return (m/float64(len(s.joined)) + m/float64(len(t.joined)) + 1) / 3
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			inter++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// intersect of two sorted, deduplicated slices.
func intersect(a, b []string) []string {
	var out []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// jaroWinkler is the Jaro-Winkler similarity (prefix scale 0.1, max prefix 4).
func jaroWinkler(a, b string) float64 {
	if a == b {
		return 1
	}
	la, lb := len(a), len(b)
	if la == 0 || lb == 0 {
		return 0
	}
	window := max(la, lb)/2 - 1
	if window < 0 {
		window = 0
	}
	var am, bm [64]bool
	if la > 64 || lb > 64 {
		return 0 // identifiers this long aren't typos of each other
	}
	matches := 0
	for i := 0; i < la; i++ {
		lo, hi := max(0, i-window), min(lb-1, i+window)
		for j := lo; j <= hi; j++ {
			if !bm[j] && a[i] == b[j] {
				am[i], bm[j] = true, true
				matches++
				break
			}
		}
	}
	if matches == 0 {
		return 0
	}
	trans, k := 0, 0
	for i := 0; i < la; i++ {
		if !am[i] {
			continue
		}
		for !bm[k] {
			k++
		}
		if a[i] != b[k] {
			trans++
		}
		k++
	}
	m := float64(matches)
	jaro := (m/float64(la) + m/float64(lb) + (m-float64(trans)/2)/m) / 3
	prefix := 0
	for prefix < 4 && prefix < la && prefix < lb && a[prefix] == b[prefix] {
		prefix++
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}

// NameScore is the maximum signal over all source × target variant pairs.
func (d *Dicts) NameScore(col SourceCol, leaf TargetLeaf) (float64, Signal) {
	return d.nameScore(d.sourceVariants(col), d.targetVariants(leaf))
}

func (d *Dicts) nameScore(svs, tvs []variant) (float64, Signal) {
	best := nameMatch{}
	for _, s := range svs {
		for _, t := range tvs {
			if m := d.compare(s, t); m.score > best.score {
				best = m
			}
		}
	}
	return best.score, best.signal
}

// structuralBonus: +0.03 when the target's parent object names prefix the source tokens
// (address.city prefers address_city over billing_city).
func (d *Dicts) structuralBonus(srcTokens []string, parentTokens []string) bool {
	if len(parentTokens) == 0 || len(srcTokens) <= len(parentTokens) {
		return false
	}
	for i := range parentTokens {
		if srcTokens[i] != parentTokens[i] {
			return false
		}
	}
	return true
}
