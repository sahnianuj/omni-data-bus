package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// Canonical re-encodes JSON with sorted keys and normalized numbers (1.0 = 1, 12.50 = 12.5).
func Canonical(raw []byte) ([]byte, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	writeCanonical(&b, v)
	return b.Bytes(), nil
}

func decode(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// normNumber renders a number exactly, without trailing zeros or exponent differences.
func normNumber(n json.Number) string {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return string(n)
	}
	if r.IsInt() {
		return r.Num().String()
	}
	return strings.TrimRight(strings.TrimRight(r.FloatString(40), "0"), ".")
}

func writeCanonical(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteByte(':')
			writeCanonical(b, x[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCanonical(b, e)
		}
		b.WriteByte(']')
	case json.Number:
		b.WriteString(normNumber(x))
	default:
		e, _ := json.Marshal(x)
		b.Write(e)
	}
}

// Diff is one difference between the expected and the actual document.
type Diff struct {
	Path     string `json:"path"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
}

// Compare returns the differing paths of two JSON documents ($.a.b[2]), comparing canonically.
func Compare(expected, actual []byte) ([]Diff, error) {
	e, err := decode(expected)
	if err != nil {
		return nil, fmt.Errorf("expected document: %w", err)
	}
	a, err := decode(actual)
	if err != nil {
		return nil, fmt.Errorf("actual document: %w", err)
	}
	var out []Diff
	diff("$", e, a, &out)
	return out, nil
}

const missingValue = "(missing)"

func diff(path string, e, a any, out *[]Diff) {
	switch ev := e.(type) {
	case map[string]any:
		av, ok := a.(map[string]any)
		if !ok {
			*out = append(*out, Diff{path, plain(e), plain(a)})
			return
		}
		keys := map[string]bool{}
		for k := range ev {
			keys[k] = true
		}
		for k := range av {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			ec, eok := ev[k]
			ac, aok := av[k]
			p := path + "." + k
			switch {
			case !eok:
				*out = append(*out, Diff{p, missingValue, plain(ac)})
			case !aok:
				*out = append(*out, Diff{p, plain(ec), missingValue})
			default:
				diff(p, ec, ac, out)
			}
		}
	case []any:
		av, ok := a.([]any)
		if !ok || len(av) != len(ev) {
			*out = append(*out, Diff{path, plain(e), plain(a)})
			return
		}
		for i := range ev {
			diff(path+"["+strconv.Itoa(i)+"]", ev[i], av[i], out)
		}
	case json.Number:
		an, ok := a.(json.Number)
		if !ok || normNumber(ev) != normNumber(an) {
			*out = append(*out, Diff{path, plain(e), plain(a)})
		}
	default:
		if !equalScalar(e, a) {
			*out = append(*out, Diff{path, plain(e), plain(a)})
		}
	}
}

func equalScalar(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

// plain makes decoded values JSON-friendly for the report (numbers as written).
func plain(v any) any {
	if n, ok := v.(json.Number); ok {
		return json.RawMessage(n)
	}
	return v
}
