package transform

import (
	"fmt"
	"sort"
	"strconv"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// Variant shapes (OC-171): an array's items take one of several shapes. Either the array's
// rows are one table and each row says which variant it is (a key that's set, or a type
// column's value: variantRule), or each variant has rows of its own and the array is their
// union (unionProg).

// Warning codes of variant arrays.
const (
	CodeVariantNoMatch = "VARIANT_NO_MATCH"
)

// variantRule is how a row is matched to a variant: column idx is set (value "") or equals value.
type variantRule struct {
	idx   int
	value string
}

type variantNode struct {
	id   string
	rule variantRule
	kids []*node
}

// variantRules are a relation's variants against its row table.
func variantRules(e mapping.Relation, t *schema.Table) map[string]variantRule {
	if len(e.Variants) == 0 {
		return nil
	}
	out := map[string]variantRule{}
	for _, v := range e.Variants {
		if i := columnIndex(t, v.When); i >= 0 {
			out[v.FieldID] = variantRule{idx: i, value: v.Value}
		}
	}
	return out
}

func (r variantRule) matches(row []any) bool {
	if r.idx < 0 || r.idx >= len(row) || row[r.idx] == nil {
		return false
	}
	return r.value == "" || String(row[r.idx]) == r.value
}

// isVariant reports whether the row being written is of variant v (set by writeObject).
func (p *Program) isVariant(v *variantNode, _ []any) bool { return p.curVariant == v }

// pickVariant is the first variant, in design order, whose rule the row matches (nil: none).
func (p *Program) pickVariant(row []any) *variantNode {
	for _, v := range p.variantOrder {
		if v.rule.matches(row) {
			return v
		}
	}
	return nil
}

// ofAnyVariant reports whether a row of a variant relation is of one of its variants.
func (p *Program) ofAnyVariant(row []any) bool { return p.pickVariant(row) != nil }

// unionProg is an array filled by several variant relations (pattern C), merged in their
// shared order column when they have one.
type unionProg struct {
	path  string
	parts []*embedProg
}

func unionOf(items *docschema.DocField, byEmbed map[string]*embedProg) *unionProg {
	u := &unionProg{}
	for i := range items.Children {
		if ep := byEmbed[items.Children[i].ID]; ep != nil && items.Children[i].Variant != nil {
			u.parts = append(u.parts, ep)
		}
	}
	if len(u.parts) == 0 {
		return nil
	}
	return u
}

type unionRow struct {
	part *embedProg
	row  source.RelatedRow
}

// orderValue is the row's order column, nil for a short row (writeObject then fails it).
func (r unionRow) orderValue() any {
	if r.part.orderIdx < len(r.row.Values) {
		return r.row.Values[r.part.orderIdx]
	}
	return nil
}

func (p *Program) writeUnion(dst []byte, u *unionProg) ([]byte, *RowError) {
	var rows []unionRow
	ordered := true
	for _, part := range u.parts {
		set := p.related[part.id]
		if set == nil {
			continue
		}
		n := min(len(set.Rows), part.limit)
		if len(set.Rows) > part.limit {
			p.warnings = append(p.warnings, Warning{FieldPath: u.path, Code: CodeEmbedTruncated, Message: fmt.Sprintf("more than %d rows of %s; kept the first %d", part.limit, part.table, part.limit)})
		}
		for _, r := range set.Rows[:n] {
			rows = append(rows, unionRow{part, r})
		}
		ordered = ordered && part.orderIdx >= 0
	}
	if ordered {
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			av, bv := a.orderValue(), b.orderValue()
			if a.part.orderDesc {
				return lessValue(bv, av)
			}
			return lessValue(av, bv)
		})
	}
	dst = append(dst, '[')
	for i, r := range rows {
		if i > 0 {
			dst = append(dst, ',')
		}
		prefix := u.path + "[" + strconv.Itoa(i) + "]."
		var rerr *RowError
		dst, rerr = r.part.sub.writeObject(dst, r.row, prefix)
		p.addWarnings(r.part.sub.warnings, prefix)
		if rerr != nil {
			return dst, rerr
		}
	}
	return append(dst, ']'), nil
}
