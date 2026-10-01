package docschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"omnidatabus/internal/problem"
)

// Sample limits (OC-015 AC2).
const (
	MaxSampleBytes = 5 << 20
	MaxSampleDocs  = 1000
	// MaxJSONDepth bounds nesting in user-supplied JSON (OC-033): deeper documents are rejected
	// before any recursive processing.
	MaxJSONDepth = 64
)

// CodeInvalidSample is returned for unparsable or oversized samples.
const CodeInvalidSample = problem.CodeInvalidSample

func sampleError(detail string) problem.Problem {
	return problem.New(http.StatusUnprocessableEntity, CodeInvalidSample, "Invalid JSON sample", detail)
}

// position turns a byte offset into a 1-based line and column.
func position(data []byte, offset int64) (line, col int) {
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	before := data[:offset]
	line = bytes.Count(before, []byte("\n")) + 1
	col = int(offset) - bytes.LastIndexByte(before, '\n')
	return line, col
}

// ParseSamples accepts one object, an array of objects, or NDJSON (≤ 5 MB, ≤ 1,000 documents).
// Parse errors carry the line and column.
func ParseSamples(data []byte) ([]json.RawMessage, error) {
	if len(data) > MaxSampleBytes {
		return nil, sampleError(fmt.Sprintf("Samples are limited to 5 MB (this one is %.1f MB).", float64(len(data))/(1<<20)))
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, sampleError("The sample is empty.")
	}
	limit := MaxJSONDepth
	if trimmed[0] == '[' {
		limit++ // the array holding the samples adds a level
	}
	if tooDeep(data, limit) {
		return nil, sampleError(fmt.Sprintf("Samples can nest at most %d levels deep.", MaxJSONDepth))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var docs []json.RawMessage
	if trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := dec.Decode(&arr); err != nil {
			return nil, syntaxErr(data, dec, err)
		}
		docs = arr
	} else {
		for {
			var raw json.RawMessage
			err := dec.Decode(&raw)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, syntaxErr(data, dec, err)
			}
			docs = append(docs, raw)
		}
	}
	if len(docs) > MaxSampleDocs {
		return nil, sampleError(fmt.Sprintf("Samples are limited to %d documents (this one has %d).", MaxSampleDocs, len(docs)))
	}
	for i, d := range docs {
		if t := bytes.TrimSpace(d); len(t) == 0 || t[0] != '{' {
			return nil, sampleError(fmt.Sprintf("Document %d isn't a JSON object.", i+1))
		}
	}
	return docs, nil
}

func syntaxErr(data []byte, dec *json.Decoder, err error) error {
	var se *json.SyntaxError
	offset := dec.InputOffset()
	if errors.As(err, &se) {
		offset = se.Offset
	}
	line, col := position(data, offset)
	return sampleError(fmt.Sprintf("Invalid JSON at line %d, column %d: %s", line, col, strings.TrimPrefix(err.Error(), "json: ")))
}

var (
	isoRE  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?$`)
	uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

func isDatetime(s string) bool {
	if !isoRE.MatchString(s) {
		return false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02", "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05.999999999"} {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// node accumulates what the samples say about one field.
type node struct {
	name     string
	seen     int // occurrences with a non-null value
	kinds    map[FieldType]int
	nulls    int
	strings  int
	dates    int
	uuids    int
	dateOnly int
	order    []string
	children map[string]*node
	items    *node
	parents  int             // occurrences of the containing object (for required)
	objects  int             // occurrences of this node as an object
	example  json.RawMessage // first non-null scalar, truncated (OC-059)
	first    json.RawMessage // first non-null scalar, verbatim (constant detection)
	varies   bool            // a later scalar differed from first
}

// MaxExampleRunes truncates string examples (OC-059).
const MaxExampleRunes = 200

// scalar records a scalar value for the field's example and constant detection.
func (n *node) scalar(v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	if n.first == nil {
		n.first = raw
		n.example = raw
		if s, ok := v.(string); ok {
			if r := []rune(s); len(r) > MaxExampleRunes {
				n.example, _ = json.Marshal(string(r[:MaxExampleRunes]) + "…")
			}
		}
	} else if !bytes.Equal(n.first, raw) {
		n.varies = true
	}
}

func newNode(name string) *node {
	return &node{name: name, kinds: map[FieldType]int{}, children: map[string]*node{}}
}

func (n *node) add(v any) {
	switch x := v.(type) {
	case nil:
		n.nulls++
		return
	case bool:
		n.kinds[Boolean]++
		n.scalar(x)
	case json.Number:
		if strings.ContainsAny(x.String(), ".eE") {
			n.kinds[Number]++
		} else {
			n.kinds[Integer]++
		}
		n.scalar(x)
	case string:
		n.kinds[String]++
		n.scalar(x)
		n.strings++
		switch {
		case isDatetime(x):
			n.dates++
			if len(x) == 10 {
				n.dateOnly++
			}
		case uuidRE.MatchString(x):
			n.uuids++
		}
	case *ordered:
		n.kinds[Object]++
		n.addObject(x)
	case []any:
		n.kinds[Array]++
		if n.items == nil {
			n.items = newNode("[]")
		}
		for _, it := range x {
			n.items.parents++
			n.items.add(it)
		}
	}
	n.seen++
}

func (n *node) addObject(obj *ordered) {
	n.objects++
	for _, k := range obj.keys {
		if _, ok := n.children[k]; !ok {
			c := newNode(k)           // first-seen order: the design follows the samples
			c.parents = n.objects - 1 // it was missing from every earlier occurrence
			n.children[k] = c
			n.order = append(n.order, k)
		}
	}
	for _, c := range n.children {
		c.parents++
	}
	for _, k := range obj.keys {
		n.children[k].add(obj.vals[k])
	}
}

// ordered is a JSON object that remembers its key order.
type ordered struct {
	keys []string
	vals map[string]any
}

// errTooDeep reports JSON nested deeper than MaxJSONDepth.
var errTooDeep = fmt.Errorf("JSON is nested more than %d levels deep", MaxJSONDepth)

// tooDeep reports whether data nests objects/arrays deeper than limit (strings are skipped).
func tooDeep(data []byte, limit int) bool {
	depth, inString, escaped := 0, false, false
	for _, c := range data {
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
		case c == '{' || c == '[':
			if depth++; depth > limit {
				return true
			}
		case c == '}' || c == ']':
			depth--
		}
	}
	return false
}

// decodeOrdered reads one JSON value, keeping object key order and numbers as json.Number.
func decodeOrdered(dec *json.Decoder) (any, error) { return decodeOrderedAt(dec, 0) }

func decodeOrderedAt(dec *json.Decoder, depth int) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if t, ok := tok.(json.Delim); ok {
		if depth >= MaxJSONDepth {
			return nil, errTooDeep
		}
		switch t {
		case '{':
			o := &ordered{vals: map[string]any{}}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := decodeOrderedAt(dec, depth+1)
				if err != nil {
					return nil, err
				}
				if _, dup := o.vals[k]; !dup {
					o.keys = append(o.keys, k)
				}
				o.vals[k] = v
			}
			_, err := dec.Token() // '}'
			return o, err
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeOrderedAt(dec, depth+1)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token() // ']'
			return arr, err
		}
	}
	return tok, nil
}

// builder collects what building the design reports besides the fields.
type builder struct {
	warnings []string
	hints    []ConstantHint
}

// discriminators are field names whose imported value is offered as a constant even from a
// single sample (OC-059).
var discriminators = map[string]bool{"type": true, "doctype": true, "_type": true, "kind": true, "schemaversion": true, "version": true}

// hint offers a scalar field outside arrays as a constant when its value never changes.
func (n *node) hint(f *DocField, path string, b *builder) {
	if n.first == nil || n.varies || n.nulls > 0 || strings.Contains(path, "[]") {
		return
	}
	switch {
	case discriminators[strings.ToLower(n.name)]:
		b.hints = append(b.hints, ConstantHint{FieldID: f.ID, Value: n.first, Reason: "discriminator-name"})
	case n.seen >= 2 && n.seen == n.parents:
		b.hints = append(b.hints, ConstantHint{FieldID: f.ID, Value: n.first, Reason: "same-in-every-sample"})
	}
}

// build turns the accumulated node into a field, collecting warnings for mixed types.
func (n *node) build(path string, b *builder) DocField {
	f := DocField{ID: NewID(), Name: n.name}
	kinds := make([]FieldType, 0, len(n.kinds))
	for k := range n.kinds {
		kinds = append(kinds, k)
	}
	if len(kinds) == 2 && n.kinds[Integer] > 0 && n.kinds[Number] > 0 {
		kinds = []FieldType{Number}
	}
	switch {
	case len(kinds) == 0:
		f.Type = Any // only nulls
	case len(kinds) > 1:
		f.Type = Any
		names := make([]string, len(kinds))
		for i, k := range kinds {
			names[i] = string(k)
		}
		sort.Strings(names)
		b.warnings = append(b.warnings, fmt.Sprintf("%s has mixed types (%s); it will accept any value.", path, strings.Join(names, ", ")))
	default:
		f.Type = kinds[0]
	}
	if f.Type.Scalar() && f.Type != Any {
		f.Example = n.example
		n.hint(&f, path, b)
	}
	switch f.Type {
	case String:
		switch {
		case n.dates == n.strings && n.strings > 0:
			f.Type, f.Format = Datetime, "iso8601"
			if n.dateOnly == n.strings {
				f.Format = "date"
			}
		case n.uuids == n.strings && n.strings > 0:
			f.Format = "uuid"
		}
	case Object:
		f.Children = []DocField{}
		for _, k := range n.order {
			c := n.children[k]
			cf := c.build(path+"."+k, b)
			cf.Required = c.seen == c.parents && c.nulls == 0
			f.Children = append(f.Children, cf)
		}
	case Array:
		if n.items != nil && (n.items.seen > 0 || n.items.nulls > 0) {
			it := n.items.build(path+"[]", b)
			f.Items = &it
		} else {
			f.Items = &DocField{ID: NewID(), Name: "[]", Type: Any}
		}
		f.Items.Name = "[]"
	}
	return f
}

// InferFromSamples derives a design from sample documents (OC-015 way 2): int+float → number,
// mixed scalars → any (with a warning), null-only → any, fields present (non-null) in every
// sample → required, array items merged across elements, ISO-8601 strings → datetime and
// UUID strings → string/uuid.
func InferFromSamples(docs []json.RawMessage) (Schema, []string, error) {
	s, b, err := inferDocs(docs)
	if err != nil {
		return Schema{}, nil, err
	}
	s.ConstantHints = b.hints
	return s, b.warnings, nil
}

func inferDocs(docs []json.RawMessage) (Schema, *builder, error) {
	root := newNode(RootName)
	for i, d := range docs {
		dec := json.NewDecoder(bytes.NewReader(d))
		dec.UseNumber()
		v, err := decodeOrdered(dec)
		obj, ok := v.(*ordered)
		if err != nil || !ok {
			return Schema{}, nil, sampleError(fmt.Sprintf("Document %d isn't a JSON object.", i+1))
		}
		root.add(obj)
	}
	b := &builder{warnings: []string{}}
	f := root.build("$", b)
	f.ID, f.Name, f.Type = RootID, RootName, Object
	if f.Children == nil {
		f.Children = []DocField{}
	}
	for i := range b.warnings {
		b.warnings[i] = strings.TrimPrefix(b.warnings[i], "$.")
	}
	return Schema{Root: f, Source: SourceJSONSample}, b, nil
}

// StripExamples removes every field's example and the reference (designs sampled from a live
// collection keep no production values, OC-059).
func StripExamples(s *Schema) {
	s.Reference, s.ConstantHints = nil, nil
	var strip func(f *DocField)
	strip = func(f *DocField) {
		f.Example = nil
		for i := range f.Children {
			strip(&f.Children[i])
		}
		if f.Items != nil {
			strip(f.Items)
		}
	}
	strip(&s.Root)
}
