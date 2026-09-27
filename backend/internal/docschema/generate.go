package docschema

import (
	"strconv"
	"strings"

	"omnidatabus/internal/naming"
	"omnidatabus/internal/schema"
)

// Naming styles for generated field names.
const (
	NamingCamel    = "camelCase"
	NamingSnake    = "snake_case"
	NamingPreserve = "preserve"
)

// GenerateOptions tune FromTable.
type GenerateOptions struct {
	Naming string `json:"naming"` // camelCase (default) | snake_case | preserve
	// NestByPrefix groups columns sharing a first word (billing_street, billing_city → billing: {street, city})
	// when at least two columns share it.
	NestByPrefix bool `json:"nestByPrefix"`
}

// Rename applies a naming style.
func Rename(name, style string) string {
	switch style {
	case NamingPreserve:
		return name
	case NamingSnake:
		return naming.Snake(name)
	}
	return naming.Camel(name)
}

// FieldTypeOf maps a column's jsonType to a field type (and format).
func FieldTypeOf(c schema.Column) (FieldType, string) {
	switch c.JSONType {
	case schema.TypeInteger:
		return Integer, ""
	case schema.TypeNumber:
		return Number, ""
	case schema.TypeBoolean:
		return Boolean, ""
	case schema.TypeDatetime:
		if c.UDTName == "date" {
			return Datetime, "date"
		}
		return Datetime, "iso8601"
	case schema.TypeAny:
		return Any, ""
	case schema.TypeArray:
		return Array, ""
	}
	if c.UDTName == "uuid" {
		return String, "uuid"
	}
	return String, ""
}

func fieldFor(c schema.Column, name string) DocField {
	t, format := FieldTypeOf(c)
	f := DocField{ID: NewID(), Name: name, Type: t, Required: !c.Nullable, Format: format}
	if t == Array {
		it := schema.Column{JSONType: c.ElementType}
		itemType, itemFormat := FieldTypeOf(it)
		if !itemType.Scalar() {
			itemType = Any
		}
		f.Items = &DocField{ID: NewID(), Name: "[]", Type: itemType, Format: itemFormat}
	}
	return f
}

// FromTable generates a design with one field per column, in column order (OC-015 way 1).
func FromTable(t *schema.Table, opts GenerateOptions) Schema {
	root := NewRoot()
	groups := map[string]int{}
	if opts.NestByPrefix {
		for _, c := range t.Columns {
			if w := naming.Lower(c.Name); len(w) > 1 {
				groups[w[0]]++
			}
		}
	}
	placed := map[string]int{} // prefix → index of its object in root.Children
	for _, c := range t.Columns {
		w := naming.Lower(c.Name)
		if opts.NestByPrefix && len(w) > 1 && groups[w[0]] >= 2 {
			prefix := w[0]
			idx, ok := placed[prefix]
			if !ok {
				idx = len(root.Children)
				placed[prefix] = idx
				root.Children = append(root.Children, DocField{ID: NewID(), Name: Rename(prefix, opts.Naming), Type: Object, Children: []DocField{}})
			}
			rest := strings.Join(w[1:], "_")
			if opts.Naming == NamingPreserve {
				rest = strings.TrimLeft(c.Name[len(strings.SplitN(c.Name, "_", 2)[0]):], "_")
				if rest == "" {
					rest = c.Name
				}
			}
			obj := &root.Children[idx]
			obj.Children = append(obj.Children, fieldFor(c, UniqueName(obj, Rename(rest, opts.Naming))))
			continue
		}
		root.Children = append(root.Children, fieldFor(c, UniqueName(&root, Rename(c.Name, opts.Naming))))
	}
	return Schema{Root: root, Source: SourceGenerated}
}

// UniqueName returns name, or name2, name3… when a child of parent already uses it.
func UniqueName(parent *DocField, name string) string {
	taken := map[string]bool{}
	for _, c := range parent.Children {
		taken[c.Name] = true
	}
	if !taken[name] {
		return name
	}
	for i := 2; ; i++ {
		if n := name + strconv.Itoa(i); !taken[n] {
			return n
		}
	}
}
