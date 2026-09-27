// Package automap matches source columns to target document fields with explainable,
// deterministic scores (OC-017). There is no ML: names are normalized (split, abbreviations
// expanded, singularized), compared through several signals, weighted by type compatibility
// and assigned greedily one-to-one.
package automap

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"omnidatabus/internal/naming"
)

//go:embed dict/*.yaml
var dictFS embed.FS

// Dicts are the abbreviation and synonym dictionaries.
type Dicts struct {
	Abbreviations map[string][]string // token → expansion tokens
	synonymGroup  map[string]int      // normalized phrase → group
	Groups        [][]string          // normalized phrases per group
}

var (
	defaultDicts     *Dicts
	defaultDictsErr  error
	defaultDictsOnce sync.Once
)

// DefaultDicts loads the embedded dictionaries once.
func DefaultDicts() *Dicts {
	defaultDictsOnce.Do(func() { defaultDicts, defaultDictsErr = loadDicts() })
	if defaultDictsErr != nil {
		panic("automap: invalid embedded dictionaries: " + defaultDictsErr.Error())
	}
	return defaultDicts
}

func loadDicts() (*Dicts, error) {
	d := &Dicts{Abbreviations: map[string][]string{}, synonymGroup: map[string]int{}}
	raw, err := dictFS.ReadFile("dict/abbreviations.yaml")
	if err != nil {
		return nil, err
	}
	var abbr map[string]string
	if err := yaml.Unmarshal(raw, &abbr); err != nil {
		return nil, fmt.Errorf("abbreviations: %w", err)
	}
	for k, v := range abbr {
		d.Abbreviations[strings.ToLower(k)] = strings.Fields(strings.ToLower(v))
	}
	raw, err = dictFS.ReadFile("dict/synonyms.yaml")
	if err != nil {
		return nil, err
	}
	var groups [][]string
	if err := yaml.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("synonyms: %w", err)
	}
	for gi, g := range groups {
		var norm []string
		for _, phrase := range g {
			p := strings.Join(d.expand(naming.Lower(phrase)), " ")
			if prev, dup := d.synonymGroup[p]; dup && prev != gi {
				return nil, fmt.Errorf("synonym phrase %q is in groups %d and %d", p, prev, gi)
			}
			d.synonymGroup[p] = gi
			norm = append(norm, p)
		}
		d.Groups = append(d.Groups, norm)
	}
	return d, nil
}

var noise = map[string]bool{"tbl": true, "col": true}

// expand applies abbreviations, singularizes and drops noise tokens.
func (d *Dicts) expand(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if noise[w] {
			continue
		}
		if exp, ok := d.Abbreviations[w]; ok {
			out = append(out, exp...)
			continue
		}
		out = append(out, naming.Singular(w))
	}
	return out
}

// Tokens normalizes a name (OC-017 step 1): split on separators and case/digit boundaries,
// lowercase, expand abbreviations, singularize and drop noise tokens.
func (d *Dicts) Tokens(name string) []string { return d.expand(naming.Lower(name)) }

// expansions lists the abbreviation expansions that fired, for explanations.
func (d *Dicts) expansions(name string) []string {
	var out []string
	for _, w := range naming.Lower(name) {
		if exp, ok := d.Abbreviations[w]; ok {
			out = append(out, fmt.Sprintf("'%s' → '%s'", w, strings.Join(exp, " ")))
		}
	}
	return out
}

// Synonyms reports whether two normalized phrases are in the same synonym group.
func (d *Dicts) Synonyms(a, b string) bool {
	ga, ok1 := d.synonymGroup[a]
	gb, ok2 := d.synonymGroup[b]
	return ok1 && ok2 && ga == gb
}

// variant is one precomputed spelling of a name.
type variant struct {
	kind     string    // raw | tokens | prefix-stripped | pk-alias | leaf | path | path-without-root
	raw      string    // the original text (raw kind only)
	basic    string    // split + lowercase, space-joined
	expanded string    // normalized tokens, space-joined
	joined   string    // normalized tokens without spaces (fuzzy)
	set      []string  // sorted unique normalized tokens (overlap)
	stripped bool      // came from the table-prefix-stripped variant
	syn      int       // synonym group of expanded, or -1
	hist     [37]uint8 // character counts of joined (a–z, 0–9, other), for the fuzzy bound
}

func (d *Dicts) makeVariant(kind string, words []string, raw string, stripped bool) variant {
	exp := d.expand(words)
	set := append([]string(nil), exp...)
	sort.Strings(set)
	set = dedupe(set)
	expanded := strings.Join(exp, " ")
	syn, ok := d.synonymGroup[expanded]
	if !ok {
		syn = -1
	}
	v := variant{
		kind: kind, raw: raw, basic: strings.Join(words, " "), expanded: expanded,
		joined: strings.Join(exp, ""), set: set, stripped: stripped, syn: syn,
	}
	for i := 0; i < len(v.joined); i++ {
		c := v.joined[i]
		switch {
		case c >= 'a' && c <= 'z':
			v.hist[c-'a']++
		case c >= '0' && c <= '9':
			v.hist[26+c-'0']++
		default:
			v.hist[36]++
		}
	}
	return v
}

func dedupe(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// sourceVariants: the raw name, its tokens, the tokens with the table name prefix stripped
// (cust_name on customer → name), and for the PK, the alias [id].
func (d *Dicts) sourceVariants(col SourceCol) []variant {
	words := naming.Lower(col.Name)
	vs := []variant{d.makeVariant("tokens", words, col.Name, false)}
	vs[0].kind = "raw"
	table := d.Tokens(col.Table)
	exp := d.expand(words)
	if len(table) > 0 && len(exp) > len(table) {
		match := true
		for i := range table {
			if exp[i] != table[i] {
				match = false
				break
			}
		}
		if match {
			// Strip as many raw words as it takes to cover the table tokens.
			n := 0
			for k := 1; k <= len(words); k++ {
				if len(d.expand(words[:k])) >= len(table) {
					n = k
					break
				}
			}
			if n > 0 && n < len(words) {
				vs = append(vs, d.makeVariant("prefix-stripped", words[n:], "", true))
			}
		}
	}
	if col.IsPK {
		vs = append(vs, d.makeVariant("pk-alias", []string{"id"}, "", false))
	}
	return vs
}

// targetVariants: the leaf name, the full path, and the path without its root-level object.
func (d *Dicts) targetVariants(l TargetLeaf) []variant {
	leaf := naming.Lower(l.Name)
	vs := []variant{d.makeVariant("leaf", leaf, l.Name, false)}
	if len(l.ParentNames) > 0 {
		var path []string
		for _, p := range l.ParentNames {
			path = append(path, naming.Lower(p)...)
		}
		vs = append(vs, d.makeVariant("path", append(path, leaf...), "", false))
		if len(l.ParentNames) > 1 {
			var rest []string
			for _, p := range l.ParentNames[1:] {
				rest = append(rest, naming.Lower(p)...)
			}
			vs = append(vs, d.makeVariant("path-without-root", append(rest, leaf...), "", false))
		}
	}
	return vs
}
