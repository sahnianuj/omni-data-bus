package mapping

import "strings"

// KeyOverlap reports whether two key templates written to the same place can produce the same
// keys (OC-170): the same literal text before the first column and the same columns, in order
// ("cart::{id}" and "cart::{id}", not "cart::{id}" and "order::{id}"). Two mappings like that
// overwrite each other's documents, and the last job to run wins. Unparsable templates never
// overlap (validation reports them).
func KeyOverlap(a, b string) bool {
	ka, err := ParseKeyTemplate(a)
	if err != nil {
		return false
	}
	kb, err := ParseKeyTemplate(b)
	if err != nil {
		return false
	}
	return keyPrefix(ka) == keyPrefix(kb) && strings.Join(ka.Columns(), ",") == strings.Join(kb.Columns(), ",")
}

// keyPrefix is the literal text before the first column or special segment.
func keyPrefix(kt *KeyTemplate) string {
	var b strings.Builder
	for _, s := range kt.segments {
		if s.column != "" || s.special != "" {
			break
		}
		b.WriteString(s.literal)
	}
	return b.String()
}
