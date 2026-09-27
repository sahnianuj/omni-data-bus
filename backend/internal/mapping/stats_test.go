package mapping

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type statsCase struct {
	Name     string              `json:"name"`
	Columns  []string            `json:"columns"`
	Embedded map[string][]string `json:"embedded"`
	Spec     Spec                `json:"spec"`
	Expected Stats               `json:"expected"`
}

func statsCases(t *testing.T) []statsCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/stats-cases.json")
	require.NoError(t, err)
	var f struct {
		Cases []statsCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	return f.Cases
}

func requireStats(t *testing.T, want, got Stats, msg string) {
	t.Helper()
	require.InDelta(t, want.AutoMapRatio, got.AutoMapRatio, 1e-9, msg)
	require.InDelta(t, want.CoverageRatio, got.CoverageRatio, 1e-9, msg)
	require.InDelta(t, want.AverageConfidence, got.AverageConfidence, 1e-9, msg)
	want.AutoMapRatio, want.CoverageRatio, want.AverageConfidence = 0, 0, 0
	got.AutoMapRatio, got.CoverageRatio, got.AverageConfidence = 0, 0, 0
	require.Equal(t, want, got, msg)
}

func TestComputeStats_AC6_Counts(t *testing.T) {
	c := statsCases(t)[0]
	got := ComputeStats(&c.Spec, c.Columns)
	requireStats(t, c.Expected, got, c.Name)
	require.InDelta(t, 0.4545, got.AutoMapRatio, 0.0001)
}

func TestComputeStats_AC6_AcceptedSuggestionCountsAsManual(t *testing.T) {
	c := statsCases(t)[1]
	requireStats(t, c.Expected, ComputeStats(&c.Spec, c.Columns), c.Name)
}

func TestComputeStats_AC6_IgnoredExcludedFromDenominator(t *testing.T) {
	for _, c := range statsCases(t) {
		requireStats(t, c.Expected, ComputeStatsWith(&c.Spec, c.Columns, c.Embedded), c.Name)
	}
	c := statsCases(t)[0]
	c.Spec.IgnoredColumns = append(c.Spec.IgnoredColumns, "c20")
	st := ComputeStats(&c.Spec, c.Columns)
	require.Equal(t, 21, st.TotalSourceColumns)
	require.Equal(t, 2, st.Ignored)
}

func TestComputeStats_EmbeddedGroupParity(t *testing.T) {
	c := statsCases(t)[3]
	got := ComputeStatsWith(&c.Spec, c.Columns, c.Embedded)
	requireStats(t, c.Expected, got, c.Name)
}
