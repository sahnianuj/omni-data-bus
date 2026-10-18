package docschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
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
	"description": true, "$ref": true, "anyOf": true, "oneOf": true, "const": true, "enum": true,
	"discriminator": true,
}

type jsImporter struct {
	root     *ordered
	warnings []string
	warned   map[string]bool
	stack    []string // $refs being expanded (cycle detection)
	hints    []ConstantHint
	// files are the other files of a bundle import (OC-171), by path; base is the file whose
	// local refs (#/$defs/…) are being resolved ("" = the main file).
	files    map[string]*ordered
	base     []string
	refsSeen []string // files reached through $ref
}

func (imp *jsImporter) curRoot() (*ordered, string) {
	if n := len(imp.base); n > 0 && imp.base[n-1] != "" {
		return imp.files[imp.base[n-1]], imp.base[n-1]
	}
	return imp.root, ""
}

// FromJSONSchema imports a JSON Schema (draft 2020-12 or draft-07 subset, OC-015 way 3):
// type, properties, required, items, format, description, local $ref ($defs/definitions,
// cycles rejected) and anyOf/oneOf of null + T (nullable T). Unsupported keywords are ignored
// with a warning.
func FromJSONSchema(data []byte) (Schema, []string, error) {
	root, err := parseSchemaObject(data)
	if err != nil {
		return Schema{}, nil, err
	}
	return (&jsImporter{root: root, warned: map[string]bool{}, warnings: []string{}}).design()
}

func parseSchemaObject(data []byte) (*ordered, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeOrdered(dec)
	if err != nil {
		return nil, syntaxErr(data, dec, err)
	}
	root, ok := v.(*ordered)
	if !ok {
		return nil, sampleError("A JSON Schema must be an object.")
	}
	return root, nil
}

// File is one file of a several-file design import (OC-171).
type File struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// FromJSONSchemaFiles imports a design split across files (OC-171): the first file is the
// document; the others are what its relative $refs point to, or the item designs of an array
// whose items have no fields and whose description names them ("see payments/*.json"), or
// that is named like their folder. Each such file becomes one variant of the array's items.
func FromJSONSchemaFiles(files []File) (Schema, []string, error) {
	if len(files) == 0 {
		return Schema{}, nil, sampleError("No file to import.")
	}
	root, err := parseSchemaObject([]byte(files[0].Content))
	if err != nil {
		return Schema{}, nil, err
	}
	imp := &jsImporter{root: root, warned: map[string]bool{}, warnings: []string{}, files: map[string]*ordered{}}
	others := []string{}
	for _, f := range files[1:] {
		n := path.Clean(strings.TrimPrefix(f.Name, "./"))
		r, err := parseSchemaObject([]byte(f.Content))
		if err != nil {
			return Schema{}, nil, sampleError(n + ": " + err.Error())
		}
		imp.files[n] = r
		others = append(others, n)
	}
	sort.Strings(others)
	s, _, err := imp.design()
	if err != nil {
		return Schema{}, nil, err
	}
	used := map[string]bool{}
	var attach func(f *DocField, p string) error
	attach = func(f *DocField, p string) error {
		for i := range f.Children {
			c := &f.Children[i]
			cp := c.Name
			if p != "" {
				cp = p + "." + c.Name
			}
			if c.Type == Array && c.Items != nil && c.Items.Type == Object && len(c.Items.Children) == 0 {
				var names []string
				for _, o := range others {
					if itemDesignFor(c, o) {
						names = append(names, o)
					}
				}
				if len(names) > 0 {
					alts := make([]*ordered, len(names))
					for k, n := range names {
						alts[k], used[n] = imp.files[n], true
					}
					v, err := imp.variants(alts, names, "[]", cp+"[]", "")
					if err != nil {
						return err
					}
					v.ID, v.Description = c.Items.ID, c.Items.Description
					c.Items = &v
				}
			}
			if err := attach(c, cp); err != nil {
				return err
			}
			if c.Items != nil {
				if err := attach(c.Items, cp+"[]"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := attach(&s.Root, ""); err != nil {
		return Schema{}, nil, err
	}
	s.ConstantHints = imp.hints
	warnings := append([]string{}, imp.warnings...)
	for _, o := range others {
		if !used[o] && !imp.referenced(o) {
			warnings = append(warnings, fmt.Sprintf("%s isn't referenced by the design and names no array of it; it wasn't used.", o))
		}
	}
	return s, warnings, nil
}

// itemDesignFor reports whether file name is an item design of array a: its description
// names the file or a pattern matching it (payments/*.schema.json), or the file is in a
// folder named like the array.
func itemDesignFor(a *DocField, name string) bool {
	for _, tok := range strings.FieldsFunc(a.Description+" "+a.Items.Description, func(r rune) bool { return r == ' ' || r == ',' || r == '(' || r == ')' || r == ';' || r == '\n' }) {
		tok = strings.TrimRight(strings.TrimPrefix(tok, "./"), ".:")
		if tok == "" || !strings.ContainsAny(tok, "/.") {
			continue
		}
		if ok, _ := path.Match(tok, name); ok || tok == name || strings.HasSuffix(tok, "/"+name) {
			return true
		}
		// Files chosen one by one have no folder: the pattern's last part names them.
		if !strings.Contains(name, "/") {
			if ok, _ := path.Match(path.Base(tok), name); ok {
				return true
			}
		}
		// "see migrate/cbdocs/payments/*.json": the last folders match.
		for t := tok; strings.Contains(t, "/"); t = t[strings.Index(t, "/")+1:] {
			if ok, _ := path.Match(t, name); ok {
				return true
			}
		}
	}
	return strings.EqualFold(path.Dir(name), a.Name)
}

func (imp *jsImporter) referenced(name string) bool {
	for _, r := range imp.refsSeen {
		if r == name {
			return true
		}
	}
	return false
}

// design converts the importer's root schema.
func (imp *jsImporter) design() (Schema, []string, error) {
	root := imp.root
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
	return Schema{Root: f, Source: SourceJSONSchema, ConstantHints: imp.hints}, imp.warnings, nil
}

func (imp *jsImporter) warn(path, keyword string) {
	key := keyword + "@" + path
	if imp.warned[key] {
		return
	}
	imp.warned[key] = true
	imp.warnings = append(imp.warnings, fmt.Sprintf("Ignored unsupported keyword %q at %s.", keyword, path))
}

// resolve finds a $ref's schema and the file it's in: a local ref (#/$defs/…) in the current
// file, or another file of a bundle import (payments/scc.schema.json, optionally with a #/…
// fragment), relative to the current file (OC-171).
func (imp *jsImporter) resolve(ref string) (*ordered, string, error) {
	root, file := imp.curRoot()
	fileRef, frag, _ := strings.Cut(ref, "#")
	if fileRef != "" {
		if imp.files == nil {
			return nil, "", sampleError("Only local references (#/$defs/… or #/definitions/…) are supported: " + ref + ". Import the files it references with it.")
		}
		target := path.Clean(path.Join(path.Dir(file), fileRef))
		r, ok := imp.files[target]
		if !ok {
			return nil, "", sampleError("Unresolved reference " + ref + ": import " + target + " with the design.")
		}
		root, file = r, target
		imp.refsSeen = append(imp.refsSeen, target)
		if frag == "" {
			return root, file, nil
		}
	}
	name := ""
	switch {
	case strings.HasPrefix(frag, "/$defs/"):
		name = strings.TrimPrefix(frag, "/$defs/")
	case strings.HasPrefix(frag, "/definitions/"):
		name = strings.TrimPrefix(frag, "/definitions/")
	default:
		return nil, "", sampleError("Only references to #/$defs/… or #/definitions/… (or to another imported file) are supported: " + ref)
	}
	for _, container := range []string{"$defs", "definitions"} {
		if defs, ok := root.vals[container].(*ordered); ok {
			if d, ok := defs.vals[name].(*ordered); ok {
				return d, file, nil
			}
		}
	}
	return nil, "", sampleError("Unresolved reference " + ref)
}

// deref follows $refs to the schema node itself (to read its title or type).
func (imp *jsImporter) deref(n *ordered) *ordered {
	for i := 0; i < 32; i++ {
		ref, ok := n.vals["$ref"].(string)
		if !ok {
			return n
		}
		next, _, err := imp.resolve(ref)
		if err != nil {
			return n
		}
		n = next
	}
	return n
}

func isObjectSchema(n *ordered) bool {
	if t, _ := n.vals["type"].(string); t == "object" {
		return true
	}
	_, props := n.vals["properties"]
	return props
}

// variants builds a variant set (OC-171) from object alternatives: each becomes an object
// child marked as a variant, named after its discriminator value (or title), in order.
func (imp *jsImporter) variants(alts []*ordered, files []string, name, path string, discriminator string) (DocField, error) {
	f := DocField{ID: NewID(), Name: name, Type: Object, Children: []DocField{}}
	for i, a := range alts {
		if files != nil && files[i] != "" {
			imp.base = append(imp.base, files[i])
		}
		vf, _, err := imp.field(a, fmt.Sprintf("variant%d", i+1), path)
		if files != nil && files[i] != "" {
			imp.base = imp.base[:len(imp.base)-1]
		}
		if err != nil {
			return DocField{}, err
		}
		title, _ := imp.deref(a).vals["title"].(string)
		vf.Variant = &Variant{Title: title}
		f.Children = append(f.Children, vf)
	}
	imp.discriminate(&f, discriminator)
	return f, nil
}

// discriminate finds the field telling the variants apart: the OpenAPI discriminator when
// given, else the field every variant fixes (const) to a different value. Variants are named
// after their value, else their title.
func (imp *jsImporter) discriminate(f *DocField, given string) {
	constOf := map[string]json.RawMessage{}
	for _, h := range imp.hints {
		constOf[h.FieldID] = h.Value
	}
	valueIn := func(v *DocField, field string) json.RawMessage {
		for i := range v.Children {
			if v.Children[i].Name == field {
				return constOf[v.Children[i].ID]
			}
		}
		return nil
	}
	field := given
	if field == "" && len(f.Children) > 0 {
		for _, c := range f.Children[0].Children {
			seen, ok := map[string]bool{}, true
			for i := range f.Children {
				v := valueIn(&f.Children[i], c.Name)
				if v == nil || seen[string(v)] {
					ok = false
					break
				}
				seen[string(v)] = true
			}
			if ok {
				field = c.Name
				break
			}
		}
	}
	used := map[string]bool{}
	for i := range f.Children {
		v := &f.Children[i]
		v.Variant.Field = field
		name := ""
		if field != "" {
			v.Variant.Value = valueIn(v, field)
			var s string
			if json.Unmarshal(v.Variant.Value, &s) == nil {
				name = identifierFor(s)
			}
		}
		if name == "" {
			name = identifierFor(v.Variant.Title)
		}
		if name == "" || used[name] {
			name = fmt.Sprintf("variant%d", i+1)
		}
		used[name] = true
		v.Name = name
	}
}

// identifierFor turns "PaymentDocument (SCC)" or "credit-card" into a field name.
func identifierFor(s string) string {
	if i := strings.LastIndex(s, "("); i >= 0 && strings.HasSuffix(strings.TrimSpace(s), ")") {
		s = strings.TrimSuffix(strings.TrimSpace(s[i+1:]), ")")
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9' && b.Len() > 0, r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '.':
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
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
		target, file, err := imp.resolve(ref)
		if err != nil {
			return DocField{}, false, err
		}
		imp.stack = append(imp.stack, ref)
		imp.base = append(imp.base, file)
		defer func() { imp.stack, imp.base = imp.stack[:len(imp.stack)-1], imp.base[:len(imp.base)-1] }()
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
			allObjects := len(nonNull) > 1
			for _, a := range nonNull {
				allObjects = allObjects && isObjectSchema(imp.deref(a))
			}
			if allObjects {
				// Objects of several shapes: a variant set (OC-171).
				disc := ""
				if d, ok := n.vals["discriminator"].(*ordered); ok {
					disc, _ = d.vals["propertyName"].(string)
				}
				f, err := imp.variants(nonNull, nil, name, path, disc)
				if d, ok := n.vals["description"].(string); ok {
					f.Description = d
				}
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
		} else if c, ok := n.vals["const"]; ok {
			// {"const": "email"} with no type: the constant's type.
			switch v := c.(type) {
			case string:
				typ = "string"
			case bool:
				typ = "boolean"
			case json.Number:
				typ = "number"
				if !strings.ContainsAny(v.String(), ".eE") {
					typ = "integer"
				}
			}
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
	if f.Type.Scalar() && f.Type != Any {
		imp.example(n, &f, path)
		if vals, ok := n.vals["enum"].([]any); ok {
			for _, v := range vals {
				if v == nil {
					nullable = true
					continue
				}
				if raw, err := json.Marshal(v); err == nil {
					f.Enum = append(f.Enum, raw)
				}
			}
		}
	}
	return f, nullable, nil
}

// example takes a scalar field's example from const, examples[0] or default (OC-059); const
// also becomes a constant hint.
func (imp *jsImporter) example(n *ordered, f *DocField, path string) {
	var v any
	if c, ok := n.vals["const"]; ok {
		v = c
	} else if ex, ok := n.vals["examples"].([]any); ok && len(ex) > 0 {
		v = ex[0]
	} else if d, ok := n.vals["default"]; ok {
		v = d
	}
	switch v.(type) {
	case string, json.Number, bool:
	default:
		return
	}
	if s, ok := v.(string); ok {
		if r := []rune(s); len(r) > MaxExampleRunes {
			v = string(r[:MaxExampleRunes]) + "…"
		}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	f.Example = raw
	// A schema's const is the customer's own statement, for fields inside arrays too (a
	// variant's discriminator, OC-171), not for a scalar array's item itself.
	if _, isConst := n.vals["const"]; isConst && !strings.HasSuffix(path, "[]") {
		imp.hints = append(imp.hints, ConstantHint{FieldID: f.ID, Value: raw, Reason: "schema-const"})
	}
}

// exportVariant is one variant's object schema, its discriminator a const.
func exportVariant(v *DocField) omap {
	m := exportField(v)
	if v.Variant.Title != "" {
		m = append(omap{{"title", v.Variant.Title}}, m...)
	}
	if v.Variant.Field == "" || len(v.Variant.Value) == 0 {
		return m
	}
	for i, e := range m {
		if e.k != "properties" {
			continue
		}
		props := e.v.(omap)
		for j, p := range props {
			if p.k == v.Variant.Field {
				field := append(omap{}, p.v.(omap)...)
				for k := range field {
					if field[k].k == "examples" {
						field[k] = kv{"const", v.Variant.Value}
					}
				}
				props[j] = kv{p.k, field}
			}
		}
		m[i] = kv{"properties", props}
	}
	return m
}

func variantField(f *DocField) string {
	for i := range f.Children {
		if v := f.Children[i].Variant; v != nil {
			return v.Field
		}
	}
	return ""
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
	if len(f.Example) > 0 {
		m = append(m, kv{"examples", []json.RawMessage{f.Example}})
	}
	if len(f.Enum) > 0 {
		m = append(m, kv{"enum", f.Enum})
	}
	switch f.Type {
	case Object:
		props := omap{}
		var required []string
		var variants []omap
		for i := range f.Children {
			c := &f.Children[i]
			if c.Variant != nil {
				variants = append(variants, exportVariant(c))
				continue
			}
			props = append(props, kv{c.Name, exportField(c)})
			if c.Required {
				required = append(required, c.Name)
			}
		}
		if len(props) > 0 || len(variants) == 0 {
			m = append(m, kv{"properties", props})
		}
		if len(required) > 0 {
			sort.Strings(required)
			m = append(m, kv{"required", required})
		}
		if len(variants) > 0 {
			// A variant set is a oneOf of its shapes (OC-171).
			m = append(m, kv{"oneOf", variants})
			if d := variantField(f); d != "" {
				m = append(m, kv{"discriminator", omap{{"propertyName", d}}})
			}
		}
	case Array:
		if f.Items != nil {
			m = append(m, kv{"items", exportField(f.Items)})
		}
	}
	return m
}
