package schema

import (
	"regexp"
	"strings"
)

// Check is a CHECK constraint, as PostgreSQL prints it (pg_get_constraintdef).
type Check struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

var (
	numNonNulls = regexp.MustCompile(`(?is)^\s*CHECK\s*\(+\s*num_nonnulls\s*\(([^()]*)\)\s*=\s*1\s*\)+\s*$`)
	caseNotNull = regexp.MustCompile(`(?is)CASE\s+WHEN\s+\(*\s*"?([A-Za-z_][A-Za-z0-9_$]*)"?\s+IS\s+NOT\s+NULL\s*\)*\s+THEN\s+1\s+ELSE\s+0\s+END`)
	sumIsOne    = regexp.MustCompile(`(?is)\)\s*=\s*1\s*\)+\s*$`)
	identifier  = regexp.MustCompile(`^"?([A-Za-z_][A-Za-z0-9_$]*)"?$`)
)

// ExclusiveArc reads the columns of an "exactly one is set" CHECK constraint (OC-171), in
// either form PostgreSQL prints: num_nonnulls(a, b, c) = 1, or a sum of
// CASE WHEN a IS NOT NULL THEN 1 ELSE 0 END terms = 1. Nil for any other constraint.
func ExclusiveArc(def string) []string {
	if m := numNonNulls.FindStringSubmatch(def); m != nil {
		var out []string
		for _, p := range strings.Split(m[1], ",") {
			id := identifier.FindStringSubmatch(strings.TrimSpace(p))
			if id == nil {
				return nil
			}
			out = append(out, id[1])
		}
		return out
	}
	terms := caseNotNull.FindAllStringSubmatch(def, -1)
	if len(terms) < 2 || !sumIsOne.MatchString(def) || strings.Count(strings.ToUpper(def), "CASE") != len(terms) {
		return nil
	}
	out := make([]string, len(terms))
	for i, t := range terms {
		out[i] = t[1]
	}
	return out
}
