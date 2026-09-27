// Package verify checks a migration after each table finishes (OC-030): it reconciles the
// counts (with a HyperLogLog estimate of distinct keys, to catch key templates that collide),
// re-reads a random sample of rows and compares their documents in Couchbase, and renders the
// job report as JSON, CSV and standalone HTML.
package verify

import (
	"math/rand/v2"
	"sync"

	"github.com/axiomhq/hyperloglog"
)

// Results.
const (
	Passed             = "passed"
	PassedWithWarnings = "passed_with_warnings"
	Failed             = "failed"
	Skipped            = "skipped"
)

// Warning codes.
const (
	CodeKeyCollisions  = "KEY_COLLISIONS_DETECTED"
	CodeTargetMismatch = "TARGET_COUNT_MISMATCH"
	CodePartial        = "VERIFICATION_PARTIAL"
)

// DefaultSampleSize is jobs.Options.VerifySampleSize's default.
const DefaultSampleSize = 100

// Sample is one written row: its source key and document key.
type Sample struct {
	SourceKey []any
	DocKey    string
}

// Reservoir keeps a uniform random sample of n items from a stream (Algorithm R), so the rows
// to verify are chosen during the run without another scan of the source.
type Reservoir struct {
	mu    sync.Mutex
	n     int
	seen  int64
	items []Sample
	rng   *rand.Rand
}

// NewReservoir samples n items; rng nil = random seed.
func NewReservoir(n int, rng *rand.Rand) *Reservoir {
	if rng == nil {
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())) //nolint:gosec // sampling, not security
	}
	return &Reservoir{n: n, rng: rng, items: make([]Sample, 0, n)}
}

// Add offers one item.
func (r *Reservoir) Add(s Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen++
	if len(r.items) < r.n {
		r.items = append(r.items, s)
		return
	}
	if j := r.rng.Int64N(r.seen); j < int64(r.n) {
		r.items[j] = s
	}
}

// Items returns the sample.
func (r *Reservoir) Items() []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Sample(nil), r.items...)
}

// Seen is how many items were offered.
func (r *Reservoir) Seen() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen
}

// Keys estimates how many distinct document keys were written (HyperLogLog++, ~0.8% error).
type Keys struct {
	mu     sync.Mutex
	sketch *hyperloglog.Sketch
	writes int64
}

// NewKeys returns an empty estimator.
func NewKeys() *Keys { return &Keys{sketch: hyperloglog.New16()} }

// Add records a written key.
func (k *Keys) Add(key string) {
	k.mu.Lock()
	k.sketch.Insert([]byte(key))
	k.writes++
	k.mu.Unlock()
}

// Estimate returns the distinct estimate and the number of writes.
func (k *Keys) Estimate() (distinct uint64, writes int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.sketch.Estimate(), k.writes
}
