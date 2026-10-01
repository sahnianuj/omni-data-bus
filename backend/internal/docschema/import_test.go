package docschema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const orders = `{"orderId":1001,"type":"order","orderDate":"2024-03-01T10:00:00Z","customer":{"name":"Ada"},"items":[{"sku":"A","qty":2}],"total":9.5}
{"orderId":1002,"type":"order","orderDate":"2024-03-02T11:00:00Z","couponCode":"SPRING","customer":{"name":"Bo"},"items":[{"sku":"B","qty":1,"note":"gift"}],"total":12}
{"orderId":1003,"type":"order","orderDate":"2024-03-03T12:00:00Z","customer":{"name":"Cy"},"items":[],"total":3.25}`

func TestInfer_AC1_ExamplesAndOrder(t *testing.T) {
	s, warnings, err := Import(KindJSONSample, orders, "orders.ndjson")
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Empty(t, Validate(&s))

	// The file's key order, with couponCode where it first appeared.
	require.Equal(t, []string{"orderId", "type", "orderDate", "customer", "items", "total", "couponCode"}, names(s.Root.Children))
	f := byName(s.Root.Children)
	require.Equal(t, Datetime, f["orderDate"].Type)
	require.Equal(t, "iso8601", f["orderDate"].Format)
	require.False(t, f["couponCode"].Required)
	for _, n := range []string{"orderId", "type", "orderDate", "customer", "items", "total"} {
		require.True(t, f[n].Required, n)
	}
	items := f["items"].Items
	require.Equal(t, Object, items.Type)
	require.Equal(t, []string{"sku", "qty", "note"}, names(items.Children)) // merged across samples

	// Examples: the first non-null value; none on containers.
	require.JSONEq(t, `1001`, string(f["orderId"].Example))
	require.JSONEq(t, `"SPRING"`, string(f["couponCode"].Example))
	require.JSONEq(t, `"A"`, string(items.Children[0].Example))
	require.Nil(t, f["customer"].Example)

	require.Equal(t, &Imported{At: s.Imported.At, FileName: "orders.ndjson", Format: FormatNDJSON, Samples: 3, FieldCount: 11, Fields: Contract(&s)}, s.Imported)
	require.Contains(t, s.Imported.Fields, ContractField{Path: "items[].sku", Type: String, Required: true})
	require.Contains(t, s.Imported.Fields, ContractField{Path: "couponCode", Type: String})
	require.False(t, s.Imported.At.IsZero())
	require.JSONEq(t, strings.SplitN(orders, "\n", 2)[0], string(s.Reference))
}

func TestInfer_AC1_ArrayFormat(t *testing.T) {
	s, _, err := Import(KindJSONSample, `[{"a":1},{"a":2}]`, "")
	require.NoError(t, err)
	require.Equal(t, FormatSample, s.Imported.Format)
	require.Equal(t, 2, s.Imported.Samples)
	require.JSONEq(t, `{"a":1}`, string(s.Reference))
}

func TestInfer_AC2_JSONSchemaExamples(t *testing.T) {
	schema := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["id"],
	  "properties":{
	    "id":{"type":"integer","examples":[7]},
	    "type":{"type":"string","const":"order"},
	    "placed":{"type":"string","format":"date-time","description":"When","default":"2024-01-01T00:00:00Z"},
	    "tags":{"type":"array","items":{"type":"string","const":"x"}}}}`
	s, warnings, err := Import(KindJSONSchema, schema, "order.schema.json")
	require.NoError(t, err)
	require.Empty(t, warnings) // const, examples and default are understood
	f := byName(s.Root.Children)
	require.True(t, f["id"].Required)
	require.JSONEq(t, `7`, string(f["id"].Example))
	require.JSONEq(t, `"order"`, string(f["type"].Example))
	require.Equal(t, "When", f["placed"].Description)
	require.JSONEq(t, `"2024-01-01T00:00:00Z"`, string(f["placed"].Example))
	require.Equal(t, []ConstantHint{{FieldID: f["type"].ID, Value: json.RawMessage(`"order"`), Reason: "schema-const"}}, s.ConstantHints)
	require.Equal(t, FormatJSONSchema, s.Imported.Format)
	require.Nil(t, s.Reference)

	// Examples survive an export → import round trip.
	out, err := ToJSONSchema(&s)
	require.NoError(t, err)
	back, _, err := FromJSONSchema(out)
	require.NoError(t, err)
	require.JSONEq(t, `7`, string(byName(back.Root.Children)["id"].Example))
}

func TestInfer_AC3_ReferenceCap(t *testing.T) {
	big := `{"id":1,"blob":"` + strings.Repeat("x", 300<<10) + `"}`
	s, warnings, err := Import(KindJSONSample, big, "")
	require.NoError(t, err)
	require.Nil(t, s.Reference)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "reference document")
	f := byName(s.Root.Children)
	require.JSONEq(t, `1`, string(f["id"].Example))
	var blob string
	require.NoError(t, json.Unmarshal(f["blob"].Example, &blob))
	require.Len(t, []rune(blob), MaxExampleRunes+1) // truncated, with an ellipsis
	require.Empty(t, Validate(&s))

	s.Reference = json.RawMessage(big)
	require.Equal(t, "REFERENCE_TOO_LARGE", Validate(&s)[0].Code)
}

func TestInfer_AC4_ConstantHints(t *testing.T) {
	three := `{"type":"order","schemaVersion":2,"id":1,"status":"paid","items":[{"kind":"line"}]}
{"type":"order","schemaVersion":2,"id":2,"status":"open","items":[{"kind":"line"}]}
{"type":"order","schemaVersion":2,"id":3,"status":"paid","items":[]}`
	s, _, err := Import(KindJSONSample, three, "")
	require.NoError(t, err)
	f := byName(s.Root.Children)
	require.Equal(t, []ConstantHint{
		{FieldID: f["type"].ID, Value: json.RawMessage(`"order"`), Reason: "discriminator-name"},
		{FieldID: f["schemaVersion"].ID, Value: json.RawMessage(`2`), Reason: "discriminator-name"},
	}, s.ConstantHints) // id and status vary; items[].kind is inside an array

	// One sample: only the dictionary names.
	one, _, err := Import(KindJSONSample, `{"docType":"invoice","currency":"USD"}`, "")
	require.NoError(t, err)
	require.Len(t, one.ConstantHints, 1)
	require.Equal(t, "discriminator-name", one.ConstantHints[0].Reason)

	// Two samples with the same value in both: offered, whatever the name.
	two, _, err := Import(KindJSONSample, `{"currency":"USD","n":1}`+"\n"+`{"currency":"USD","n":2}`, "")
	require.NoError(t, err)
	require.Len(t, two.ConstantHints, 1)
	require.Equal(t, "same-in-every-sample", two.ConstantHints[0].Reason)
}

func TestStripExamples(t *testing.T) {
	s, _, err := Import(KindJSONSample, orders, "")
	require.NoError(t, err)
	StripExamples(&s)
	require.Nil(t, s.Reference)
	require.Nil(t, s.ConstantHints)
	s.Walk(func(f *DocField, _ string, _ *DocField) { require.Nil(t, f.Example, f.Name) })
}
