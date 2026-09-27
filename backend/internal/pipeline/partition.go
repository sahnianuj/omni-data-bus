// Package pipeline streams a mapping's rows from the source, transforms them into documents
// and writes them to the target with bounded memory and backpressure (OC-024, OC-025, OC-026).
package pipeline

import (
	"math/big"
	"slices"
)

// Partitioning thresholds (OC-024).
const (
	MinRowsToPartition = 500_000
	MaxPartitions      = 8
	RowsPerPartition   = 1_000_000
)

// Range is one partition's key range [Lo, Hi); nil bounds are open.
type Range struct {
	Lo, Hi any
}

// KeyKind is how a single-column key can be split.
type KeyKind int

// Key kinds.
const (
	KeyOther KeyKind = iota // composite, text, … : one partition
	KeyInt
	KeyUUID
)

// KindOf classifies a key column by its PostgreSQL type.
func KindOf(udt string, keyColumns int) KeyKind {
	if keyColumns != 1 {
		return KeyOther
	}
	switch udt {
	case "int2", "int4", "int8":
		return KeyInt
	case "uuid":
		return KeyUUID
	}
	return KeyOther
}

// PartitionCount is K: the fixed parallelism (capped at 8) or, for "auto" (0), one per
// million estimated rows, clamped to 1..8. Small tables and unsplittable keys get 1.
func PartitionCount(kind KeyKind, rowEstimate int64, parallelism int) int {
	if kind == KeyOther || rowEstimate < MinRowsToPartition {
		return 1
	}
	k := parallelism
	if k <= 0 {
		k = int(rowEstimate / RowsPerPartition)
	}
	return min(max(k, 1), MaxPartitions)
}

// PlanPartitions splits [lo, hi] (the key's min and max) into k contiguous, non-overlapping
// ranges. The first range starts open and the last ends open, so nothing is missed.
// Integer bounds are int64; UUID bounds are [16]byte and split the 128-bit space evenly (fine
// for random v4 keys).
func PlanPartitions(kind KeyKind, lo, hi any, k int) []Range {
	if k <= 1 || lo == nil || hi == nil {
		return []Range{{}}
	}
	var a, b *big.Int
	switch kind {
	case KeyInt:
		l, ok1 := asInt64(lo)
		h, ok2 := asInt64(hi)
		if !ok1 || !ok2 || h <= l {
			return []Range{{}}
		}
		a, b = big.NewInt(l), big.NewInt(h)
	case KeyUUID:
		a, b = big.NewInt(0), new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	default:
		return []Range{{}}
	}
	span := new(big.Int).Add(new(big.Int).Sub(b, a), big.NewInt(1)) // hi - lo + 1 values
	if span.Cmp(big.NewInt(int64(k))) < 0 {
		k = int(span.Int64())
	}
	bounds := make([]*big.Int, k+1)
	for i := 0; i <= k; i++ {
		step := new(big.Int).Mul(span, big.NewInt(int64(i)))
		step.Quo(step, big.NewInt(int64(k)))
		bounds[i] = new(big.Int).Add(a, step)
	}
	out := make([]Range, k)
	for i := 0; i < k; i++ {
		var r Range
		if i > 0 {
			r.Lo = toBound(kind, bounds[i])
		}
		if i < k-1 {
			r.Hi = toBound(kind, bounds[i+1])
		}
		out[i] = r
	}
	return out
}

func toBound(kind KeyKind, v *big.Int) any {
	if kind == KeyInt {
		return v.Int64()
	}
	var b [16]byte
	v.FillBytes(b[:])
	return b
}

func asInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int32:
		return int64(x), true
	case int16:
		return int64(x), true
	case int:
		return int64(x), true
	}
	return 0, false
}

// compareKey orders two key values of the same kind (for tests and range checks).
func compareKey(a, b any) int {
	if x, ok := asInt64(a); ok {
		y, _ := asInt64(b)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	}
	x, _ := a.([16]byte)
	y, _ := b.([16]byte)
	return slices.Compare(x[:], y[:])
}
