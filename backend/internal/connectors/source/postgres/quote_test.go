package postgres

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

var bareIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// unquoteIdent reverses PostgreSQL identifier quoting: "a""b" → a"b. ok is false for an
// unbalanced or malformed quoted identifier.
func unquoteIdent(q string) (string, bool) {
	if !strings.HasPrefix(q, `"`) {
		return q, bareIdent.MatchString(q)
	}
	if len(q) < 2 || !strings.HasSuffix(q, `"`) {
		return "", false
	}
	inner := q[1 : len(q)-1]
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] == '"' {
			if i+1 >= len(inner) || inner[i+1] != '"' {
				return "", false // a lone quote would end the identifier early
			}
			i++
		}
		b.WriteByte(inner[i])
	}
	return b.String(), true
}

func FuzzQuoteIdent_AC6(f *testing.F) {
	for _, s := range []string{"customer", "Customer", `we"ird`, `"`, `""`, "a b", "select", "x; DROP TABLE t; --", "1st", "ünï", `"; --`, ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if strings.ContainsRune(s, 0) {
			t.Skip("PostgreSQL identifiers can't contain NUL")
		}
		for name, q := range map[string]string{"quoteIdent": quoteIdent(s), "Sanitize": pgx.Identifier{s}.Sanitize()} {
			got, ok := unquoteIdent(q)
			if !ok {
				t.Fatalf("%s(%q) = %q is not a well-formed identifier", name, s, q)
			}
			if got != s {
				t.Fatalf("%s(%q) = %q round-trips to %q", name, s, q, got)
			}
		}
	})
}
