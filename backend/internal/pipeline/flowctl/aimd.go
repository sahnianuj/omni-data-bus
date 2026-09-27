package flowctl

import (
	"context"
	"sync"
	"time"
)

// AIMD adapts write concurrency (OC-025 AC6): when more than 5% of the operations in a 5 s
// window hit temporary failures or out-of-memory, the limit halves; each clean window adds 4,
// up to the configured maximum.
type AIMD struct {
	mu        sync.Mutex
	clock     Clock
	max       int
	limit     int
	window    time.Duration
	threshold float64
	step      int
	start     time.Time
	ops, busy int
	// OnChange is told when the limit changes.
	OnChange func(limit int, reason string)
}

// NewAIMD starts at max.
func NewAIMD(maxLimit int, clock Clock) *AIMD {
	if clock == nil {
		clock = Real
	}
	return &AIMD{clock: clock, max: maxLimit, limit: maxLimit, window: 5 * time.Second, threshold: 0.05, step: 4, start: clock.Now()}
}

// Observe records one operation's result: busy is a temporary failure or server OOM.
func (a *AIMD) Observe(busy bool) {
	a.mu.Lock()
	a.ops++
	if busy {
		a.busy++
	}
	var changed bool
	var reason string
	now := a.clock.Now()
	if now.Sub(a.start) >= a.window {
		switch {
		case a.ops > 0 && float64(a.busy)/float64(a.ops) > a.threshold:
			if n := max(1, a.limit/2); n != a.limit {
				a.limit, changed, reason = n, true, "cluster busy"
			}
		case a.busy == 0 && a.limit < a.max:
			a.limit, changed, reason = min(a.max, a.limit+a.step), true, "recovering"
		}
		a.start, a.ops, a.busy = now, 0, 0
	}
	limit, cb := a.limit, a.OnChange
	a.mu.Unlock()
	if changed && cb != nil {
		cb(limit, reason)
	}
}

// Limit is the current concurrency limit.
func (a *AIMD) Limit() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.limit
}

// Gate bounds in-flight operations by a changing limit.
type Gate struct {
	mu       sync.Mutex
	cond     *sync.Cond
	inflight int
	limit    func() int
	// MaxSeen is the highest in-flight count observed (for tests and metrics).
	MaxSeen int
}

// NewGate uses limit() as the bound on every Acquire.
func NewGate(limit func() int) *Gate {
	g := &Gate{limit: limit}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// Acquire waits for a slot; it fails when ctx ends.
func (g *Gate) Acquire(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() {
		g.mu.Lock()
		g.cond.Broadcast()
		g.mu.Unlock()
	})
	defer stop()
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.inflight >= max(1, g.limit()) {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.cond.Wait()
	}
	g.inflight++
	g.MaxSeen = max(g.MaxSeen, g.inflight)
	return nil
}

// Release frees a slot.
func (g *Gate) Release() {
	g.mu.Lock()
	g.inflight--
	g.cond.Broadcast()
	g.mu.Unlock()
}

// InFlight is the current number of held slots.
func (g *Gate) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inflight
}
