package docschema

import (
	"strings"
	"unicode"
)

// Hints are what a design's description says about where a field's value comes from
// (OC-166). They're parsed on demand from the description, so designs imported before 2.5
// get them too.
type Hints struct {
	// NotPopulated: the customer leaves the field empty on purpose ("Not populated", "not
	// tracked", "no column", "always empty").
	NotPopulated bool `json:"notPopulated,omitempty"`
	// Sources are the table.column tokens the description names ("sourced from
	// saved_orders.saved_at"). Whether they exist is up to the caller (the source snapshot).
	Sources []string `json:"sources,omitempty"`
	// Aggregate is a function the description says the value is ("the sum of …", "number of
	// …"): sum | count | min | max | avg.
	Aggregate string `json:"aggregate,omitempty"`
	// Always is a literal the value always is ("Always 1").
	Always string `json:"always,omitempty"`
	// KeyTemplate is the document ID pattern on the root ("Document ID: cart::{uuid}").
	KeyTemplate string `json:"keyTemplate,omitempty"`
}

// maxHintDescription bounds the text the parser reads.
const maxHintDescription = 2048

var notPopulatedPhrases = []string{"not populated", "not tracked", "no column", "always empty", "never populated", "never persisted", "never stored"}

var aggregateWords = []struct{ word, fn string }{
	{"sum of", "sum"},
	{"total of", "sum"},
	{"number of", "count"},
	{"count of", "count"},
	{"minimum of", "min"},
	{"maximum of", "max"},
	{"average of", "avg"},
}

// ParseHints reads a description. It's a plain scan over sentences: no backtracking regular
// expressions over the whole text.
func ParseHints(description string) Hints {
	var h Hints
	d := description
	if len(d) > maxHintDescription {
		d = d[:maxHintDescription]
	}
	lower := strings.ToLower(d)
	for _, sentence := range sentences(lower) {
		s := strings.TrimSpace(sentence)
		for _, p := range notPopulatedPhrases {
			// At the start of a sentence (possibly after "-"), not inside "is populated only if…".
			if strings.HasPrefix(s, p) || strings.Contains(s, " - "+p) || strings.Contains(s, "— "+p) {
				h.NotPopulated = true
			}
		}
		for _, a := range aggregateWords {
			if strings.Contains(s, a.word) && h.Aggregate == "" {
				h.Aggregate = a.fn
			}
		}
		if rest, ok := strings.CutPrefix(s, "always "); ok && h.Always == "" && !strings.HasPrefix(rest, "empty") {
			h.Always = firstWord(rest)
		}
	}
	h.Sources = columnRefs(d)
	if i := strings.Index(lower, "document id:"); i >= 0 {
		h.KeyTemplate = firstWord(strings.TrimSpace(d[i+len("document id:"):]))
		h.KeyTemplate = strings.TrimRight(h.KeyTemplate, ".,;")
	}
	return h
}

// sentences splits on ". " and newlines, keeping "table.column" tokens whole.
func sentences(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		end := s[i] == '\n' || (s[i] == '.' && (i+1 == len(s) || s[i+1] == ' '))
		if end {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func firstWord(s string) string {
	if i := strings.IndexFunc(s, unicode.IsSpace); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, ".,;)")
}

// columnRefs are the identifier.identifier tokens of s ("saved_orders.saved_at"), lower case,
// once each, skipping file names (x.json) and dotted paths with brackets (payments[].tip).
func columnRefs(s string) []string {
	var out []string
	seen := map[string]bool{}
	isIdent := func(r byte) bool {
		return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	for i := 0; i < len(s); {
		if !isIdent(s[i]) || (i > 0 && (isIdent(s[i-1]) || s[i-1] == '.' || s[i-1] == '/' || s[i-1] == ']' || s[i-1] == '[')) {
			i++
			continue
		}
		j := i
		for j < len(s) && isIdent(s[j]) {
			j++
		}
		if j+1 < len(s) && s[j] == '.' && isIdent(s[j+1]) {
			k := j + 1
			for k < len(s) && isIdent(s[k]) {
				k++
			}
			table, col := s[i:j], s[j+1:k]
			next := byte(' ')
			if k < len(s) {
				next = s[k]
			}
			ext := strings.ToLower(col)
			dotted := next == '.' && k+1 < len(s) && isIdent(s[k+1]) // a.b.c is a path, not a column
			if !dotted && next != '[' && next != '/' && ext != "json" && ext != "go" && !isDigits(table) {
				ref := strings.ToLower(table + "." + col)
				if !seen[ref] {
					seen[ref] = true
					out = append(out, ref)
				}
			}
			i = k
			continue
		}
		i = j
	}
	return out
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
