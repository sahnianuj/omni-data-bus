package flowctl

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAIMD_AC6_DecreaseAndRecover(t *testing.T) {
	clock := NewFakeClock()
	a := NewAIMD(64, clock)
	var changes []int
	a.OnChange = func(l int, _ string) { changes = append(changes, l) }

	// 5 s with 10% temporary failures: the limit halves at the window's end.
	for i := 0; i < 1000; i++ {
		a.Observe(i%10 == 0)
		clock.Advance(5 * time.Millisecond)
	}
	a.Observe(false)
	require.Equal(t, 32, a.Limit())

	// 5 s clean: +4.
	for i := 0; i < 1000; i++ {
		a.Observe(false)
		clock.Advance(5 * time.Millisecond)
	}
	a.Observe(false)
	require.Equal(t, 36, a.Limit())
	require.Equal(t, []int{32, 36}, changes)

	// 3% busy is under the threshold: no halving (and no growth either).
	for i := 0; i < 1000; i++ {
		a.Observe(i%33 == 0)
		clock.Advance(5 * time.Millisecond)
	}
	a.Observe(false)
	require.Equal(t, 36, a.Limit())

	// Never below 1, never above max.
	b := NewAIMD(2, clock)
	for range 4 {
		for range 10 {
			b.Observe(true)
		}
		clock.Advance(6 * time.Second)
		b.Observe(true)
	}
	require.Equal(t, 1, b.Limit())
	for range 10 {
		clock.Advance(6 * time.Second)
		b.Observe(false)
	}
	require.Equal(t, 2, b.Limit())
}

func TestGate_BoundsInflight(t *testing.T) {
	limit := 8
	g := NewGate(func() int { return limit })
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, g.Acquire(context.Background()))
			time.Sleep(100 * time.Microsecond)
			g.Release()
		}()
	}
	wg.Wait()
	require.LessOrEqual(t, g.MaxSeen, 8)
	require.Equal(t, 0, g.InFlight())

	// A cancelled context stops waiting.
	limit = 1
	require.NoError(t, g.Acquire(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, g.Acquire(ctx), context.DeadlineExceeded)
}
