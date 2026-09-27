package automap

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
)

type corpusCase struct {
	Table   string `json:"table"`
	Columns []struct {
		Name     string `json:"name"`
		JSONType string `json:"jsonType"`
	} `json:"columns"`
	Fields []struct {
		Name string              `json:"name"`
		Type docschema.FieldType `json:"type"`
	} `json:"fields"`
}

// hungarian returns the maximum-weight assignment of an n×m score matrix (0 = no edge) as
// row → column (-1 when unassigned). O(n³) with n = max(rows, cols).
func hungarian(w [][]float64) []int {
	n := len(w)
	m := 0
	if n > 0 {
		m = len(w[0])
	}
	size := max(n, m)
	cost := make([][]float64, size+1)
	for i := range cost {
		cost[i] = make([]float64, size+1)
	}
	for i := 0; i < n; i++ {
		for j := 0; j < m; j++ {
			cost[i+1][j+1] = -w[i][j]
		}
	}
	u, v := make([]float64, size+1), make([]float64, size+1)
	p, way := make([]int, size+1), make([]int, size+1)
	for i := 1; i <= size; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, size+1)
		used := make([]bool, size+1)
		for j := range minv {
			minv[j] = math.Inf(1)
		}
		for {
			used[j0] = true
			i0, delta, j1 := p[j0], math.Inf(1), 0
			for j := 1; j <= size; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0][j] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j], way[j] = cur, j0
				}
				if minv[j] < delta {
					delta, j1 = minv[j], j
				}
			}
			for j := 0; j <= size; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}
	out := make([]int, n)
	for i := range out {
		out[i] = -1
	}
	for j := 1; j <= size; j++ {
		if p[j] >= 1 && p[j] <= n && j <= m && w[p[j]-1][j-1] > 0 {
			out[p[j]-1] = j - 1
		}
	}
	return out
}

func TestAutomap_AC4_GreedyMatchesHungarianOnCorpus(t *testing.T) {
	files, err := filepath.Glob("testdata/corpus/*.json")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(files), 20)
	d := DefaultDicts()
	agree := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		var c corpusCase
		require.NoError(t, json.Unmarshal(raw, &c))
		var fields []docschema.DocField
		for i, fl := range c.Fields {
			fields = append(fields, docschema.DocField{ID: fmt.Sprintf("f%d", i), Name: fl.Name, Type: fl.Type})
		}
		spec := specWith(fields...)
		var cols []SourceCol
		for i, cl := range c.Columns {
			cols = append(cols, SourceCol{Name: cl.Name, JSONType: cl.JSONType, Ordinal: i + 1, Table: c.Table})
		}
		leaves := Leaves(&spec.DocSchema)
		// The score matrix, as the engine scores pairs.
		w := make([][]float64, len(cols))
		for i, cl := range cols {
			w[i] = make([]float64, len(leaves))
			for j, l := range leaves {
				class, _ := mapping.Compat(cl.JSONType, l.Type)
				ts := mapping.TypeScore(class)
				if ts == 0 {
					continue
				}
				ns, _ := d.NameScore(cl, l)
				if s := ns * (0.8 + 0.2*ts); s >= DefaultSuggestThreshold {
					w[i][j] = s
				}
			}
		}
		opt := hungarian(w)
		var want []string
		for i, j := range opt {
			if j >= 0 {
				want = append(want, cols[i].Name+"→"+leaves[j].FieldID)
			}
		}
		var got []string
		for _, m := range Run(spec, cols, Options{}).FieldMappings {
			got = append(got, m.Sources[0].Column+"→"+m.TargetFieldID)
		}
		sort.Strings(want)
		sort.Strings(got)
		if fmt.Sprint(want) == fmt.Sprint(got) {
			agree++
		} else {
			t.Logf("%s: greedy %v, optimal %v", filepath.Base(f), got, want)
		}
	}
	ratio := float64(agree) / float64(len(files))
	t.Logf("greedy matches Hungarian on %d/%d cases (%.0f%%)", agree, len(files), ratio*100)
	require.GreaterOrEqual(t, ratio, 0.95)
}

func TestHungarian(t *testing.T) {
	require.Equal(t, []int{1, 0}, hungarian([][]float64{{0.9, 1.0}, {0.8, 0}}), "total 1.8 beats 1.0 + nothing")
	require.Equal(t, []int{-1}, hungarian([][]float64{{0}}))
	require.Equal(t, []int{0, -1}, hungarian([][]float64{{1}, {0.5}}))
}
