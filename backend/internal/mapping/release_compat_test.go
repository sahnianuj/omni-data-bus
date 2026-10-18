package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
)

func TestRequiresV2_OC057(t *testing.T) {
	require.Empty(t, RequiresV2(&Spec{Mode: ModeCanvas}), "a flat canvas mapping runs on 1.0")
	require.Equal(t, []string{"relations"}, RequiresV2(&Spec{Mode: ModeCanvas, Relations: []Relation{{ID: "e1"}}}), "even a 1.0-style embed: its frozen spec is v2")
	s := &Spec{Mode: ModeComposed, Root: &Root{GroupBy: []string{"order_id"}}, Aggregates: []Aggregate{{}}}
	require.Equal(t, []string{"composed document", "grouped rows", "aggregates"}, RequiresV2(s))
}

// OC-140 AC2: a mapping with a computed field needs a 2.0 worker; a pending one doesn't count.
func TestRequiresComputed_AC2_MinWorkerLevel(t *testing.T) {
	s := &Spec{Mode: ModeCanvas, FieldMappings: []FieldMapping{{ID: "c", TargetFieldID: "f", State: StatePending, Computed: &Computed{Lang: ComputedTemplate, Expr: "x"}}}}
	require.Empty(t, RequiresV2(s))
	s.FieldMappings[0].State = StateActive
	require.Equal(t, []string{"computed fields"}, RequiresV2(s))
}

// OC-171: variants need a 2.5 worker: a 2.0 one would write their items without their fields.
func TestRequiresV25_OC171(t *testing.T) {
	plain := &Spec{DocSchema: docschema.Schema{Root: docschema.NewRoot()}}
	require.Nil(t, RequiresV25(plain))
	withRule := &Spec{DocSchema: docschema.Schema{Root: docschema.NewRoot()}, Relations: []Relation{{ID: "r", Variants: []RelationVariant{{FieldID: "v", When: "scc_id"}}}}}
	require.Equal(t, []string{"variant shapes"}, RequiresV25(withRule))
	union := &Spec{DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
		{ID: "a", Name: "activity", Type: docschema.Array, Items: &docschema.DocField{ID: "i", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{{ID: "c", Name: "comment", Type: docschema.Object, Variant: &docschema.Variant{Title: "Comment"}}}}},
	}}}}
	require.Equal(t, []string{"variant shapes"}, RequiresV25(union))
}

// OC-179: a now step needs a 2.5 worker (a 2.0 one doesn't know it).
func TestRequiresV25_OC179_Now(t *testing.T) {
	s := baseSpec()
	require.Nil(t, RequiresV25(&s))
	s.FieldMappings = append(s.FieldMappings, FieldMapping{ID: "m_now", TargetFieldID: "f_age", Sources: []SourceRef{}, Transforms: []Step{{Op: "now", Args: map[string]any{"format": "epoch_ms"}}}, Origin: OriginManual, State: StateActive})
	require.Equal(t, []string{"current date and time"}, RequiresV25(&s))
}
