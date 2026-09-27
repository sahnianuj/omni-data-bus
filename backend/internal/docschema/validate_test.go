package docschema

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

func leaf(name string, t FieldType) DocField { return DocField{ID: NewID(), Name: name, Type: t} }

func codes(issues []Issue) []string {
	out := []string{}
	for _, i := range issues {
		out = append(out, i.Code+"@"+i.Path)
	}
	return out
}

func TestValidate_AC5_Table(t *testing.T) {
	deep := leaf("x", String)
	for i := 0; i < MaxDepth; i++ {
		deep = DocField{ID: NewID(), Name: fmt.Sprintf("l%d", i), Type: Object, Children: []DocField{deep}}
	}
	many := make([]DocField, MaxFields+1)
	for i := range many {
		many[i] = leaf(fmt.Sprintf("f%d", i), String)
	}
	cases := []struct {
		name     string
		children []DocField
		want     []string
	}{
		{"duplicate siblings", []DocField{leaf("a", String), leaf("a", Integer)}, []string{"DUPLICATE_NAME@a"}},
		{"dot in name", []DocField{leaf("a.b", String)}, []string{"INVALID_NAME@a.b"}},
		{"bracket in name", []DocField{leaf("a[0]", String)}, []string{"INVALID_NAME@a[0]"}},
		{"empty name", []DocField{leaf("", String)}, []string{"INVALID_NAME@"}},
		{"depth 17", []DocField{deep}, []string{"TOO_DEEP@l15.l14.l13.l12.l11.l10.l9.l8.l7.l6.l5.l4.l3.l2.l1.l0.x"}},
		{"array without items", []DocField{leaf("tags", Array)}, []string{"ARRAY_WITHOUT_ITEMS@tags"}},
		{"object with items", []DocField{{ID: NewID(), Name: "o", Type: Object, Items: &DocField{ID: NewID(), Name: "[]", Type: String}}}, []string{"OBJECT_WITH_ITEMS@o"}},
		{"unknown type", []DocField{leaf("x", "date")}, []string{"INVALID_TYPE@x"}},
		{"too many fields", many, []string{"TOO_MANY_FIELDS@$"}},
		{"valid nested", []DocField{{ID: NewID(), Name: "tags", Type: Array, Items: &DocField{ID: NewID(), Name: "[]", Type: String}}}, []string{}},
	}
	for _, c := range cases {
		s := Schema{Root: DocField{ID: RootID, Name: RootName, Type: Object, Children: c.children}}
		require.Equal(t, c.want, codes(Validate(&s)), c.name)
	}
	bad := Schema{Root: DocField{ID: RootID, Type: String}}
	require.Equal(t, []string{"ROOT_NOT_OBJECT@$"}, codes(Validate(&bad)))
	dupID := Schema{Root: DocField{ID: RootID, Type: Object, Children: []DocField{{ID: "x", Name: "a", Type: String}, {ID: "x", Name: "b", Type: String}}}}
	require.Equal(t, []string{"DUPLICATE_ID@b"}, codes(Validate(&dupID)))
	p := Problem(Validate(&dupID))
	require.Equal(t, problem.CodeValidation, p.Code)
	require.Equal(t, "b", p.FieldErrors[0].Field)
}

func TestSchema_PathsLeavesAndFind(t *testing.T) {
	s := FromTable(customerTable(), GenerateOptions{NestByPrefix: true})
	s.Root.Children = append(s.Root.Children,
		DocField{ID: "fld_orders", Name: "orders", Type: Array, Items: &DocField{ID: "fld_item", Name: "[]", Type: Object, Children: []DocField{{ID: "fld_sku", Name: "sku", Type: String}}}},
		DocField{ID: "fld_raw", Name: "raw", Type: Object, Children: []DocField{}},
	)
	paths := s.Paths()
	require.Equal(t, "billing.street", paths[s.Root.Children[2].Children[0].ID])
	require.Equal(t, "orders[].sku", paths["fld_sku"])
	require.Equal(t, "orders[]", paths["fld_item"])
	var leafPaths []string
	for _, l := range s.Leaves() {
		leafPaths = append(leafPaths, l.Path)
	}
	require.Equal(t, []string{"customerId", "firstName", "billing.street", "billing.city", "createdAt", "tags", "externalRef", "raw"}, leafPaths,
		"arrays of objects are embeds, empty objects are leaves")
	require.Equal(t, []string{"billing"}, s.Leaves()[2].ParentNames)
	require.Equal(t, String, s.Leaves()[5].ItemType)
	require.True(t, s.Leaves()[7].Empty)
	require.Equal(t, "sku", s.Find("fld_sku").Name)
	require.Nil(t, s.Find("nope"))
	require.Equal(t, 13, s.Count())
	a, b := NewID(), NewID()
	require.NotEqual(t, a, b)
	require.Len(t, NewID(), 14)
}
