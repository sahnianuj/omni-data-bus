package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"
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
