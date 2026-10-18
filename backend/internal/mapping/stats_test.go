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
	require.InDelta(t, want.DoneRatio, got.DoneRatio, 1e-9, msg)
	want.AutoMapRatio, want.CoverageRatio, want.AverageConfidence, want.DoneRatio = 0, 0, 0, 0
	got.AutoMapRatio, got.CoverageRatio, got.AverageConfidence, got.DoneRatio = 0, 0, 0, 0
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

// A grouped document reads its rows through a relation to its own table: those columns are the
// table's, so "one document per order" with every line column mapped is fully covered.
func TestComputeStats_GroupedRowsAreOwnColumns(t *testing.T) {
	s, _ := groupedSpec()
	cols := []string{"type", "order_id", "line_no", "quantity"}
	st := ComputeStatsWith(s, cols, map[string][]string{"rel_lines": cols})
	require.Equal(t, 4, st.TotalSourceColumns)
	require.Equal(t, 4, st.ManuallyMapped)
	require.Equal(t, 1.0, st.CoverageRatio)
	require.Equal(t, 0, st.Embedded.Tables)
	require.Equal(t, 0, st.Embedded.TotalColumns)

	// Auto-mapped lines count as auto.
	for i := range s.FieldMappings {
		if s.FieldMappings[i].ID == "m_line" || s.FieldMappings[i].ID == "m_qty" {
			s.FieldMappings[i].Origin = OriginAuto
		}
	}
	require.Equal(t, 0.5, ComputeStats(s, cols).AutoMapRatio)
}

// OC-178 AC1: every field of the document counts, array items and variants included; filled
// (a mapping, a total, a relation) or not populated is done; a pending suggestion isn't.
func TestComputeStats_OC178_FieldsDone(t *testing.T) {
	cs := statsCases(t)
	c := cs[len(cs)-1]
	st := ComputeStatsWith(&c.Spec, c.Columns, c.Embedded)
	require.Equal(t, 10, st.TargetFields)
	require.Equal(t, 7, st.TargetFieldsDone)
	require.InDelta(t, 0.7, st.DoneRatio, 1e-9)
	// Marking the pending and the empty fields not populated finishes the document.
	c.Spec.FieldMappings = append(c.Spec.FieldMappings,
		FieldMapping{ID: "np1", TargetFieldID: "f_pending", State: StateNotPopulated, Origin: OriginManual},
		FieldMapping{ID: "np2", TargetFieldID: "f_qty", State: StateNotPopulated, Origin: OriginManual},
		FieldMapping{ID: "np3", TargetFieldID: "f_empty", State: StateNotPopulated, Origin: OriginManual})
	st = ComputeStatsWith(&c.Spec, c.Columns, c.Embedded)
	require.Equal(t, st.TargetFields, st.TargetFieldsDone)
	require.InDelta(t, 1.0, st.DoneRatio, 1e-9)
}

// OC-187 AC1: done splits into filled with data and left empty on purpose; a field under a
// not-populated container counts as not populated, unless something fills it.
func TestStats_OC187_AC1_FilledAndNotPopulated(t *testing.T) {
	cs := statsCases(t)
	c := cs[len(cs)-1]
	st := ComputeStatsWith(&c.Spec, c.Columns, c.Embedded)
	require.Equal(t, 5, st.TargetFieldsFilled)
	require.Equal(t, 2, st.TargetFieldsNotPopulated, "the not-populated object's two fields")
	require.Equal(t, st.TargetFieldsDone, st.TargetFieldsFilled+st.TargetFieldsNotPopulated)
	// Leaving the pending field empty moves it to not populated, not filled.
	c.Spec.FieldMappings = append(c.Spec.FieldMappings, FieldMapping{ID: "np1", TargetFieldID: "f_pending", State: StateNotPopulated, Origin: OriginManual})
	st = ComputeStatsWith(&c.Spec, c.Columns, c.Embedded)
	require.Equal(t, 5, st.TargetFieldsFilled)
	require.Equal(t, 3, st.TargetFieldsNotPopulated)
}
