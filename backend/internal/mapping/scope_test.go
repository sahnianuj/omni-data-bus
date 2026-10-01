package mapping

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
)

type scopeFixture struct {
	Spec   Spec `json:"spec"`
	Scopes []struct {
		FieldID string   `json:"fieldId"`
		Scope   []string `json:"scope"`
	} `json:"scopes"`
	Sources []struct {
		FieldID  string `json:"fieldId"`
		Relation string `json:"relation"`
		Column   string `json:"column"`
		InScope  bool   `json:"inScope"`
	} `json:"sources"`
}

func loadScopeFixture(t *testing.T) scopeFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/scope-cases.json")
	require.NoError(t, err)
	var f scopeFixture
	require.NoError(t, json.Unmarshal(raw, &f))
	f.Spec.Normalize()
	return f
}

// The same fixture drives web/src/features/composer/scope.test.ts.
func TestScope_AC2_Rules(t *testing.T) {
	f := loadScopeFixture(t)
	for _, c := range f.Scopes {
		var ids []string
		for _, e := range Scope(&f.Spec, c.FieldID) {
			ids = append(ids, e.RelationID)
		}
		require.Equal(t, c.Scope, ids, c.FieldID)
	}
	paths := f.Spec.DocSchema.Paths()
	for _, c := range f.Sources {
		s := f.Spec
		s.FieldMappings = []FieldMapping{{ID: "m", TargetFieldID: c.FieldID, State: StateActive, Sources: []SourceRef{{Relation: c.Relation, Column: c.Column}}}}
		issues := validateScopes(&s, FieldScopes(&s), paths)
		require.Equal(t, !c.InScope, len(issues) == 1, "%s ← %s.%s: %v", c.FieldID, c.Relation, c.Column, issues)
		if len(issues) == 1 {
			require.Equal(t, CodeSourceOutOfScope, issues[0].Code)
		}
	}
}

func TestScope_AC2_Message(t *testing.T) {
	f := loadScopeFixture(t)
	s := f.Spec
	// products bound at the root, not under items: items[].productName can't read it.
	s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, docschema.DocField{ID: "f_featured", Name: "featured", Type: docschema.Object, Children: []docschema.DocField{}})
	for i := range s.Relations {
		if s.Relations[i].ID == "rel_prod" {
			s.Relations[i].ParentID, s.Relations[i].LookupOnly, s.Relations[i].TargetFieldID = nil, false, "f_featured"
		}
	}
	s.FieldMappings = []FieldMapping{{ID: "m", TargetFieldID: "f_pname", State: StateActive, Sources: []SourceRef{{Relation: "rel_prod", Column: "name"}}}}
	issues := validateScopes(&s, FieldScopes(&s), s.DocSchema.Paths())
	require.Len(t, issues, 1)
	require.Equal(t, "items[].productName reads products.name, but products isn't bound on the path to this field; bind it under items or add a lookup.", issues[0].Message)
	require.Equal(t, "f_pname", issues[0].FieldID)
}

func TestScope_EntriesCarryTables(t *testing.T) {
	f := loadScopeFixture(t)
	got := Scope(&f.Spec, "f_label")
	require.Equal(t, TableRef{Schema: "public", Name: "orders"}, got[0].Table)
	require.True(t, got[1].Lookup)
	require.Equal(t, &TableRef{Schema: "public", Name: "order_tags"}, got[2].Via, "junction payload columns are in scope")
	require.Nil(t, Scope(&f.Spec, "no_such_field"))
}
