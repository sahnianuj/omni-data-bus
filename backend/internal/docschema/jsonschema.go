package docschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"omnidatabus/internal/problem"
)

// CodeRefCycle is returned for a JSON Schema whose $refs loop.
const CodeRefCycle = problem.CodeSchemaRefCycle

// Keywords that carry no structure and are dropped silently; anything else we don't
// understand is reported as a warning.
var silentKeywords = map[string]bool{
	"$schema": true, "$id": true, "$comment": true, "title": true, "examples": true, "default": true,
	"$defs": true, "definitions": true, "readOnly": true, "writeOnly": true, "deprecated": true,
}

var supportedKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "items": true, "format": true,
	"description": true, "$ref": true, "anyOf": true, "oneOf": true,
}

type jsImporter struct {
	root     *ordered
	warnings []string
	warned   map[string]bool
	stack    []string // $refs being expanded (cycle detection)
}

// FromJSONSchema imports a JSON Schema (draft 2020-12 or draft-07 subset, OC-015 way 3):
// type, properties, required, items, format, description, local $ref ($defs/definitions,
// cycles rejected) and anyOf/oneOf of null + T (nullable T). Unsupported keywords are ignored
// with a warning.
func FromJSONSchema(data []byte) (Schema, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeOrdered(dec)
	if err != nil {
		return Schema{}, nil, syntaxErr(data, dec, err)
	}
	root, ok := v.(*ordered)
	if !ok {
		return Schema{}, nil, sampleError("A JSON Schema must be an object.")
	}
	imp := &jsImporter{root: root, warned: map[string]bool{}, warnings: []string{}}
	f, _, err := imp.field(root, RootName, "$")
	if err != nil {
		return Schema{}, nil, err
	}
	if f.Type != Object {
		return Schema{}, nil, sampleError("The schema's root must describe an object.")
	}
	f.ID, f.Name = RootID, RootName
	if f.Children == nil {
		f.Children = []DocField{}
	}
	return Schema{Root: f, Source: SourceJSONSchema}, imp.warnings, nil
}

func (imp *jsImporter) warn(path, keyword string) {
	key := keyword + "@" + path
	if imp.warned[key] {
		return
	}
	imp.warned[key] = true
	imp.warnings = append(imp.warnings, fmt.Sprintf("Ignored unsupported keyword %q at %s.", keyword, path))
}

func (imp *jsImporter) resolve(ref string) (*ordered, error) {
	name := ""
	switch {
	case strings.HasPrefix(ref, "#/$defs/"):
		name = strings.TrimPrefix(ref, "#/$defs/")
	case strings.HasPrefix(ref, "#/definitions/"):
		name = strings.TrimPrefix(ref, "#/definitions/")
	default:
		return nil, sampleError("Only local references (#/$defs/… or #/definitions/…) are supported: " + ref)
	}
	for _, container := range []string{"$defs", "definitions"} {
		if defs, ok := imp.root.vals[container].(*ordered); ok {
			if d, ok := defs.vals[name].(*ordered); ok {
				return d, nil
			}
		}
	}
	return nil, sampleError("Unresolved reference " + ref)
}

// field converts a schema node. nullable reports an anyOf/oneOf [null, T] or type [T, "null"].
func (imp *jsImporter) field(n *ordered, name, path string) (DocField, bool, error) {
	if ref, ok := n.vals["$ref"].(string); ok {
		for _, r := range imp.stack {
			if r == ref {
				return DocField{}, false, problem.New(http.StatusUnprocessableEntity, CodeRefCycle, "Cyclic $ref",
					"The schema references itself through "+strings.Join(append(imp.stack, ref), " → ")+"; cyclic designs can't be stored as documents.")
			}
		}
		target, err := imp.resolve(ref)
		if err != nil {
			return DocField{}, false, err
		}
		imp.stack = append(imp.stack, ref)
		defer func() { imp.stack = imp.stack[:len(imp.stack)-1] }()
		f, nullable, err := imp.field(target, name, path)
		if d, ok := n.vals["description"].(string); ok && err == nil {
			f.Description = d
		}
		return f, nullable, err
	}
	for _, k := range n.keys {
		if !supportedKeywords[k] && !silentKeywords[k] {
			imp.warn(path, k)
		}
	}
	for _, combo := range []string{"anyOf", "oneOf"} {
		if alts, ok := n.vals[combo].([]any); ok {
			var nonNull []*ordered
			hasNull := false
			for _, a := range alts {
				ao, ok := a.(*ordered)
				if !ok {
					continue
				}
				if t, _ := ao.vals["type"].(string); t == "null" {
					hasNull = true
					continue
				}
				nonNull = append(nonNull, ao)
			}
			if len(nonNull) == 1 {
				f, _, err := imp.field(nonNull[0], name, path)
				return f, hasNull, err
			}
			imp.warn(path, combo)
			return DocField{ID: NewID(), Name: name, Type: Any}, hasNull, nil
		}
	}
	f := DocField{ID: NewID(), Name: name}
	if d, ok := n.vals["description"].(string); ok {
		f.Description = d
	}
	nullable := false
	typ := ""
	switch t := n.vals["type"].(type) {
	case string:
		typ = t
	case []any:
		for _, x := range t {
			if s, _ := x.(string); s == "null" {
				nullable = true
			} else if typ == "" {
				typ = s
			} else {
				typ = "mixed"
			}
		}
	}
	if typ == "" {
		if _, ok := n.vals["properties"]; ok {
			typ = "object"
		} else if _, ok := n.vals["items"]; ok {
			typ = "array"
		}
	}
	format, _ := n.vals["format"].(string)
	switch typ {
	case "string":
		f.Type = String
		switch format {
		case "date-time":
			f.Type, f.Format = Datetime, "iso8601"
		case "date":
			f.Type, f.Format = Datetime, "date"
		case "email", "uuid":
			f.Format = format
		}
	case "integer":
		f.Type = Integer
	case "number":
		f.Type = Number
	case "boolean":
		f.Type = Boolean
	case "object":
		f.Type = Object
		f.Children = []DocField{}
		required := map[string]bool{}
		if req, ok := n.vals["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					required[s] = true
				}
			}
		}
		if props, ok := n.vals["properties"].(*ordered); ok {
			for _, k := range props.keys {
				pn, ok := props.vals[k].(*ordered)
				if !ok {
					continue
				}
				child, childNullable, err := imp.field(pn, k, path+"."+k)
				if err != nil {
					return DocField{}, false, err
				}
				child.Required = required[k] && !childNullable
				f.Children = append(f.Children, child)
			}
		}
	case "array":
		f.Type = Array
		if it, ok := n.vals["items"].(*ordered); ok {
			item, _, err := imp.field(it, "[]", path+"[]")
			if err != nil {
				return DocField{}, false, err
			}
			f.Items = &item
		} else {
			f.Items = &DocField{ID: NewID(), Name: "[]", Type: Any}
		}
	default:
		f.Type = Any
	}
	return f, nullable, nil
}

// kv is one entry of an ordered JSON object.
type kv struct {
	k string
	v any
}

// omap marshals as a JSON object in insertion order.
type omap []kv

// MarshalJSON implements json.Marshaler.
func (m omap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range m {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(e.k)
		b.Write(k)
		b.WriteByte(':')
		v, err := json.Marshal(e.v)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// ToJSONSchema exports the design as a draft 2020-12 JSON Schema (the designer's
// *Export JSON Schema*). Importing the export yields an equal tree, apart from IDs.
func ToJSONSchema(s *Schema) ([]byte, error) {
	root := exportField(&s.Root)
	out := append(omap{{"$schema", "https://json-schema.org/draft/2020-12/schema"}}, root...)
	return json.MarshalIndent(out, "", "  ")
}

func exportField(f *DocField) omap {
	var m omap
	switch f.Type {
	case Datetime:
		m = append(m, kv{"type", "string"})
		if f.Format == "date" {
			m = append(m, kv{"format", "date"})
		} else {
			m = append(m, kv{"format", "date-time"})
		}
	case Any:
		// no type: any value
	default:
		m = append(m, kv{"type", string(f.Type)})
		if f.Format == "email" || f.Format == "uuid" {
			m = append(m, kv{"format", f.Format})
		}
	}
	if f.Description != "" {
		m = append(m, kv{"description", f.Description})
	}
	switch f.Type {
	case Object:
		props := omap{}
		var required []string
		for i := range f.Children {
			c := &f.Children[i]
			props = append(props, kv{c.Name, exportField(c)})
			if c.Required {
				required = append(required, c.Name)
			}
		}
		m = append(m, kv{"properties", props})
		if len(required) > 0 {
			sort.Strings(required)
			m = append(m, kv{"required", required})
		}
	case Array:
		if f.Items != nil {
			m = append(m, kv{"items", exportField(f.Items)})
		}
	}
	return m
}
