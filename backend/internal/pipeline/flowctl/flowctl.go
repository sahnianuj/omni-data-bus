// Package flowctl holds the pipeline's flow-control primitives: retry backoff, a token-bucket
// throttle and AIMD concurrency. Clocks are injectable so tests run in simulated time.
package flowctl

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

// Clock is the time source (Real, or a fake in tests).
type Clock interface {
	Now() time.Time
	// Sleep waits d or until ctx is done.
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

// Real is the wall clock.
var Real Clock = realClock{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Backoff is exponential backoff with full jitter: attempt n (1-based) waits a random time
// in [0, min(Cap, Base·Factor^(n-1))].
type Backoff struct {
	Base   time.Duration
	Factor float64
	Cap    time.Duration
	// Rand returns a float in [0, 1); nil uses math/rand.
	Rand func() float64
}

// Max is the upper bound of attempt n's delay.
func (b Backoff) Max(attempt int) time.Duration {
	d := float64(b.Base)
	for i := 1; i < attempt; i++ {
		d *= b.Factor
		if d >= float64(b.Cap) {
			return b.Cap
		}
	}
	return min(time.Duration(d), b.Cap)
}

// Delay is attempt n's jittered delay.
func (b Backoff) Delay(attempt int) time.Duration {
	r := b.Rand
	if r == nil {
		r = rand.Float64 //nolint:gosec // jitter, not security
	}
	return time.Duration(r() * float64(b.Max(attempt)))
}

// TokenBucket throttles to Rate tokens per second with a burst of Burst tokens.
type TokenBucket struct {
	mu     sync.Mutex
	clock  Clock
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

// NewTokenBucket returns a full bucket.
func NewTokenBucket(rate float64, burst int, clock Clock) *TokenBucket {
	if clock == nil {
		clock = Real
	}
	return &TokenBucket{clock: clock, rate: rate, burst: float64(burst), tokens: float64(burst), last: clock.Now()}
}

// WaitN blocks until n tokens are available (n may exceed the burst: it then waits for the
// deficit), then takes them.
func (b *TokenBucket) WaitN(ctx context.Context, n int) error {
	b.mu.Lock()
	now := b.clock.Now()
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	b.tokens -= float64(n)
	deficit := -b.tokens
	b.mu.Unlock()
	if deficit <= 0 {
		return nil
	}
	return b.clock.Sleep(ctx, time.Duration(deficit/b.rate*float64(time.Second)))
}
