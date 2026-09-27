package verify

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/race"
)

func TestReservoir_AC2_SizeAndUniformity(t *testing.T) {
	r := NewReservoir(100, rand.New(rand.NewPCG(1, 2)))
	for i := 0; i < 1_000_000; i++ {
		r.Add(Sample{DocKey: "k", SourceKey: []any{i}})
	}
	require.Len(t, r.Items(), 100, "exactly N retained")
	require.EqualValues(t, 1_000_000, r.Seen())

	// Uniformity: over many runs, each of 20 buckets of the stream is picked equally often.
	const stream, buckets = 2000, 20
	runs := 2000
	if race.Enabled {
		runs = 400
	}
	counts := make([]float64, buckets)
	rng := rand.New(rand.NewPCG(3, 4))
	for run := 0; run < runs; run++ {
		r := NewReservoir(10, rng)
		for i := 0; i < stream; i++ {
			r.Add(Sample{SourceKey: []any{i}})
		}
		for _, s := range r.Items() {
			counts[s.SourceKey[0].(int)*buckets/stream]++
		}
	}
	expected := float64(runs*10) / buckets
	var chi2 float64
	for _, c := range counts {
		chi2 += (c - expected) * (c - expected) / expected
	}
	// 19 degrees of freedom: the p = 0.01 critical value is 36.19.
	require.Less(t, chi2, 36.19, "chi-square %.2f", chi2)

	small := NewReservoir(5, nil)
	small.Add(Sample{DocKey: "a"})
	require.Len(t, small.Items(), 1, "fewer items than N: all kept")
}
