package verify

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReconcile_AC1_Balanced(t *testing.T) {
	k := NewKeys()
	for i := 0; i < 9990; i++ {
		k.Add(fmt.Sprintf("customer::%d", i))
	}
	d, w := k.Estimate()
	c := Reconcile(CountsInput{RowsRead: 10000, Written: 9990, Failed: 10, Distinct: d, Writes: w, Mode: "upsert"})
	require.True(t, c.Reconciled)
	require.Empty(t, c.Warnings)
	require.Zero(t, c.DuplicateKeys)
	require.EqualValues(t, 9990, c.ExpectedDocs)
	require.InDelta(t, 9990, float64(*c.DistinctKeys), 9990*0.02, "HLL estimate within 2%")

	bad := Reconcile(CountsInput{RowsRead: 10000, Written: 9980, Failed: 10, Distinct: d, Writes: 9980})
	require.False(t, bad.Reconciled, "10 rows have no outcome")
	require.Contains(t, bad.Messages[0], "10000 rows read")
}

func TestReconcile_AC1_DetectsKeyCollisions(t *testing.T) {
	// 9,990 writes whose keys repeat 10% of the time: about 9,000 distinct.
	k := NewKeys()
	for i := 0; i < 9990; i++ {
		k.Add(fmt.Sprintf("customer::%d", i%9000))
	}
	d, w := k.Estimate()
	c := Reconcile(CountsInput{RowsRead: 10000, Written: 9990, Failed: 10, Distinct: d, Writes: w})
	require.True(t, c.Reconciled, "every row has an outcome")
	require.Equal(t, []string{CodeKeyCollisions}, c.Warnings)
	require.InDelta(t, 990, float64(c.DuplicateKeys), 200)
	require.Contains(t, c.Messages[0], "distinct keys for 9990 writes")
	require.Less(t, c.ExpectedDocs, int64(9990))
}

func TestReconcile_AC1_TargetCountOnlyWhenEmptyAtStart(t *testing.T) {
	// Without a count (the collection wasn't empty, or Query is unavailable): nothing compared.
	c := Reconcile(CountsInput{RowsRead: 100, Written: 100, Distinct: 100, Writes: 100})
	require.Nil(t, c.TargetCount)
	require.Nil(t, c.TargetCountMatch)

	n := int64(100)
	c = Reconcile(CountsInput{RowsRead: 100, Written: 100, Distinct: 100, Writes: 100, TargetCount: &n})
	require.True(t, *c.TargetCountMatch)
	n = 97
	c = Reconcile(CountsInput{RowsRead: 100, Written: 100, Distinct: 100, Writes: 100, TargetCount: &n})
	require.False(t, *c.TargetCountMatch)
	require.Equal(t, []string{CodeTargetMismatch}, c.Warnings)

	// Insert mode: skipped rows' documents already existed, so they count.
	n = 100
	c = Reconcile(CountsInput{RowsRead: 100, Written: 90, Skipped: 10, Distinct: 90, Writes: 90, TargetCount: &n, Mode: "insert"})
	require.True(t, *c.TargetCountMatch)
	// A resumed run has no distinct estimate: partial, not a warning that fails anything.
	c = Reconcile(CountsInput{RowsRead: 10, Written: 10})
	require.Equal(t, []string{CodePartial}, c.Warnings)
	require.Nil(t, c.DistinctKeys)
}
