package progress

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestETA_AC2_NullWarmupAndSmoothing(t *testing.T) {
	var e ETA
	require.Nil(t, e.Next(100_000, 1000, 4*time.Second), "null in the first 5 s")
	require.Nil(t, e.Next(100_000, 0, 10*time.Second), "null without a rate")
	first := e.Next(100_000, 1000, 6*time.Second)
	require.EqualValues(t, 100, *first)
	// The rate drops 10%: the raw ETA would jump to 111 s, it moves at most 20% (to ≤ 120).
	next := e.Next(100_000, 900, 7*time.Second)
	require.EqualValues(t, 111, *next)
	// A small rate change can't make it jump more than 20% per tick.
	e2 := ETA{}
	e2.Next(10_000, 1000, 6*time.Second)          // 10 s
	capped := e2.Next(10_000, 700, 7*time.Second) // raw 14.3 s, rate −30%
	require.EqualValues(t, 12, *capped, "capped at +20%")
	// A big change (> 50%) is followed at once.
	jumped := e2.Next(10_000, 200, 8*time.Second)
	require.EqualValues(t, 50, *jumped)
}
