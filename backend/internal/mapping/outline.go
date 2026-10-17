package mapping

import (
	"fmt"
	"strings"
)

// Outline describes what a mapping writes, one line per document field, for people reading an
// exported mappings.json (OC-158): "customer.name ← customers.name", "items[] ← order_items
// (array, order_id = order_id, at most 1000)", "fullName = `${self.first} ${self.last}`",
// "type = \"order\"". It follows the mapping sheet
// (OC-061): fields in tree order, with their sources and transforms; fields nothing fills are
// left out. The runner never reads it.
func Outline(s *Spec, driving TableRef) []string {
	var out []string
	if s.Key.Template != "" {
		out = append(out, "key = "+s.Key.Template)
	}
	paths := s.DocSchema.Paths()
	bound := map[string]*Relation{}
	for i := range s.Relations {
		if r := &s.Relations[i]; !r.LookupOnly && r.TargetFieldID != "" {
			bound[paths[r.TargetFieldID]] = r
		}
	}
	for _, row := range Sheet(s, driving, nil) {
		switch row.Status {
		case SheetBound:
			r := bound[row.Path]
			if r == nil {
				continue
			}
			path := row.Path
			if r.Many() && r.EffectiveShape() != ShapeKeyed {
				path += "[]"
			}
			out = append(out, path+" ← "+describeRelation(r))
		case SheetMapped, SheetAggregate:
			steps := strings.ReplaceAll(row.Transform, " → ", " | ")
			switch {
			case row.Source == "" && steps != "":
				out = append(out, row.Path+" = "+steps)
			case steps != "":
				out = append(out, row.Path+" ← "+row.Source+" | "+steps)
			default:
				out = append(out, row.Path+" ← "+row.Source)
			}
		case SheetConstant:
			out = append(out, row.Path+" = "+row.Constant)
		case SheetComputed:
			expr := strings.TrimPrefix(row.Source, ComputedTemplate+": ")
			line := row.Path + " = `" + expr + "`"
			if row.Transform != "" {
				line += " | " + strings.ReplaceAll(row.Transform, " → ", " | ")
			}
			out = append(out, line)
		}
	}
	if s.TypeField.Enabled && s.TypeField.Name != "" {
		out = append(out, fmt.Sprintf("%s = %q", s.TypeField.Name, s.TypeField.Value))
	}
	return out
}

// describeRelation is "order_items (array, order_id = order_id, at most 1000)".
func describeRelation(r *Relation) string {
	var parts []string
	switch {
	case !r.Many():
		parts = append(parts, "object")
	case r.EffectiveShape() == ShapeScalars:
		parts = append(parts, "array of "+r.ValueColumn)
	case r.EffectiveShape() == ShapeKeyed:
		parts = append(parts, "object keyed by "+r.KeyColumn)
	default:
		parts = append(parts, "array")
	}
	var join []string
	for _, j := range r.Join {
		join = append(join, j.Parent+" = "+j.Child)
	}
	if r.Via != nil {
		for _, j := range r.Via.ParentJoin {
			join = append(join, j.Parent+" = "+r.Via.Table.Name+"."+j.Via)
		}
		parts = append(parts, "via "+r.Via.Table.Name)
	}
	if len(join) > 0 {
		parts = append(parts, strings.Join(join, " and "))
	}
	if r.Many() {
		limit := r.Limit
		if limit == 0 {
			limit = DefaultEmbedLimit
		}
		parts = append(parts, fmt.Sprintf("at most %d", limit))
	}
	return r.Table.Name + " (" + strings.Join(parts, ", ") + ")"
}
