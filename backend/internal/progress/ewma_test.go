package progress

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEWMA_AC2_ConvergesSteadyRate(t *testing.T) {
	c := newClock()
	tr := tracker(c, 100_000_000)
	for i := 0; i < 30; i++ {
		c.add(time.Second)
		tr.Acked("m0", 20_000, 0, 0, 0)
		_ = tr.Snapshot("running")
	}
	s := tr.Snapshot("running")
	require.InDelta(t, 20_000, s.Throughput, 20_000*0.02)
	require.InDelta(t, 20_000, s.PeakThroughput, 1, "peak is the highest measured rate")
	require.NotNil(t, s.EtaSeconds)
	require.InDelta(t, float64(s.Total-s.Processed)/20_000, float64(*s.EtaSeconds), 1)

	// Half-life: after ~10 samples at a new rate, the average is halfway there.
	e := NewEWMA(10)
	e.Update(0)
	for i := 0; i < 10; i++ {
		e.Update(100)
	}
	require.InDelta(t, 50, e.Value(), 1)
}
