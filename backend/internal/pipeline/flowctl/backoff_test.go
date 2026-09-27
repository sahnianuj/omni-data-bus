package flowctl

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBackoff_AC4_JitterBounds(t *testing.T) {
	b := Backoff{Base: 50 * time.Millisecond, Factor: 2, Cap: 5 * time.Second}
	want := []time.Duration{50, 100, 200, 400, 800, 1600, 3200, 5000, 5000}
	for i, w := range want {
		require.Equal(t, w*time.Millisecond, b.Max(i+1), "attempt %d", i+1)
	}
	for attempt := 1; attempt <= 9; attempt++ {
		for range 200 {
			d := b.Delay(attempt)
			require.GreaterOrEqual(t, d, time.Duration(0))
			require.LessOrEqual(t, d, b.Max(attempt))
		}
	}
	b.Rand = func() float64 { return 0.5 }
	require.Equal(t, 25*time.Millisecond, b.Delay(1), "full jitter scales the whole window")
}

func TestTokenBucket_RateOverTime(t *testing.T) {
	clock := NewFakeClock()
	b := NewTokenBucket(1000, 100, clock)
	start := clock.Now()
	taken := 0
	for clock.Now().Sub(start) < 5*time.Second {
		require.NoError(t, b.WaitN(context.Background(), 100))
		taken += 100
	}
	// 5 s at 1000/s plus the initial burst.
	require.LessOrEqual(t, taken, 5100+100)
	require.GreaterOrEqual(t, taken, 4900)
}
