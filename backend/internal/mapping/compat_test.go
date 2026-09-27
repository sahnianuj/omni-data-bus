package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
)

// OC-017 AC3: TestTypeCompat_AC3_Matrix (full 8×8, one assertion per documented rule).
func TestTypeCompat_AC3_Matrix(t *testing.T) {
	types := []string{"string", "integer", "number", "boolean", "datetime", "any", "object", "array"}
	for _, src := range types {
		for _, dst := range types {
			class, _ := Compat(src, docschema.FieldType(dst))
			require.NotEmpty(t, class, "%s → %s", src, dst)
			if src == dst {
				require.Equal(t, ClassExact, class, "%s → %s", src, dst)
			}
		}
	}
	cases := []struct {
		src, dst, class, op string
	}{
		{"integer", "number", ClassExact, ""},
		{"integer", "string", ClassLossless, "toString"},
		{"datetime", "string", ClassLossless, "toString"},
		{"string", "integer", ClassLossy, "parseInteger"},
		{"number", "integer", ClassLossy, "parseInteger"},
		{"datetime", "integer", ClassLossy, "formatDatetime"},
		{"string", "object", ClassIncompatible, ""},
		{"integer", "array", ClassIncompatible, ""},
		{"any", "object", ClassExact, ""},
		{"any", "array", ClassExact, ""},
		{"string", "any", ClassExact, ""},
	}
	for _, c := range cases {
		class, tr := Compat(c.src, docschema.FieldType(c.dst))
		require.Equal(t, c.class, class, "%s → %s", c.src, c.dst)
		if c.op == "" {
			require.Nil(t, tr, "%s → %s", c.src, c.dst)
		} else {
			require.Equal(t, c.op, tr.Op, "%s → %s", c.src, c.dst)
		}
	}
	_, tr := Compat("datetime", docschema.Integer)
	require.Equal(t, map[string]any{"format": "epoch_ms"}, tr.Args)
	class, _ := Compat("mystery", docschema.String)
	require.Equal(t, ClassIncompatible, class)
	require.Equal(t, []float64{1, 0.85, 0.5, 0}, []float64{TypeScore(ClassExact), TypeScore(ClassLossless), TypeScore(ClassLossy), TypeScore(ClassIncompatible)})
}
