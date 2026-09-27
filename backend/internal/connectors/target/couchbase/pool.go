package couchbase

import (
	"sync"
	"time"
)

// Closer is anything the pool can release.
type Closer interface{ Close() error }

// Pool caches open cluster connections per key (connection ID + config version), because
// bootstrapping the SDK is expensive (OC-012 AC5). Idle entries are closed after ttl.
type Pool struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[string]*poolEntry
}

type poolEntry struct {
	key      string
	value    Closer
	lastUsed time.Time
}

// NewPool returns an empty pool.
func NewPool(ttl time.Duration, now func() time.Time) *Pool {
	return &Pool{ttl: ttl, now: now, entries: map[string]*poolEntry{}}
}

// Get returns the cached value for id at version, opening it with open when missing. A new
// version (the connection was edited) closes and replaces the old value.
func (p *Pool) Get(id, version string, open func() (Closer, error)) (Closer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictLocked()
	key := id + "@" + version
	if e, ok := p.entries[id]; ok {
		if e.key == key {
			e.lastUsed = p.now()
			return e.value, nil
		}
		_ = e.value.Close()
		delete(p.entries, id)
	}
	v, err := open()
	if err != nil {
		return nil, err
	}
	p.entries[id] = &poolEntry{key: key, value: v, lastUsed: p.now()}
	return v, nil
}

// Evict closes idle entries (called on every Get; exported for a periodic sweeper).
func (p *Pool) Evict() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictLocked()
}

func (p *Pool) evictLocked() {
	cutoff := p.now().Add(-p.ttl)
	for id, e := range p.entries {
		if e.lastUsed.Before(cutoff) {
			_ = e.value.Close()
			delete(p.entries, id)
		}
	}
}

// CloseAll releases everything.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, e := range p.entries {
		_ = e.value.Close()
		delete(p.entries, id)
	}
}

// Len is the number of open entries.
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}
