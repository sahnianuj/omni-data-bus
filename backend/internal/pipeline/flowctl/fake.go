package flowctl

import (
	"context"
	"sync"
	"time"
)

// FakeClock is a simulated clock: Sleep advances it instantly.
type FakeClock struct {
	mu    sync.Mutex
	now   time.Time
	Slept []time.Duration
}

// NewFakeClock starts at a fixed instant.
func NewFakeClock() *FakeClock { return &FakeClock{now: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)} }

// Now implements Clock.
func (c *FakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

// Advance moves the clock.
func (c *FakeClock) Advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

// Sleep implements Clock without blocking.
func (c *FakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.Slept = append(c.Slept, d)
	c.mu.Unlock()
	return nil
}
