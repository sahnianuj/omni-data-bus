package pipeline

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// covers checks the ranges tile (-inf, +inf) in order: each Hi equals the next Lo, so the
// union covers [min, max] with no overlap.
func covers(t *testing.T, rs []Range) {
	t.Helper()
	require.Nil(t, rs[0].Lo, "first range open below")
	require.Nil(t, rs[len(rs)-1].Hi, "last range open above")
	for i := 0; i+1 < len(rs); i++ {
		require.Equal(t, rs[i].Hi, rs[i+1].Lo, "contiguous at %d", i)
		if rs[i].Lo != nil {
			require.Equal(t, -1, compareKey(rs[i].Lo, rs[i].Hi), "non-empty range %d", i)
		}
	}
}

// owner returns the index of the range containing v.
func owner(rs []Range, v int64) int {
	n := -1
	for i, r := range rs {
		if (r.Lo == nil || compareKey(v, r.Lo) >= 0) && (r.Hi == nil || compareKey(v, r.Hi) < 0) {
			if n != -1 {
				return -2 // overlap
			}
			n = i
		}
	}
	return n
}

func TestPlanPartitions_AC2_IntRanges(t *testing.T) {
	k := PartitionCount(KeyInt, 10_000_000, 0)
	require.Equal(t, 8, k)
	rs := PlanPartitions(KeyInt, int64(1), int64(10_000_000), k)
	require.Len(t, rs, 8)
	covers(t, rs)
	// Property: every value in [min, max] (and a random sample) falls in exactly one range,
	// and ranges are roughly equal.
	for _, v := range []int64{1, 2, 1_250_000, 1_250_001, 9_999_999, 10_000_000} {
		require.GreaterOrEqual(t, owner(rs, v), 0, "value %d", v)
	}
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // test data
	for range 10_000 {
		require.GreaterOrEqual(t, owner(rs, 1+r.Int64N(10_000_000)), 0)
	}
	require.Equal(t, int64(1_250_001), rs[1].Lo)
	// Negative and narrow spans.
	rs = PlanPartitions(KeyInt, int64(-50), int64(-47), 8)
	require.Len(t, rs, 4, "no more ranges than values")
	covers(t, rs)
	for v := int64(-50); v <= -47; v++ {
		require.GreaterOrEqual(t, owner(rs, v), 0)
	}
	require.Equal(t, 3, PartitionCount(KeyInt, 3_000_000, 0))
	require.Equal(t, 8, PartitionCount(KeyInt, 90_000_000, 0), "capped")
	require.Equal(t, 4, PartitionCount(KeyInt, 90_000_000, 4), "fixed parallelism")
	require.Equal(t, 8, PartitionCount(KeyInt, 90_000_000, 16), "fixed parallelism is capped too")
}

func TestPlanPartitions_AC2_SmallTableSingle(t *testing.T) {
	k := PartitionCount(KeyInt, 200_000, 0)
	require.Equal(t, 1, k)
	require.Equal(t, 1, PartitionCount(KeyInt, 200_000, 8), "below 500k even when fixed")
	require.Equal(t, []Range{{}}, PlanPartitions(KeyInt, int64(1), int64(200_000), k))
	require.Equal(t, []Range{{}}, PlanPartitions(KeyInt, nil, nil, 8), "empty table")
}

func TestPlanPartitions_AC2_CompositeSingle(t *testing.T) {
	require.Equal(t, KeyOther, KindOf("int4", 2))
	require.Equal(t, 1, PartitionCount(KindOf("int4", 2), 50_000_000, 8))
	require.Equal(t, KeyOther, KindOf("text", 1))
	require.Equal(t, KeyInt, KindOf("int8", 1))
	require.Equal(t, KeyUUID, KindOf("uuid", 1))
}

func TestPlanPartitions_AC2_UUIDSpace(t *testing.T) {
	rs := PlanPartitions(KeyUUID, [16]byte{}, [16]byte{}, 4)
	require.Len(t, rs, 4)
	covers(t, rs)
	require.Equal(t, [16]byte{0x40}, rs[1].Lo)
	require.Equal(t, [16]byte{0x80}, rs[2].Lo)
	require.Equal(t, [16]byte{0xc0}, rs[3].Lo)
}
