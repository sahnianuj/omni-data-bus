package directory

import (
	"hash/fnv"
	"math"
)

// bloom is a plain Bloom filter (double hashing over FNV-1a 64).
type bloom struct {
	bits []uint64
	m, k uint64
}

func newBloom(n int, p float64) *bloom {
	m := uint64(math.Ceil(-float64(n) * math.Log(p) / (math.Ln2 * math.Ln2)))
	k := uint64(math.Max(1, math.Round(float64(m)/float64(n)*math.Ln2)))
	return &bloom{bits: make([]uint64, (m+63)/64), m: m, k: k}
}

func (b *bloom) hashes(s string) (uint64, uint64) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	x := h.Sum64()
	return x, x>>33 | x<<31 | 1
}

func (b *bloom) add(s string) {
	h1, h2 := b.hashes(s)
	for i := uint64(0); i < b.k; i++ {
		n := (h1 + i*h2) % b.m
		b.bits[n/64] |= 1 << (n % 64)
	}
}

func (b *bloom) has(s string) bool {
	h1, h2 := b.hashes(s)
	for i := uint64(0); i < b.k; i++ {
		n := (h1 + i*h2) % b.m
		if b.bits[n/64]&(1<<(n%64)) == 0 {
			return false
		}
	}
	return true
}
