package docschema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

const customerSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Customer",
  "type": "object",
  "required": ["id", "email", "nickname"],
  "properties": {
    "id": { "type": "integer" },
    "email": { "type": "string", "format": "email" },
    "nickname": { "anyOf": [{ "type": "null" }, { "type": "string" }] },
    "createdAt": { "type": "string", "format": "date-time", "description": "When" },
    "address": { "$ref": "#/$defs/address" },
    "tags": { "type": "array", "items": { "type": "string" } },
    "meta": { "type": "object", "patternProperties": { "^x-": {} } }
  },
  "$defs": {
    "address": { "type": "object", "required": ["city"], "properties": { "city": { "type": "string" }, "zip": { "type": ["string", "null"] } } }
  }
}`

func TestFromJSONSchema_AC3_RefsAndNullable(t *testing.T) {
	s, _, err := FromJSONSchema([]byte(customerSchema))
	require.NoError(t, err)
	require.Empty(t, Validate(&s))
	f := byName(s.Root.Children)
	require.Equal(t, []string{"id", "email", "nickname", "createdAt", "address", "tags", "meta"}, names(s.Root.Children))
	require.True(t, f["id"].Required)
	require.Equal(t, "email", f["email"].Format)
	require.Equal(t, String, f["nickname"].Type)
	require.False(t, f["nickname"].Required, "nullable → optional even when listed in required")
	require.Equal(t, Datetime, f["createdAt"].Type)
	require.Equal(t, "When", f["createdAt"].Description)
	addr := byName(f["address"].Children)
	require.True(t, addr["city"].Required)
	require.Equal(t, String, addr["zip"].Type)
	require.Equal(t, String, f["tags"].Items.Type)
}

func names(fs []DocField) []string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}

func TestFromJSONSchema_AC3_CycleDetected(t *testing.T) {
	_, _, err := FromJSONSchema([]byte(`{"type":"object","properties":{"node":{"$ref":"#/$defs/node"}},
	  "$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"}}}}}`))
	require.True(t, problem.HasCode(err, CodeRefCycle))

	_, _, err = FromJSONSchema([]byte(`{"type":"object","properties":{"x":{"$ref":"https://example.com/x"}}}`))
	require.Error(t, err)
	_, _, err = FromJSONSchema([]byte(`{"type":"object","properties":{"x":{"$ref":"#/$defs/missing"}}}`))
	require.Error(t, err)
	_, _, err = FromJSONSchema([]byte(`{"type":"string"}`))
	require.Error(t, err, "root must be an object")
	_, _, err = FromJSONSchema([]byte(`{`))
	require.True(t, problem.HasCode(err, CodeInvalidSample))
}

func TestFromJSONSchema_AC3_UnsupportedWarns(t *testing.T) {
	_, warnings, err := FromJSONSchema([]byte(customerSchema))
	require.NoError(t, err)
	require.Equal(t, []string{`Ignored unsupported keyword "patternProperties" at $.meta.`}, warnings)

	s, warnings, err := FromJSONSchema([]byte(`{"properties":{"v":{"oneOf":[{"type":"string"},{"type":"integer"}]},"d":{"type":"string","format":"date"},"u":{}}}`))
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	f := byName(s.Root.Children)
	require.Equal(t, Any, f["v"].Type)
	require.Equal(t, "date", f["d"].Format)
	require.Equal(t, Any, f["u"].Type)
}

// clean drops IDs so trees can be compared.
func clean(f DocField) DocField {
	f.ID = ""
	for i := range f.Children {
		f.Children[i] = clean(f.Children[i])
	}
	if f.Items != nil {
		it := clean(*f.Items)
		f.Items = &it
	}
	return f
}

func TestToJSONSchema_AC3_RoundTrip(t *testing.T) {
	orig, _, err := FromJSONSchema([]byte(customerSchema))
	require.NoError(t, err)
	gen := FromTable(customerTable(), GenerateOptions{NestByPrefix: true})
	for _, s := range []Schema{orig, gen} {
		out, err := ToJSONSchema(&s)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(out), "{\n  \"$schema\""))
		back, _, err := FromJSONSchema(out)
		require.NoError(t, err)
		require.Equal(t, clean(s.Root), clean(back.Root))
	}
}

// OC-171 AC1: a oneOf of objects is a variant set, with its discriminator; [null, T] stays a
// nullable T; the export writes the same oneOf back.
func TestJSONSchema_OC171_AC1_Variants(t *testing.T) {
	src := `{"type":"object","properties":{
	  "nick":{"oneOf":[{"type":"null"},{"type":"string"}]},
	  "contactMethods":{"type":"array","items":{"oneOf":[
	    {"title":"Email","type":"object","properties":{"kind":{"const":"email"},"address":{"type":"string"}}},
	    {"title":"Phone","type":"object","properties":{"kind":{"const":"phone"},"number":{"type":"string"},"ext":{"type":"integer"}}},
	    {"title":"Postal","type":"object","properties":{"kind":{"const":"postal"},"city":{"type":"string"}}}]}}}}`
	s, warnings, err := FromJSONSchema([]byte(src))
	require.NoError(t, err)
	require.Empty(t, warnings)
	by := map[string]*DocField{}
	s.Walk(func(f *DocField, p string, _ *DocField) { by[p] = f })
	require.Equal(t, String, by["nick"].Type, "[null, string] is a nullable string")
	items := by["contactMethods[]"]
	require.True(t, items.HasVariants())
	var names []string
	for _, v := range items.Children {
		names = append(names, v.Name)
		require.Equal(t, "kind", v.Variant.Field)
	}
	require.Equal(t, []string{"email", "phone", "postal"}, names)
	require.JSONEq(t, `"phone"`, string(items.Children[1].Variant.Value))
	require.Equal(t, "Phone", items.Children[1].Variant.Title)
	// Conformance paths are the document's.
	var paths []string
	for _, c := range Contract(&s) {
		paths = append(paths, c.Path)
	}
	require.Contains(t, paths, "contactMethods[].number")
	require.NotContains(t, paths, "contactMethods[].phone.number")

	out, err := ToJSONSchema(&s)
	require.NoError(t, err)
	again, _, err := FromJSONSchema(out)
	require.NoError(t, err)
	var back []string
	again.Walk(func(f *DocField, _ string, _ *DocField) {
		if f.Variant != nil {
			back = append(back, f.Name+"="+string(f.Variant.Value))
		}
	})
	require.Equal(t, []string{`email="email"`, `phone="phone"`, `postal="postal"`}, back)
}
