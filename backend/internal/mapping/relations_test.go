package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func codes(is []Issue) []string {
	out := []string{}
	for _, i := range is {
		out = append(out, i.Code)
	}
	return out
}

func TestRelationTree_OrderDepthAndErrors(t *testing.T) {
	s := &Spec{Relations: []Relation{
		{ID: "b", ParentID: sptr("a"), Kind: EmbedManyToOne},
		{ID: "a", Kind: EmbedOneToMany},
		{ID: "c", Kind: EmbedManyToOne},
	}}
	order, depth, is := Depths(s)
	require.Nil(t, is)
	require.Equal(t, []string{"a", "c", "b"}, order, "parents first, then by id")
	require.Equal(t, map[string]int{"a": 1, "b": 2, "c": 1}, depth)

	s.Relations[1].ParentID = sptr("b") // a → b → a
	_, _, is = Depths(s)
	require.Equal(t, CodeRelationCycle, is.Code)

	s.Relations[1].ParentID = sptr("nope")
	require.Equal(t, []string{CodeRelationParentMissing}, codes(ValidateRelationTree(s)))
}

func TestRelationTree_DepthLimit(t *testing.T) {
	defer SetMaxRelationDepth(DefaultMaxRelationDepth)
	s := &Spec{Relations: []Relation{{ID: "r1"}, {ID: "r2", ParentID: sptr("r1")}, {ID: "r3", ParentID: sptr("r2")}}}
	SetMaxRelationDepth(2)
	require.Contains(t, codes(ValidateRelationTree(s)), CodeRelationTooDeep)
	SetMaxRelationDepth(99)
	require.Equal(t, MaxRelationDepthCap, MaxRelationDepth(), "clamped to the hard cap")
	SetMaxRelationDepth(0)
	require.Equal(t, 1, MaxRelationDepth())
}

func sptr(s string) *string { return &s }
