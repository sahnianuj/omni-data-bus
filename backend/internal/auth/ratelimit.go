package auth

import (
	"container/list"
	"sync"
	"time"

	"omnidatabus/internal/store"
)

// LoginLimiter throttles failed sign-in attempts per key (IP + email) with sliding windows:
// 5 per minute and 20 per hour by default. It is in-memory with an LRU bound on keys.
type LoginLimiter struct {
	mu        sync.Mutex
	clock     store.Clock
	perMinute int
	perHour   int
	maxKeys   int
	entries   map[string]*list.Element
	lru       *list.List
}

type limiterEntry struct {
	key      string
	failures []time.Time // oldest first, within the last hour
}

// NewLoginLimiter returns a limiter with the default policy.
func NewLoginLimiter(clock store.Clock) *LoginLimiter {
	return &LoginLimiter{clock: clock, perMinute: 5, perHour: 20, maxKeys: 100_000, entries: map[string]*list.Element{}, lru: list.New()}
}

// Allow reports whether another attempt is allowed now, and if not, how long to wait.
func (l *LoginLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	el, ok := l.entries[key]
	if !ok {
		return true, 0
	}
	e := el.Value.(*limiterEntry)
	now := l.clock.Now()
	e.failures = prune(e.failures, now.Add(-time.Hour))
	if len(e.failures) >= l.perHour {
		return false, e.failures[len(e.failures)-l.perHour].Add(time.Hour).Sub(now)
	}
	recent := 0
	for _, f := range e.failures {
		if f.After(now.Add(-time.Minute)) {
			recent++
		}
	}
	if recent >= l.perMinute {
		oldestInMinute := e.failures[len(e.failures)-l.perMinute]
		return false, oldestInMinute.Add(time.Minute).Sub(now)
	}
	return true, 0
}

// Fail records a failed attempt.
func (l *LoginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if el, ok := l.entries[key]; ok {
		e := el.Value.(*limiterEntry)
		e.failures = append(prune(e.failures, now.Add(-time.Hour)), now)
		l.lru.MoveToFront(el)
		return
	}
	l.entries[key] = l.lru.PushFront(&limiterEntry{key: key, failures: []time.Time{now}})
	for l.lru.Len() > l.maxKeys {
		oldest := l.lru.Back()
		l.lru.Remove(oldest)
		delete(l.entries, oldest.Value.(*limiterEntry).key)
	}
}

// Reset clears a key after a successful sign-in.
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if el, ok := l.entries[key]; ok {
		l.lru.Remove(el)
		delete(l.entries, key)
	}
}

func prune(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && !ts[i].After(cutoff) {
		i++
	}
	return ts[i:]
}
