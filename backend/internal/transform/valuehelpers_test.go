package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
)

func TestToFloat(t *testing.T) {
	for _, v := range []any{int64(2), 2, 2.0} {
		f, ok := toFloat(v)
		require.True(t, ok, "%T", v)
		require.Equal(t, 2.0, f)
	}
	_, ok := toFloat("2")
	require.False(t, ok)
}

func TestNormalizeExprValue(t *testing.T) {
	require.Equal(t, int64(3), normalizeExprValue(3))
	require.Equal(t, int64(3), normalizeExprValue(int32(3)))
	require.Equal(t, 1.5, normalizeExprValue(float32(1.5)))
	require.Equal(t, []any{int64(1), "a", []any{int64(2)}}, normalizeExprValue([]any{1, "a", []any{int32(2)}}))
	require.Equal(t, "x", normalizeExprValue("x"))
}

func TestNumberTextAndLessValue(t *testing.T) {
	require.Equal(t, "12", numberText(int64(12)))
	require.Equal(t, "1.5", numberText(1.5))
	require.Equal(t, "3.25", numberText(json.Number("3.25")))
	require.Equal(t, "12345678901234567890.5", numberText("12345678901234567890.5"), "a high-precision numeric kept as text")
	require.Empty(t, numberText("abc"))
	require.Empty(t, numberText(true))

	require.True(t, lessValue(int64(9), int64(10)), "numbers compare as numbers, not text")
	require.True(t, lessValue("2.5", json.Number("10")))
	require.False(t, lessValue(1.5, int64(1)))
	require.True(t, lessValue("2026-01-02", "2026-10-01"), "dates compare as text")
	require.True(t, lessValue(int64(5), "b"), "mixed values compare as text")
}

func TestConstantTypeAndNormalizeArg(t *testing.T) {
	cases := map[docschema.FieldType]any{
		docschema.String:  "s",
		docschema.Integer: 3.0,
		docschema.Number:  3.5,
		docschema.Boolean: true,
		docschema.Array:   []any{1.0},
		docschema.Object:  map[string]any{},
		docschema.Any:     nil,
	}
	for want, v := range cases {
		require.Equal(t, want, constantType(docschema.Any, map[string]any{"value": v}), "%v", v)
	}
	require.Equal(t, docschema.Integer, constantType(docschema.Any, map[string]any{"value": json.Number("7")}))
	require.Equal(t, docschema.Number, constantType(docschema.Any, map[string]any{"value": json.Number("7.5")}))

	require.Equal(t, int64(4), normalizeArg(4.0))
	require.Equal(t, 4.5, normalizeArg(4.5))
	require.Equal(t, 1e300, normalizeArg(1e300), "too large to be an exact integer")
	require.Equal(t, int64(-2), normalizeArg(json.Number("-2")))
	require.Equal(t, 0.25, normalizeArg(json.Number("0.25")))
	require.Equal(t, json.Number("x"), normalizeArg(json.Number("x")))
}

func TestKindOfAndClip(t *testing.T) {
	cases := map[string]any{
		"text": "a", "numeric": int64(1), "boolean": true, "array": []any{}, "object": map[string]any{},
	}
	for want, v := range cases {
		require.Equal(t, want, kindOf(v))
	}
	require.Equal(t, "numeric", kindOf(1.5))
	require.Equal(t, "numeric", kindOf(json.Number("1")))
	require.Equal(t, "int32", kindOf(int32(1)))

	require.Equal(t, `"short"`, clip("short"))
	long := clip(strings.Repeat("x", 50))
	require.True(t, strings.HasSuffix(long, "…"))
	require.Equal(t, `"`+strings.Repeat("x", 40)+`"…`, long)
}

func TestRowError_Error(t *testing.T) {
	require.Equal(t, "TYPE_MISMATCH: bad", (&RowError{Code: CodeTypeMismatch, Message: "bad"}).Error())
}
