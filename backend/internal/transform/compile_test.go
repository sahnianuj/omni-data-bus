package transform

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
)

func TestCompile_AC5_Issues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *mapping.Spec)
		path   string
		want   string
	}{
		{"bad regex", func(s *mapping.Spec) {
			s.FieldMappings[1].Transforms = []mapping.Step{{Op: "trim"}, {Op: "replace", Args: map[string]any{"pattern": "(", "regex": true}}}
		}, "name.first", "replace: invalid regular expression"},
		{"lookup without map", func(s *mapping.Spec) { s.FieldMappings[1].Transforms = []mapping.Step{{Op: "lookup"}} }, "name.first", "lookup: map is required"},
		{"concat with one source", func(s *mapping.Spec) {
			s.FieldMappings[1].Transforms = []mapping.Step{{Op: "concat"}}
		}, "name.first", "concat needs at least two sources"},
		{"several sources without concat", func(s *mapping.Spec) { s.FieldMappings[3].Transforms = nil }, "fullName", "need a concat step"},
		{"array into a string", func(s *mapping.Spec) {
			s.FieldMappings[1].Transforms = []mapping.Step{{Op: "split", Args: map[string]any{"separator": ","}}}
		}, "name.first", "produces array, but name.first is string"},
		{"unknown op", func(s *mapping.Spec) { s.FieldMappings[1].Transforms = []mapping.Step{{Op: "explode"}} }, "name.first", "Unknown transform"},
		{"no sources, no constant", func(s *mapping.Spec) { s.FieldMappings[2].Sources = nil }, "name.last", "needs a constant"},
		{"unknown column", func(s *mapping.Spec) { s.FieldMappings[2].Sources = []mapping.SourceRef{{Column: "nope"}} }, "name.last", "isn't in the source table"},
		{"unknown field", func(s *mapping.Spec) { s.FieldMappings[2].TargetFieldID = "f_nope" }, "fieldMappings.m3", "Unknown target field"},
		{"bad key", func(s *mapping.Spec) { s.Key.Template = "c::{oops" }, "key", "key template is invalid"},
		{"key column missing", func(s *mapping.Spec) { s.Key.Template = "c::{gone}" }, "key", "unknown column"},
	}
	for _, c := range cases {
		s := baseSpec()
		c.mutate(s)
		p, issues := Compile(s, tbl(), Options{})
		require.Nil(t, p, c.name)
		require.NotEmpty(t, issues, c.name)
		require.Equal(t, c.path, issues[0].Path, c.name)
		require.Contains(t, issues[0].Message, c.want, c.name)
	}
	// Transform steps report their index.
	s := baseSpec()
	s.FieldMappings[1].Transforms = []mapping.Step{{Op: "trim"}, {Op: "lookup"}}
	_, issues := Compile(s, tbl(), Options{})
	require.Equal(t, 1, *issues[0].OpIndex)
	require.Equal(t, "lookup", issues[0].Op)

	// A constant needs no source; rejected and pending mappings are skipped.
	s = baseSpec()
	s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, fd("f_v", "schemaVersion", docschema.Integer))
	s.FieldMappings = append(s.FieldMappings,
		mapping.FieldMapping{ID: "mc", TargetFieldID: "f_v", Transforms: []mapping.Step{{Op: "constant", Args: map[string]any{"value": 2.0}}}, State: mapping.StateActive},
		mapping.FieldMapping{ID: "mp", TargetFieldID: "f_unmapped", Sources: []mapping.SourceRef{{Column: "nope"}}, State: mapping.StatePending},
	)
	p := compile(t, s)
	_, doc, _, _ := p.Apply(row(1, "a", "b", "c", "d"), nil)
	require.Contains(t, string(doc), `"schemaVersion":2`)
	require.Contains(t, string(doc), `"loyalty":null`, "a pending suggestion isn't migrated: the field stays null")
}
