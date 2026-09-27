package docschema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

func infer(t *testing.T, content string) (Schema, []string) {
	t.Helper()
	docs, err := ParseSamples([]byte(content))
	require.NoError(t, err)
	s, warnings, err := InferFromSamples(docs)
	require.NoError(t, err)
	require.Empty(t, Validate(&s))
	return s, warnings
}

func byName(fs []DocField) map[string]DocField {
	out := map[string]DocField{}
	for _, f := range fs {
		out[f.Name] = f
	}
	return out
}

func TestInfer_AC2_MergesTypesAndRequired(t *testing.T) {
	s, _ := infer(t, `[{"a":1,"b":"x"},{"a":2.5,"c":null},{"a":3,"b":"y","d":"2024-01-01T00:00:00Z"}]`)
	f := byName(s.Root.Children)
	require.Equal(t, Number, f["a"].Type)
	require.True(t, f["a"].Required)
	require.Equal(t, String, f["b"].Type)
	require.Equal(t, Any, f["c"].Type)
	require.Equal(t, Datetime, f["d"].Type)
	for _, n := range []string{"b", "c", "d"} {
		require.False(t, f[n].Required, n)
	}
	require.Equal(t, []string{"a", "b", "c", "d"}, []string{s.Root.Children[0].Name, s.Root.Children[1].Name, s.Root.Children[2].Name, s.Root.Children[3].Name}, "first-seen order")

	_, warnings := infer(t, `[{"x":1},{"x":"one"}]`)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "x has mixed types (integer, string)")
}

func TestInfer_AC2_ArrayOfObjectsUnion(t *testing.T) {
	s, _ := infer(t, `{"orders":[{"sku":"A","qty":1},{"sku":"B","price":9.5}],"tags":["a","b"],"empty":[]}`)
	f := byName(s.Root.Children)
	orders := f["orders"]
	require.Equal(t, Array, orders.Type)
	require.Equal(t, Object, orders.Items.Type)
	items := byName(orders.Items.Children)
	require.Len(t, items, 3)
	require.True(t, items["sku"].Required)
	require.False(t, items["qty"].Required)
	require.Equal(t, Number, items["price"].Type)
	require.Equal(t, String, f["tags"].Items.Type)
	require.Equal(t, Any, f["empty"].Items.Type)
}

func TestInfer_AC2_NDJSONAndArrayInputs(t *testing.T) {
	nd, _ := infer(t, "{\"a\":1}\n{\"a\":2,\"b\":true}\n")
	require.Equal(t, Boolean, byName(nd.Root.Children)["b"].Type)
	one, _ := infer(t, `{"a":{"b":{"c":1}}}`)
	require.Equal(t, Integer, one.Root.Children[0].Children[0].Children[0].Type)
}

func TestInfer_AC2_DatetimeAndUUIDDetection(t *testing.T) {
	s, _ := infer(t, `[{"d":"2024-01-01","ts":"2024-01-01T10:00:00+05:30","id":"0f8fad5b-d9cb-469f-a165-70867728950e","notDate":"2024-13-45"}]`)
	f := byName(s.Root.Children)
	require.Equal(t, Datetime, f["d"].Type)
	require.Equal(t, "date", f["d"].Format)
	require.Equal(t, "iso8601", f["ts"].Format)
	require.Equal(t, "uuid", f["id"].Format)
	require.Equal(t, String, f["notDate"].Type)
}

func TestInfer_AC2_RejectsOversizeAndInvalid(t *testing.T) {
	_, err := ParseSamples([]byte("{\"a\": 1,\n  \"b\": }"))
	require.True(t, problem.HasCode(err, CodeInvalidSample))
	require.Contains(t, problem.From(err).Detail, "line 2, column")

	_, err = ParseSamples([]byte(strings.Repeat(" ", MaxSampleBytes+1)))
	require.Contains(t, problem.From(err).Detail, "5 MB")

	many := strings.Repeat("{\"a\":1}\n", MaxSampleDocs+1)
	_, err = ParseSamples([]byte(many))
	require.Contains(t, problem.From(err).Detail, "1000 documents")

	_, err = ParseSamples([]byte(`[1,2]`))
	require.Contains(t, problem.From(err).Detail, "isn't a JSON object")
	_, err = ParseSamples([]byte("  "))
	require.Error(t, err)
	_, _, err = InferFromSamples([]json.RawMessage{json.RawMessage(`[1]`)})
	require.Error(t, err)
}

func nested(levels int) string {
	return strings.Repeat(`{"a":`, levels-1) + `{}` + strings.Repeat(`}`, levels-1)
}

func TestJSONDepth_AC5_Rejected(t *testing.T) {
	_, err := ParseSamples([]byte(nested(64)))
	require.NoError(t, err, "64 levels are fine")
	_, err = ParseSamples([]byte(nested(65)))
	require.ErrorContains(t, err, "at most 64 levels deep")
	require.Equal(t, CodeInvalidSample, problem.From(err).Code)
	_, err = ParseSamples([]byte("[" + nested(64) + "]"))
	require.NoError(t, err, "an array of samples adds no depth to each document")
	// Brackets inside strings don't count.
	_, err = ParseSamples([]byte(`{"s":"` + strings.Repeat("[{", 100) + `\"x"}`))
	require.NoError(t, err)

	// Documents that skip ParseSamples (sampled from a collection) are bounded while decoding.
	_, _, err = InferFromSamples([]json.RawMessage{json.RawMessage(nested(65))})
	require.Error(t, err)
	_, _, err = InferFromSamples([]json.RawMessage{json.RawMessage(nested(64))})
	require.NoError(t, err)
}
