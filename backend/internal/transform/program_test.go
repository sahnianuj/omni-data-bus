package transform

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

func tbl() *schema.Table {
	p := 12
	return &schema.Table{Name: "customer", PrimaryKey: []string{"customer_id"}, Columns: []schema.Column{
		{Name: "customer_id", UDTName: "int4", JSONType: schema.TypeInteger},
		{Name: "first_name", UDTName: "text", JSONType: schema.TypeString},
		{Name: "last_name", UDTName: "text", JSONType: schema.TypeString},
		{Name: "city", UDTName: "text", JSONType: schema.TypeString},
		{Name: "zip", UDTName: "text", JSONType: schema.TypeString},
		{Name: "created_on", UDTName: "timestamptz", JSONType: schema.TypeDatetime},
		{Name: "balance", UDTName: "numeric", JSONType: schema.TypeNumber, NumericPrecision: &p},
		{Name: "tags", UDTName: "_text", JSONType: schema.TypeArray, ElementType: schema.TypeString, IsArray: true},
		{Name: "age_text", UDTName: "text", JSONType: schema.TypeString},
	}}
}

func fd(id, name string, t docschema.FieldType, kids ...docschema.DocField) docschema.DocField {
	f := docschema.DocField{ID: id, Name: name, Type: t}
	if t == docschema.Object {
		f.Children = kids
	}
	if t == docschema.Array {
		f.Items = &docschema.DocField{ID: id + "_i", Name: "[]", Type: docschema.String}
	}
	return f
}

func fm(id, target string, cols []string, steps ...mapping.Step) mapping.FieldMapping {
	var src []mapping.SourceRef
	for _, c := range cols {
		src = append(src, mapping.SourceRef{Column: c})
	}
	return mapping.FieldMapping{ID: id, TargetFieldID: target, Sources: src, Transforms: steps, Origin: mapping.OriginManual, State: mapping.StateActive}
}

func baseSpec() *mapping.Spec {
	s := &mapping.Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			fd("f_id", "customerId", docschema.Integer),
			fd("f_name", "name", docschema.Object, fd("f_first", "first", docschema.String), fd("f_last", "last", docschema.String)),
			fd("f_full", "fullName", docschema.String),
			fd("f_addr", "address", docschema.Object, fd("f_city", "city", docschema.String), fd("f_zip", "zip", docschema.String)),
			fd("f_created", "createdAt", docschema.Integer),
			fd("f_bal", "balance", docschema.Number),
			fd("f_tags", "tags", docschema.Array),
			fd("f_unmapped", "loyalty", docschema.String),
		}}},
		Key:       mapping.KeySpec{Template: "customer::{customer_id}"},
		TypeField: mapping.TypeField{Enabled: true, Name: "type", Value: "customer"},
		FieldMappings: []mapping.FieldMapping{
			fm("m1", "f_id", []string{"customer_id"}),
			fm("m2", "f_first", []string{"first_name"}, mapping.Step{Op: "trim"}),
			fm("m3", "f_last", []string{"last_name"}),
			fm("m4", "f_full", []string{"first_name", "last_name"}, mapping.Step{Op: "concat", Args: map[string]any{"separator": " "}}),
			fm("m5", "f_city", []string{"city"}),
			fm("m6", "f_zip", []string{"zip"}),
			fm("m7", "f_created", []string{"created_on"}, mapping.Step{Op: "formatDatetime", Args: map[string]any{"format": "epoch_ms"}}),
			fm("m8", "f_bal", []string{"balance"}),
			fm("m9", "f_tags", []string{"tags"}),
		},
	}
	s.Normalize()
	return s
}

func row(id int32, first, last, city, zip any) []any {
	num := mustNumeric("1234.50")
	return []any{id, first, last, city, zip, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), num, []any{"vip", nil}, "42"}
}

func compile(t *testing.T, s *mapping.Spec) *Program {
	t.Helper()
	p, issues := Compile(s, tbl(), Options{Now: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)})
	require.Empty(t, issues)
	require.NotNil(t, p)
	return p
}

func TestApply_AC6_NestedDocumentGolden(t *testing.T) {
	p := compile(t, baseSpec())
	key, doc, warnings, rerr := p.Apply(row(42, "  Ada ", "Lovelace", "London", "N1"), nil)
	require.Nil(t, rerr)
	require.Empty(t, warnings)
	require.Equal(t, "customer::42", key)
	require.Equal(t, `{"type":"customer","customerId":42,"name":{"first":"Ada","last":"Lovelace"},"fullName":"  Ada  Lovelace",`+
		`"address":{"city":"London","zip":"N1"},"createdAt":1704164645000,"balance":1234.5,"tags":["vip",null],"loyalty":null}`, string(doc))
	require.True(t, json.Valid(doc))
}

func TestApply_AC6_UnmappedFields(t *testing.T) {
	// By default a designed but unmapped field is written as null (the document keeps its
	// designed shape), whatever the null handling; with OmitUnmapped it's left out.
	for _, nh := range []string{mapping.NullInclude, mapping.NullOmit} {
		s := baseSpec()
		s.NullHandling = nh
		_, doc, _, rerr := compile(t, s).Apply(row(1, "a", "b", "c", "d"), nil)
		require.Nil(t, rerr)
		require.Contains(t, string(doc), `"loyalty":null`, nh)
	}
	s := baseSpec()
	s.OmitUnmapped = true
	_, doc, _, rerr := compile(t, s).Apply(row(1, "a", "b", "c", "d"), nil)
	require.Nil(t, rerr)
	require.NotContains(t, string(doc), "loyalty")
}

func TestApply_AC6_FieldOrderFollowsDesign(t *testing.T) {
	s := baseSpec()
	s.DocSchema.Root.Children[0], s.DocSchema.Root.Children[5] = s.DocSchema.Root.Children[5], s.DocSchema.Root.Children[0]
	_, doc, _, rerr := compile(t, s).Apply(row(1, "a", "b", "c", "d"), nil)
	require.Nil(t, rerr)
	require.True(t, strings.HasPrefix(string(doc), `{"type":"customer","balance":1234.5,`))
}

func TestApply_AC6_DocTooLarge(t *testing.T) {
	p := compile(t, baseSpec())
	_, _, _, rerr := p.Apply(row(1, strings.Repeat("x", MaxDocBytes), "b", "c", "d"), nil)
	require.NotNil(t, rerr)
	require.Equal(t, CodeDocTooLarge, rerr.Code)
}

func TestApply_AC3_NullHandlingIncludeOmit(t *testing.T) {
	s := baseSpec()
	p := compile(t, s)
	_, doc, _, _ := p.Apply(row(1, "Ada", nil, "London", nil), nil)
	require.Contains(t, string(doc), `"last":null`)
	require.Contains(t, string(doc), `"zip":null`)

	s.NullHandling = mapping.NullOmit
	s.FieldMappings[4].NullHandling = mapping.NullInclude // per-field override: city keeps null
	p = compile(t, s)
	_, doc, _, _ = p.Apply(row(1, "Ada", nil, nil, nil), nil)
	require.Contains(t, string(doc), `"name":{"first":"Ada"}`)
	require.Contains(t, string(doc), `"address":{"city":null}`)
	require.NotContains(t, string(doc), "zip")
}

func TestApply_AC3_EmptyObjectOmitted(t *testing.T) {
	s := baseSpec()
	s.NullHandling = mapping.NullOmit
	_, doc, _, _ := compile(t, s).Apply(row(1, "A", "B", nil, nil), nil)
	require.NotContains(t, string(doc), "address")
	require.True(t, json.Valid(doc))
}

func TestPipeline_AC3_Composition(t *testing.T) {
	s := baseSpec()
	s.FieldMappings[1].Transforms = []mapping.Step{{Op: "trim"}, {Op: "nullIf", Args: map[string]any{"value": ""}}, {Op: "default", Args: map[string]any{"value": "n/a"}}, {Op: "upper"}}
	_, doc, _, _ := compile(t, s).Apply(row(1, "  ", "B", "c", "d"), nil)
	require.Contains(t, string(doc), `"first":"N/A"`)
}

func TestApply_RowErrorsAndWarnings(t *testing.T) {
	s := baseSpec()
	s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, fd("f_age", "age", docschema.Integer))
	s.FieldMappings = append(s.FieldMappings, fm("m10", "f_age", []string{"age_text"}, mapping.Step{Op: "parseInteger", Args: map[string]any{"strict": true}}))
	p := compile(t, s)
	r := row(1, "a", "b", "c", "d")
	_, doc, _, rerr := p.Apply(r, nil)
	require.Nil(t, rerr)
	require.Contains(t, string(doc), `"age":42`)
	r[8] = "forty"
	_, _, _, rerr = p.Apply(r, nil)
	require.Equal(t, &RowError{FieldPath: "age", Op: "parseInteger", Code: CodeParseFailed, Message: `can't parse "forty"`}, rerr)

	s.FieldMappings[len(s.FieldMappings)-1].Transforms[0].Args = nil // non-strict: null + warning
	_, doc, warnings, rerr := compile(t, s).Apply(r, nil)
	require.Nil(t, rerr)
	require.Contains(t, string(doc), `"age":null`)
	require.Equal(t, "age", warnings[0].FieldPath)

	_, _, _, rerr = p.Apply(row(0, "a", "b", "c", "d")[:3], nil)
	require.Equal(t, CodeTypeMismatch, rerr.Code, "short rows are rejected")
	nullKey := row(1, "a", "b", "c", "d")
	nullKey[0] = nil
	_, _, _, rerr = p.Apply(nullKey, nil)
	require.Equal(t, mapping.CodeKeyNullComponent, rerr.Code)
}

func TestApply_TypeCoercionAtTheEnd(t *testing.T) {
	cases := []struct {
		v      any
		target docschema.FieldType
		want   any
		err    bool
	}{
		{int64(1), docschema.String, "1", false},
		{2.0, docschema.Integer, int64(2), false},
		{2.5, docschema.Integer, nil, true},
		{"9007199254740993", docschema.Integer, "9007199254740993", false},
		{"1.0000000001", docschema.Number, "1.0000000001", false},
		{"x", docschema.Number, nil, true},
		{"x", docschema.Boolean, nil, true},
		{int64(5), docschema.Datetime, int64(5), false},
		{map[string]any{}, docschema.Object, map[string]any{}, false},
		{"x", docschema.Array, nil, true},
		{"anything", docschema.Any, "anything", false},
		{json.Number("3"), docschema.Integer, json.Number("3"), false},
	}
	for _, c := range cases {
		got, err := coerce(c.v, c.target)
		if c.err {
			require.Error(t, err, "%v → %s", c.v, c.target)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, c.want, got, "%v → %s", c.v, c.target)
	}
}

func TestProgram_AC6_TraceMode(t *testing.T) {
	s := baseSpec()
	s.FieldMappings[1].Transforms = []mapping.Step{{Op: "trim"}, {Op: "upper"}}
	key, doc, _, trace, rerr := compile(t, s).ApplyTrace(row(7, " ann ", "b", "c", "d"), nil)
	require.Nil(t, rerr)
	require.Equal(t, "customer::7", key)
	require.Contains(t, string(doc), `"first":"ANN"`)
	require.Equal(t, []TraceStep{{Step: "source", Value: " ann "}, {Step: "trim", Value: "ann"}, {Step: "upper", Value: "ANN"}}, trace["name.first"])
	require.Equal(t, []TraceStep{{Step: "source", Value: int64(7)}}, trace["customerId"], "direct fields are traced too")
}

func TestAppendString_Escapes(t *testing.T) {
	for _, s := range []string{"plain", `quote"back\slash`, "tab\tnew\nline\rret", "\x01ctrl", "ünïcödé ✓", " sep", string([]byte{0xff, 'a'})} {
		b := appendString(nil, s)
		require.True(t, json.Valid(b), "%q → %s", s, b)
		var back string
		require.NoError(t, json.Unmarshal(b, &back))
		if s != string([]byte{0xff, 'a'}) {
			require.Equal(t, s, back)
		}
	}
	require.Equal(t, `{"a":[1,2.5,true,null,"x"],"b":{"c":"1e3"}}`, string(appendJSON(nil, map[string]any{"b": map[string]any{"c": "1e3"}, "a": []any{int64(1), 2.5, true, nil, "x"}})))
}
