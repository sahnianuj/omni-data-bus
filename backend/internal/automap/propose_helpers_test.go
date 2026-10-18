package automap

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// Unit tests of the proposal's small helpers (OC-163–OC-171); the whole proposal is covered
// by propose_test.go and, against the point-of-sale source, the integration tests.

func TestMarkUnder(t *testing.T) {
	f := docschema.DocField{ID: "a", Children: []docschema.DocField{{ID: "b"}, {ID: "c", Items: &docschema.DocField{ID: "d"}}}}
	out := map[string]bool{}
	markUnder(&f, out)
	require.Equal(t, map[string]bool{"a": true, "b": true, "c": true, "d": true}, out)
}

func TestTop3_BestFirstWithoutDuplicates(t *testing.T) {
	require.Equal(t, []string{"c", "a", "d"}, top3([]scoredName{{"a", 0.5}, {"c", 0.9}, {"a", 0.4}, {"d", 0.3}, {"b", 0.1}}))
	require.Equal(t, []string{"x"}, top3([]scoredName{{"x", 1}, {"x", 0.5}}))
	require.Empty(t, top3(nil))
}

func TestLiteral_ParsesByFieldType(t *testing.T) {
	for _, c := range []struct {
		s    string
		t    docschema.FieldType
		want any
		ok   bool
	}{
		{"42", docschema.Integer, int64(42), true},
		{"x", docschema.Integer, nil, false},
		{"1.5", docschema.Number, 1.5, true},
		{"x", docschema.Number, nil, false},
		{"TRUE", docschema.Boolean, true, true},
		{"false", docschema.Boolean, false, true},
		{"maybe", docschema.Boolean, nil, false},
		{`"cafe"`, docschema.String, "cafe", true},
		{"{}", docschema.Object, nil, false},
	} {
		got, ok := literal(c.s, c.t)
		require.Equal(t, c.ok, ok, "%s as %s", c.s, c.t)
		require.Equal(t, c.want, got, "%s as %s", c.s, c.t)
	}
}

// A container nothing fills: its fields are unresolved, except those already decided or the
// design leaves empty, and a nested container the design leaves empty is not populated as a
// whole (OC-166, OC-169).
func TestWalkUnresolved_NotPopulatedAndNested(t *testing.T) {
	root := docschema.DocField{ID: "loyalty", Name: "loyalty", Type: docschema.Object, Children: []docschema.DocField{
		{ID: "points", Name: "points", Type: docschema.Integer},
		{ID: "tier", Name: "tier", Type: docschema.String, Description: "Not populated."},
		{ID: "kept", Name: "kept", Type: docschema.String},
		{ID: "history", Name: "history", Type: docschema.Array, Items: &docschema.DocField{ID: "h", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "at", Name: "at", Type: docschema.Datetime},
		}}},
		{ID: "legacy", Name: "legacy", Type: docschema.Object, Description: "Not populated.", Children: []docschema.DocField{
			{ID: "code", Name: "code", Type: docschema.String},
			{ID: "old", Name: "old", Type: docschema.Object, Children: []docschema.DocField{{ID: "x", Name: "x", Type: docschema.String}}},
		}},
	}}
	spec := &mapping.Spec{}
	p := &proposer{spec: spec, itemOf: map[string]int{}, mapped: map[string]bool{"kept": true}, paths: map[string]string{
		"points": "loyalty.points", "tier": "loyalty.tier", "kept": "loyalty.kept", "history": "loyalty.history", "at": "loyalty.history[].at",
		"legacy": "loyalty.legacy", "code": "loyalty.legacy.code", "old": "loyalty.legacy.old", "x": "loyalty.legacy.old.x",
	}}
	p.walkUnresolved(&root, nil)
	status := map[string]string{}
	for _, it := range p.items {
		status[it.Path] = it.Status
	}
	require.Equal(t, StatusUnresolved, status["loyalty.points"])
	require.Equal(t, StatusNotPopulated, status["loyalty.tier"])
	require.NotContains(t, status, "loyalty.kept", "already decided")
	require.Equal(t, StatusUnresolved, status["loyalty.history[]"])
	require.Equal(t, StatusUnresolved, status["loyalty.history[].at"])
	require.Equal(t, StatusNotPopulated, status["loyalty.legacy"])
	require.Equal(t, StatusNotPopulated, status["loyalty.legacy.code"])
	require.Equal(t, StatusNotPopulated, status["loyalty.legacy.old"])
	require.Equal(t, StatusNotPopulated, status["loyalty.legacy.old.x"])

	// A variant nothing fills says why, and so does everything in it.
	p.unresolvedVariant(variantPart{v: &docschema.DocField{ID: "v", Type: docschema.Object, Children: []docschema.DocField{{ID: "vf", Name: "vf", Type: docschema.String}}}}, "no table")
	require.Equal(t, []string{"no table"}, p.items[p.itemOf["v"]].Reasons)
	require.Equal(t, StatusUnresolved, p.items[p.itemOf["vf"]].Status)
}

// A union's items are ordered by a time column every one of its tables has.
func TestCommonOrder(t *testing.T) {
	tables := map[string]*schema.Table{
		"pos.cash": {Name: "cash", Columns: []schema.Column{{Name: "id"}, {Name: "created_at"}, {Name: "at"}}},
		"pos.card": {Name: "card", Columns: []schema.Column{{Name: "id"}, {Name: "at"}}},
	}
	p := &proposer{in: ProposeInput{Table: func(r mapping.TableRef) *schema.Table { return tables[r.String()] }}}
	link := func(n string) *Link { return &Link{Table: mapping.TableRef{Schema: "pos", Name: n}} }
	require.Equal(t, "at", commonOrder(p, variantPlan{parts: []variantPart{{union: link("cash")}, {union: link("card")}, {}}}))
	require.Equal(t, "created_at", commonOrder(p, variantPlan{parts: []variantPart{{union: link("cash")}}}))
	require.Empty(t, commonOrder(p, variantPlan{parts: []variantPart{{union: link("card")}, {union: link("missing")}}}))
}
