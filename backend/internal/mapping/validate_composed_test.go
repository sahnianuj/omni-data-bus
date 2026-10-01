package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
)

// One out-of-scope mapping and one object bound to one-to-many without an adapter: both are
// errors on their own nodes (OC-049 AC3).
func TestValidate_AC3_ComposedIssues(t *testing.T) {
	s, tables := composedShop()
	// customer becomes an object bound to order_items (one-to-many) without the first adapter.
	s.Relations = append(s.Relations, Relation{
		ID: "rel_last", Kind: EmbedOneToMany, Table: TableRef{Schema: "public", Name: "order_items"},
		Join: []JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetFieldID: "f_lastline",
	})
	s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, docschema.DocField{ID: "f_lastline", Name: "lastLine", Type: docschema.Object, Children: []docschema.DocField{
		{ID: "f_last_no", Name: "lineNo", Type: docschema.Integer},
	}})
	// items[].lineNo reads the customer's name: customers is bound on a sibling node.
	s.FieldMappings[1].Sources = []SourceRef{{Relation: "rel_cust", Column: "name"}}
	s.Normalize()

	issues := Validate(s, contextFor(s, tables))
	Annotate(s, issues)
	var scope, kind *Issue
	for i := range issues {
		switch issues[i].Code {
		case CodeSourceOutOfScope:
			scope = &issues[i]
		case CodeBindingKindMismatch:
			kind = &issues[i]
		}
	}
	require.NotNil(t, scope, "%v", issues)
	require.Equal(t, SeverityError, scope.Severity)
	require.Equal(t, "items[].lineNo", scope.NodePath)
	require.Equal(t, "f_line", scope.FieldID)
	require.Equal(t, "items[].lineNo reads customers.name, but customers isn't bound on the path to this field; bind it under items or add a lookup.", scope.Message)

	require.NotNil(t, kind, "%v", issues)
	require.Equal(t, SeverityError, kind.Severity)
	require.Equal(t, "lastLine", kind.NodePath)
	require.Equal(t, "rel_last", kind.RelationID)
	require.False(t, Ready(issues))
}

func TestAnnotate_RelationWithoutField(t *testing.T) {
	s, _ := composedShop()
	issues := []Issue{{Code: CodeEmbedInvalid, Path: "relations.rel_x"}, {Code: CodeKeyTemplateMissing, Path: "key"}, {Code: "X", Path: "items[].lineNo"}}
	Annotate(s, issues)
	require.Equal(t, "rel_x", issues[0].RelationID)
	require.Empty(t, issues[1].NodePath)
	require.Equal(t, "items[].lineNo", issues[2].NodePath)
}
