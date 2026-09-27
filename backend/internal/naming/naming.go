// Package naming splits identifiers into words and re-joins them in other styles. Document
// generation (OC-015), key and type-field defaults (OC-016) and auto-mapping (OC-017) share it,
// and web/src/lib/naming.ts mirrors it.
package naming

import (
	"strings"
	"unicode"
)

// Words splits an identifier on separators (_ - space .), on camelCase and PascalCase
// boundaries (customerID → customer, ID), and on letter/digit boundaries (addr2 → addr, 2).
// Case is preserved; callers lowercase as needed.
func Words(name string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == ' ' || r == '.' || r == '/':
			flush()
			continue
		case len(cur) > 0:
			prev := cur[len(cur)-1]
			next := rune(0)
			if i+1 < len(rs) {
				next = rs[i+1]
			}
			switch {
			case unicode.IsDigit(r) != unicode.IsDigit(prev) && (unicode.IsLetter(prev) || unicode.IsLetter(r)):
				flush() // addr2 → addr, 2
			case unicode.IsUpper(r) && unicode.IsLower(prev):
				flush() // customerId → customer, Id
			case unicode.IsUpper(r) && unicode.IsUpper(prev) && next != 0 && unicode.IsLower(next):
				flush() // URLValue → URL, Value
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

// Lower returns the lowercased words.
func Lower(name string) []string {
	w := Words(name)
	for i := range w {
		w[i] = strings.ToLower(w[i])
	}
	return w
}

// Camel joins words as camelCase: order_ID2 → orderId2, URL → url.
func Camel(name string) string {
	w := Lower(name)
	if len(w) == 0 {
		return name
	}
	var b strings.Builder
	b.WriteString(w[0])
	for _, s := range w[1:] {
		b.WriteString(upperFirst(s))
	}
	return b.String()
}

// Snake joins words as snake_case: orderID → order_id, "Order Details" → order_details.
func Snake(name string) string {
	w := Lower(name)
	if len(w) == 0 {
		return name
	}
	return strings.Join(w, "_")
}

func upperFirst(s string) string {
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// irregular plurals, and words that are the same singular and plural.
var irregular = map[string]string{
	"people": "person", "men": "man", "women": "woman", "children": "child", "mice": "mouse",
	"geese": "goose", "feet": "foot", "teeth": "tooth", "indices": "index", "matrices": "matrix",
	"vertices": "vertex", "criteria": "criterion", "phenomena": "phenomenon", "analyses": "analysis",
}

var invariant = map[string]bool{
	"data": true, "series": true, "species": true, "news": true, "status": true, "info": true, "metadata": true,
	"sheep": true, "fish": true, "equipment": true, "information": true, "address": true, "class": true,
	"bus": true, "gas": true, "alias": true, "canvas": true, "analysis": true, "access": true, "process": true,
	"business": true, "success": true, "progress": true,
}

// Singular converts one lowercase English word to its singular with rules good enough for
// identifiers: categories → category, addresses → address, statuses → status, people → person.
func Singular(w string) string {
	if s, ok := irregular[w]; ok {
		return s
	}
	if invariant[w] || len(w) < 3 {
		return w
	}
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "sses"), strings.HasSuffix(w, "uses"), strings.HasSuffix(w, "xes"),
		strings.HasSuffix(w, "zes"), strings.HasSuffix(w, "ches"), strings.HasSuffix(w, "shes"):
		return w[:len(w)-2]
	case strings.HasSuffix(w, "ss"), strings.HasSuffix(w, "us"), strings.HasSuffix(w, "is"):
		return w
	case strings.HasSuffix(w, "s"):
		return w[:len(w)-1]
	}
	return w
}

// SingularSnake singularizes the last word of a name and returns snake_case:
// customers → customer, order_items → order_item, "Order Details" → order_detail.
func SingularSnake(name string) string {
	w := Lower(name)
	if len(w) == 0 {
		return name
	}
	w[len(w)-1] = Singular(w[len(w)-1])
	return strings.Join(w, "_")
}
