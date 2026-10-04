package source_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
)

func TestFlatRelated(t *testing.T) {
	require.Nil(t, source.FlatRelated(nil))

	rel := source.FlatRelated(map[string][][]any{
		"orders": {{1, "a"}, {2, "b"}},
		"notes":  {},
	})
	require.Len(t, rel, 2)
	require.Equal(t, 2, rel["orders"].Len())
	require.Equal(t, []any{2, "b"}, rel["orders"].Rows[1].Values)
	require.Nil(t, rel["orders"].Rows[0].Sub, "flat rows have no nested relations")
	require.Zero(t, rel["notes"].Len())
}

func TestRelatedSet_LenOfNil(t *testing.T) {
	var s *source.RelatedSet
	require.Zero(t, s.Len())
	require.Zero(t, source.Related{}["missing"].Len(), "a relation with no rows reads as empty")
}
