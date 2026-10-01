// Package docschema is the target document design (OC-015, architecture §6.2): a tree of
// fields with stable IDs. Paths (name.first, tags[], orders[].sku) are derived, never stored.
package docschema

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"omnidatabus/internal/problem"
)

// FieldType is a target field's type.
type FieldType string

// Field types.
const (
	String   FieldType = "string"
	Integer  FieldType = "integer"
	Number   FieldType = "number"
	Boolean  FieldType = "boolean"
	Datetime FieldType = "datetime"
	Object   FieldType = "object"
	Array    FieldType = "array"
	Any      FieldType = "any"
)

// Valid reports whether t is a known type.
func (t FieldType) Valid() bool {
	switch t {
	case String, Integer, Number, Boolean, Datetime, Object, Array, Any:
		return true
	}
	return false
}

// Scalar reports whether t holds a single value (not object or array).
func (t FieldType) Scalar() bool { return t != Object && t != Array }

// DocField is one field of the design.
type DocField struct {
	ID          string     `json:"id"` // "fld_" + 10 random chars; stable across renames and moves
	Name        string     `json:"name"`
	Type        FieldType  `json:"type"`
	Required    bool       `json:"required,omitempty"`
	Description string     `json:"description,omitempty"`
	Children    []DocField `json:"children,omitempty"` // object
	Items       *DocField  `json:"items,omitempty"`    // array element
	Format      string     `json:"format,omitempty"`   // datetime: iso8601|epoch_ms|date; string: uuid|email
	// Binding is the document composer's metadata for an object or array node (OC-047).
	Binding *Binding `json:"binding,omitempty"`
	// Example is a scalar value from an imported design (OC-059): shown next to the field,
	// never written to documents.
	Example json.RawMessage `json:"example,omitempty"`
}

// Adapters let a node take a relation of the other cardinality (OC-047).
const (
	AdapterFirst = "first" // an object bound to a one-to-many or many-to-many relation: its first row
	AdapterWrap  = "wrap"  // an array bound to a many-to-one relation: a 0- or 1-item array
)

// Binding ties a composed node to the relation that fills it. RelationID mirrors the relation
// whose targetFieldId is this node (the relation is authoritative); Adapter changes how the
// relation's rows fill the node.
type Binding struct {
	RelationID string `json:"relationId,omitempty"`
	Adapter    string `json:"adapter,omitempty"`
}

// Schema is a document design. Source records how it was created.
type Schema struct {
	Root   DocField `json:"root"`
	Source string   `json:"source"` // generated | json-sample | json-schema | collection-sample | manual
	// Reference is the first imported sample, verbatim (compacted), when it fits in
	// MaxReferenceBytes (OC-059). Design-time only: it never reaches an exported bundle.
	Reference json.RawMessage `json:"reference,omitempty"`
	// Imported records the import that created the design; unlike Source it survives edits.
	Imported *Imported `json:"imported,omitempty"`
	// ConstantHints are fields whose imported values look fixed (OC-059). The composer offers
	// to keep each as a constant; applied or dismissed hints are removed.
	ConstantHints []ConstantHint `json:"constantHints,omitempty"`
}

// Imported describes the customer design a mapping was built from (OC-059).
type Imported struct {
	At         time.Time `json:"at"`
	FileName   string    `json:"fileName,omitempty"`
	Format     string    `json:"format"` // sample | ndjson | json-schema
	Samples    int       `json:"samples"`
	FieldCount int       `json:"fieldCount"`
	// Fields are the imported design's fields as the customer defined them: what the
	// generated documents are checked against (OC-061), whatever is edited later.
	Fields []ContractField `json:"fields,omitempty"`
}

// ContractField is one field of an imported design.
type ContractField struct {
	Path     string    `json:"path"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required,omitempty"`
}

// Contract lists a design's fields (array items included, as "x[]") in tree order.
func Contract(s *Schema) []ContractField {
	out := []ContractField{}
	s.Walk(func(f *DocField, path string, _ *DocField) {
		out = append(out, ContractField{Path: path, Type: f.Type, Required: f.Required})
	})
	return out
}

// Import formats.
const (
	FormatSample     = "sample"
	FormatNDJSON     = "ndjson"
	FormatJSONSchema = "json-schema"
)

// ConstantHint suggests keeping a field as a constant value.
type ConstantHint struct {
	FieldID string          `json:"fieldId"`
	Value   json.RawMessage `json:"value"`
	Reason  string          `json:"reason"` // same-in-every-sample | discriminator-name | schema-const
}

// Design sources.
const (
	SourceGenerated        = "generated"
	SourceJSONSample       = "json-sample"
	SourceJSONSchema       = "json-schema"
	SourceCollectionSample = "collection-sample"
	SourceManual           = "manual"
)

// Limits (OC-015 AC5).
const (
	MaxDepth   = 16
	MaxFields  = 2000
	MaxNameLen = 128
	// MaxReferenceBytes caps the stored reference document (OC-059).
	MaxReferenceBytes = 256 << 10
	// MaxExampleBytes caps one field's example (a 200-character string plus quoting and escapes).
	MaxExampleBytes = 1 << 10
	RootName        = "$"
	RootID          = "fld_root"
)

const idAlphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// NewID returns a new field ID, "fld_" + 10 random base62 characters.
func NewID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = idAlphabet[int(b[i])%len(idAlphabet)]
	}
	return "fld_" + string(b)
}

// NewRoot returns an empty object root.
func NewRoot() DocField {
	return DocField{ID: RootID, Name: RootName, Type: Object, Children: []DocField{}}
}

// Walk visits every field below the root (not the root itself) depth-first in design order,
// with its path.
func (s *Schema) Walk(fn func(f *DocField, path string, parent *DocField)) {
	var walk func(parent *DocField, prefix string)
	walk = func(parent *DocField, prefix string) {
		for i := range parent.Children {
			f := &parent.Children[i]
			p := f.Name
			if prefix != "" {
				p = prefix + "." + f.Name
			}
			fn(f, p, parent)
			visitNested(f, p, fn, walk)
		}
	}
	walk(&s.Root, "")
}

func visitNested(f *DocField, p string, fn func(*DocField, string, *DocField), walk func(*DocField, string)) {
	switch f.Type {
	case Object:
		walk(f, p)
	case Array:
		if f.Items != nil {
			ip := p + "[]"
			fn(f.Items, ip, f)
			visitNested(f.Items, ip, fn, walk)
		}
	}
}

// Paths maps field IDs to their paths.
func (s *Schema) Paths() map[string]string {
	out := map[string]string{}
	s.Walk(func(f *DocField, path string, _ *DocField) { out[f.ID] = path })
	return out
}

// Find returns the field with id, or nil.
func (s *Schema) Find(id string) *DocField {
	var found *DocField
	s.Walk(func(f *DocField, _ string, _ *DocField) {
		if f.ID == id && found == nil {
			found = f
		}
	})
	return found
}

// Leaf is a mappable target field (OC-017): scalars, any, arrays of scalars, and empty
// objects/arrays (a json/jsonb column can fill them).
type Leaf struct {
	FieldID     string
	Path        string
	Name        string
	Type        FieldType
	ItemType    FieldType // for arrays of scalars
	ParentNames []string
	Required    bool
	Empty       bool // an object/array with no children or item design
}

// Leaves lists the mappable fields in design order. Fields inside arrays of objects are not
// leaves: they are filled by embeds (OC-021).
func (s *Schema) Leaves() []Leaf {
	var out []Leaf
	var walk func(parent *DocField, prefix string, parents []string)
	walk = func(parent *DocField, prefix string, parents []string) {
		for i := range parent.Children {
			f := &parent.Children[i]
			p := f.Name
			if prefix != "" {
				p = prefix + "." + f.Name
			}
			switch {
			case f.Type == Object && len(f.Children) > 0:
				walk(f, p, append(append([]string{}, parents...), f.Name))
			case f.Type == Object:
				out = append(out, Leaf{FieldID: f.ID, Path: p, Name: f.Name, Type: Object, ParentNames: parents, Required: f.Required, Empty: true})
			case f.Type == Array:
				l := Leaf{FieldID: f.ID, Path: p, Name: f.Name, Type: Array, ParentNames: parents, Required: f.Required}
				switch {
				case f.Items == nil:
					l.Empty = true
				case f.Items.Type.Scalar():
					l.ItemType = f.Items.Type
				default:
					continue // array of objects/arrays: embeds
				}
				out = append(out, l)
			default:
				out = append(out, Leaf{FieldID: f.ID, Path: p, Name: f.Name, Type: f.Type, ParentNames: parents, Required: f.Required})
			}
		}
	}
	walk(&s.Root, "", nil)
	return out
}

// Count is the number of fields below the root.
func (s *Schema) Count() int {
	n := 0
	s.Walk(func(*DocField, string, *DocField) { n++ })
	return n
}

// Issue is a design problem at a field path.
type Issue struct {
	Path    string `json:"path"`
	FieldID string `json:"fieldId,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Validate checks the rules of OC-015 AC5: unique sibling names of 1–128 characters without
// "." or "[", depth ≤ 16, arrays with exactly one items (objects none), ≤ 2,000 fields and an
// object root. IDs must be unique.
func Validate(s *Schema) []Issue {
	var issues []Issue
	add := func(path, id, code, msg string) {
		issues = append(issues, Issue{Path: path, FieldID: id, Code: code, Message: msg})
	}
	if s.Root.Type != Object {
		add("$", s.Root.ID, "ROOT_NOT_OBJECT", "The document root must be an object.")
		return issues
	}
	ids := map[string]bool{}
	count := 0
	var check func(f *DocField, path string, depth int)
	check = func(parent *DocField, prefix string, depth int) {
		seen := map[string]bool{}
		for i := range parent.Children {
			f := &parent.Children[i]
			path := f.Name
			if prefix != "" {
				path = prefix + "." + f.Name
			}
			checkField(f, path, depth, false, seen, ids, &count, add, check)
		}
	}
	check(&s.Root, "", 1)
	if len(s.Reference) > MaxReferenceBytes {
		add("$", s.Root.ID, "REFERENCE_TOO_LARGE", fmt.Sprintf("The reference document is limited to %d KB.", MaxReferenceBytes>>10))
	}
	if count > MaxFields {
		add("$", s.Root.ID, "TOO_MANY_FIELDS", fmt.Sprintf("A design can have at most %d fields (this one has %d).", MaxFields, count))
	}
	return issues
}

func checkField(f *DocField, path string, depth int, item bool, seen, ids map[string]bool, count *int,
	add func(path, id, code, msg string), check func(*DocField, string, int),
) {
	*count++
	switch {
	case item: // array items are named "[]" by convention
	case f.Name == "" || len(f.Name) > MaxNameLen:
		add(path, f.ID, "INVALID_NAME", "Names must be 1–128 characters.")
	case strings.ContainsAny(f.Name, ".["):
		add(path, f.ID, "INVALID_NAME", "Names can't contain '.' or '[' (they separate paths).")
	case seen != nil && seen[f.Name]:
		add(path, f.ID, "DUPLICATE_NAME", "Another field at this level is already called '"+f.Name+"'.")
	}
	if seen != nil {
		seen[f.Name] = true
	}
	if f.ID == "" || ids[f.ID] {
		add(path, f.ID, "DUPLICATE_ID", "Every field needs a unique ID.")
	}
	ids[f.ID] = true
	if !f.Type.Valid() {
		add(path, f.ID, "INVALID_TYPE", "Unknown type '"+string(f.Type)+"'.")
	}
	if len(f.Example) > MaxExampleBytes {
		add(path, f.ID, "EXAMPLE_TOO_LARGE", fmt.Sprintf("Examples are limited to %d bytes.", MaxExampleBytes))
	}
	if depth > MaxDepth {
		add(path, f.ID, "TOO_DEEP", fmt.Sprintf("Designs can nest at most %d levels.", MaxDepth))
		return
	}
	switch f.Type {
	case Object:
		if f.Items != nil {
			add(path, f.ID, "OBJECT_WITH_ITEMS", "An object has fields, not items.")
		}
		check(f, path, depth+1)
	case Array:
		if f.Items == nil {
			add(path, f.ID, "ARRAY_WITHOUT_ITEMS", "An array needs an item type.")
			return
		}
		if len(f.Children) > 0 {
			add(path, f.ID, "ARRAY_WITH_CHILDREN", "An array has items, not fields.")
		}
		checkField(f.Items, path+"[]", depth+1, true, nil, ids, count, add, check)
	default:
		if len(f.Children) > 0 || f.Items != nil {
			add(path, f.ID, "SCALAR_WITH_CHILDREN", "Only objects and arrays can contain fields.")
		}
	}
}

// Problem turns issues into a 422 with one field error per issue.
func Problem(issues []Issue) problem.Problem {
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid document design", issues[0].Message)
	for _, i := range issues {
		p.FieldErrors = append(p.FieldErrors, problem.FieldError{Field: i.Path, Message: i.Message})
	}
	return p
}
