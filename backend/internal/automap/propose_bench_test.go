package automap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/race"
)

// bigProposal is bigSource as a design-first input: the driving table is chosen too.
func bigProposal() ProposeInput {
	in, spec := bigSource()
	return ProposeInput{Spec: spec, Tables: in.Tables, Table: in.Table, Links: in.Links}
}

func BenchmarkPropose_AC4(b *testing.B) {
	in := bigProposal()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Propose(in)
	}
}

// OC-163 AC4: the whole proposal on 300 tables and a 200-container design takes under 2 s.
func TestPropose_AC4_Budget(t *testing.T) {
	in := bigProposal()
	p := Propose(in)
	require.Equal(t, "hub", p.Driving[0].Table.Name)
	require.Equal(t, 200, p.Coverage.Containers)
	require.Equal(t, 200, p.Coverage.Bound)
	if race.Enabled || testing.CoverMode() != "" {
		t.Skip("timing budgets need uninstrumented code: run `make test-budgets`")
	}
	elapsed := time.Duration(1<<62 - 1)
	for range 3 {
		start := time.Now()
		Propose(in)
		elapsed = min(elapsed, time.Since(start))
	}
	t.Logf("Propose: %s", elapsed)
	require.Less(t, elapsed, 2*time.Second, "300 tables × 200 containers")
}
