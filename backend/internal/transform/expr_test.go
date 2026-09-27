package transform

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
)

func exprSpec(target docschema.FieldType, cols []string, expr string) *mapping.Spec {
	s := baseSpec()
	s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, fd("f_x", "x", target))
	s.FieldMappings = append(s.FieldMappings, fm("mx", "f_x", cols, mapping.Step{Op: "expression", Args: map[string]any{"expr": expr}}))
	return s
}

func TestExpression_AC4_Evaluates(t *testing.T) {
	_, doc, _, rerr := compile(t, exprSpec(docschema.Number, []string{"balance"}, "value * 1.5")).Apply(row(1, "a", "b", "c", "d"), nil)
	require.Nil(t, rerr)
	require.Contains(t, string(doc), `"x":1851.75`)
	_, doc, _, _ = compile(t, exprSpec(docschema.String, nil, `row.first_name + " " + row.last_name`)).Apply(row(1, "Ada", "Lovelace", "c", "d"), nil)
	require.Contains(t, string(doc), `"x":"Ada Lovelace"`)
	_, doc, _, _ = compile(t, exprSpec(docschema.String, []string{"city"}, `coalesce(value, "unknown") | upper()`)).Apply(row(1, "a", "b", nil, "d"), nil)
	require.Contains(t, string(doc), `"x":"UNKNOWN"`)
	_, doc, _, _ = compile(t, exprSpec(docschema.Integer, []string{"first_name"}, `len(value) > 2 ? 1 : 0`)).Apply(row(1, "Ada", "b", "c", "d"), nil)
	require.Contains(t, string(doc), `"x":1`)
}

func TestExpression_AC4_CompileErrors(t *testing.T) {
	_, issues := Compile(exprSpec(docschema.String, []string{"city"}, "value +"), tbl(), Options{})
	require.Len(t, issues, 1)
	require.Equal(t, mapping.CodeTransformInvalid, issues[0].Code)
	require.Equal(t, "x", issues[0].Path)
	require.Equal(t, 0, *issues[0].OpIndex)
	require.Equal(t, 1, issues[0].Line)
	require.Positive(t, issues[0].Column)
	_, issues = Compile(exprSpec(docschema.String, []string{"city"}, "unknownThing(value)"), tbl(), Options{})
	require.Len(t, issues, 1)
	require.Contains(t, issues[0].Message, "unknownThing")

	// row is a map at run time, so column names are checked against the table here.
	_, issues = Compile(exprSpec(docschema.String, []string{"city"}, "upper(value)\n  + row.frist_name"), tbl(), Options{})
	require.Len(t, issues, 1)
	require.Equal(t, `expression: unknown column "frist_name"; did you mean "first_name"?`, issues[0].Message)
	require.Equal(t, 2, issues[0].Line)
	require.Equal(t, 9, issues[0].Column)
	_, issues = Compile(exprSpec(docschema.String, []string{"city"}, `row["nope"] ?? row.city`), tbl(), Options{})
	require.Len(t, issues, 1)
	require.Equal(t, `expression: unknown column "nope"`, issues[0].Message)
	require.Equal(t, 1, issues[0].Line)
	_, issues = Compile(exprSpec(docschema.String, []string{"city"}, `row.first_name + " " + row["last_name"]`), tbl(), Options{})
	require.Empty(t, issues)
}

func TestExpression_AC4_MaxNodes(t *testing.T) {
	expr := "value"
	for range 600 {
		expr += " + 1"
	}
	_, issues := Compile(exprSpec(docschema.Number, []string{"balance"}, expr), tbl(), Options{})
	require.Len(t, issues, 1)
	require.Contains(t, issues[0].Message, "node")
}

func TestExpression_AC4_NoDangerousBuiltins(t *testing.T) {
	var keys []string
	et := reflect.TypeOf(exprEnv(time.Now(), nil, nil))
	for i := range et.NumField() {
		keys = append(keys, et.Field(i).Tag.Get("expr"))
	}
	sort.Strings(keys)
	allowed := append([]string{"row", "value"}, ExprHelpers...)
	sort.Strings(allowed)
	require.Equal(t, allowed, keys, "the environment is exactly the allow-list")
	for _, builtin := range []string{"date(\"2024-01-01\")", "duration(\"1h\")", "get(row, \"x\")", "toJSON(row)", "fromJSON(value)", "split(value, \",\")", "now().Year()"} {
		_, issues := Compile(exprSpec(docschema.Any, []string{"city"}, builtin), tbl(), Options{})
		require.NotEmpty(t, issues, "%s must not compile", builtin)
	}
}

func TestExpression_AC4_NowIsJobStart(t *testing.T) {
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	p, issues := Compile(exprSpec(docschema.Datetime, []string{"city"}, "now()"), tbl(), Options{Now: start})
	require.Empty(t, issues)
	for range 3 {
		_, doc, _, _ := p.Apply(row(1, "a", "b", "c", "d"), nil)
		require.Contains(t, string(doc), `"x":"2026-09-01T12:00:00Z"`)
		time.Sleep(time.Millisecond)
	}
	_, _, _, rerr := compile(t, exprSpec(docschema.Any, []string{"balance"}, `value / (value - value)`)).Apply(row(1, "a", "b", "c", "d"), nil)
	if rerr != nil {
		require.Equal(t, CodeExprError, rerr.Code)
	}
	_, _, _, rerr = compile(t, exprSpec(docschema.Any, nil, `row.first_name.missing.deeper`)).Apply(row(1, "a", "b", "c", "d"), nil)
	require.NotNil(t, rerr)
	require.Equal(t, CodeExprError, rerr.Code)
}
