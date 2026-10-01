package docschema

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// Conformance is how one generated document differs in shape from the imported design
// (OC-061): paths the design has that the document lacks (where their parent object is
// there), paths the design doesn't have, and paths whose JSON type differs. Values aren't
// compared.
type Conformance struct {
	Missing  []string `json:"missing"`
	Extra    []string `json:"extra"`
	TypeDiff []string `json:"typeDiff"`
}

// Empty reports a conforming document.
func (c Conformance) Empty() bool { return len(c.Missing)+len(c.Extra)+len(c.TypeDiff) == 0 }

// Conform compares a document with an imported design's fields.
func Conform(fields []ContractField, doc json.RawMessage) Conformance {
	out := Conformance{Missing: []string{}, Extra: []string{}, TypeDiff: []string{}}
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	v, err := decodeOrdered(dec)
	if err != nil {
		return out
	}
	seen := map[string]map[string]bool{} // path → JSON kinds seen (null excluded)
	objects := map[string]bool{"": true} // paths seen as objects ("" = the root)
	var walk func(v any, path string)
	walk = func(v any, path string) {
		k := kindOf(v)
		if path != "" {
			if seen[path] == nil {
				seen[path] = map[string]bool{}
			}
			if k != "null" {
				seen[path][k] = true
			}
		}
		switch x := v.(type) {
		case *ordered:
			objects[path] = true
			for _, key := range x.keys {
				p := key
				if path != "" {
					p = path + "." + key
				}
				walk(x.vals[key], p)
			}
		case []any:
			for _, it := range x {
				walk(it, path+"[]")
			}
		}
	}
	walk(v, "")
	contract := map[string]FieldType{}
	for _, f := range fields {
		contract[f.Path] = f.Type
		kinds, ok := seen[f.Path]
		if !ok {
			if !strings.HasSuffix(f.Path, "[]") && objects[parentPath(f.Path)] {
				out.Missing = append(out.Missing, f.Path)
			}
			continue
		}
		for k := range kinds {
			if !kindFits(k, f.Type) {
				out.TypeDiff = append(out.TypeDiff, f.Path)
				break
			}
		}
	}
	for p := range seen {
		if _, ok := contract[p]; !ok && !underExtra(p, contract) {
			out.Extra = append(out.Extra, p)
		}
	}
	sort.Strings(out.Extra)
	return out
}

// parentPath: "a.b" → "a", "a[].b" → "a[]", "b" → "".
func parentPath(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[:i]
	}
	return ""
}

// underExtra: a path inside an extra object or array is reported through that one only.
func underExtra(p string, contract map[string]FieldType) bool {
	for q := parentPath(strings.TrimSuffix(p, "[]")); q != ""; q = parentPath(strings.TrimSuffix(q, "[]")) {
		if _, ok := contract[q]; !ok {
			return true
		}
	}
	if strings.HasSuffix(p, "[]") {
		_, ok := contract[strings.TrimSuffix(p, "[]")]
		return !ok
	}
	return false
}

func kindOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		if strings.ContainsAny(x.String(), ".eE") {
			return "number"
		}
		return "integer"
	case string:
		return "string"
	case *ordered:
		return "object"
	case []any:
		return "array"
	}
	return "any"
}

func kindFits(kind string, t FieldType) bool {
	switch t {
	case Any:
		return true
	case Datetime:
		return kind == "string" || kind == "integer" // epoch_ms datetimes are numbers
	case Number:
		return kind == "number" || kind == "integer"
	}
	return kind == string(t)
}
