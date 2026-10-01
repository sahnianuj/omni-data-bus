package related

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// childTable holds child rows [parent_id int8, seq int4], sorted by (parent_id, seq).
type childTable struct {
	rows    [][]any
	ranges  int
	batched int
	stream  bool
}

func (c *childTable) ReadRelated(_ context.Context, q source.RelatedQuery, keys [][]any) ([][]any, error) {
	var out [][]any
	if q.Range != nil {
		c.ranges++
		from, to := q.Range.From.(int64), q.Range.To.(int64)
		for _, r := range c.rows {
			if k := r[0].(int64); k >= from && k <= to {
				out = append(out, r)
			}
		}
		return out, nil
	}
	c.batched++
	want := map[int64]bool{}
	for _, k := range keys {
		want[k[0].(int64)] = true
	}
	for _, r := range c.rows {
		if want[r[0].(int64)] {
			out = append(out, r)
		}
	}
	return out, nil // ORDER BY the join key, then the relation's order (seq)
}

// StreamRelated is ReadRelated one row at a time.
func (c *childTable) StreamRelated(ctx context.Context, q source.RelatedQuery, keys [][]any, fn func([]any) error) error {
	c.stream = true
	rows, err := c.ReadRelated(ctx, q, keys)
	for _, r := range rows {
		if err := fn(r); err != nil {
			return err
		}
	}
	return err
}

func mergePlans(t *testing.T, strategy string, limit int) []Plan {
	t.Helper()
	parent := &schema.Table{Name: "customer", PrimaryKey: []string{"customer_id"}, Columns: []schema.Column{{Name: "customer_id", UDTName: "int8"}}}
	child := &schema.Table{Name: "orders", Columns: []schema.Column{{Name: "customer_id", UDTName: "int8"}, {Name: "seq", UDTName: "int4"}}}
	s := &mapping.Spec{DocSchema: docschema.Schema{Root: docschema.NewRoot()}, Relations: []mapping.Relation{{
		ID: "o", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "shop", Name: "orders"},
		Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, OrderBy: []mapping.OrderColumn{{Column: "seq"}}, Limit: limit,
	}}}
	plans, err := Plans(s, parent, func(_, _ string) *schema.Table { return child })
	require.NoError(t, err)
	plans[0].Strategy = strategy
	return plans
}

func TestMerge_AC2_EqualsBatched(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for round := range 5 {
		// Parents 1..10,000 with skewed fan-out: most have 0–3 children, a few up to 5,000.
		ct := &childTable{}
		for p := int64(1); p <= 10_000; p++ {
			n := rng.IntN(4)
			switch {
			case rng.IntN(500) == 0:
				n = rng.IntN(5001)
			case rng.IntN(3) == 0:
				n = 0
			}
			for s := range n {
				ct.rows = append(ct.rows, []any{p, int32(s)})
			}
		}
		limit := []int{3, 50, 1000, 100_000, 7}[round]
		parents := make([][]any, 0, 10_000)
		for p := int64(1); p <= 10_000; p++ {
			parents = append(parents, []any{p})
		}
		rng.Shuffle(len(parents), func(i, j int) { parents[i], parents[j] = parents[j], parents[i] }) // any read order
		for start := 0; start < len(parents); start += 1000 {
			batch := parents[start:min(start+1000, len(parents))]
			merged, _, err := (&Resolver{Plans: mergePlans(t, StrategySortedMerge, limit), Reader: ct}).Resolve(context.Background(), batch)
			require.NoError(t, err)
			batched, _, err := (&Resolver{Plans: mergePlans(t, StrategyBatched, limit), Reader: ct}).Resolve(context.Background(), batch)
			require.NoError(t, err)
			require.Equal(t, batched, merged, "round %d batch %d", round, start)
		}
		require.Positive(t, ct.ranges)
		require.True(t, ct.stream, "rows are streamed")
	}
}

func TestMerge_AC2_BoundedMemory(t *testing.T) {
	keys := []any{int64(1), int64(2), int64(3), int64(5)}
	const keep = 11 // limit + 1
	var groups [][]int
	m := NewMerger(keys, keep, nil, func(pos int, rows [][]any) {
		groups = append(groups, []int{pos, len(rows)})
	})
	for _, k := range []int64{0, 1, 1, 2, 4, 4} { // 0 and 4 match no parent
		m.Push(k, []any{k})
	}
	for range 5000 { // parent 5 has 5,000 children
		m.Push(int64(5), []any{int64(5)})
	}
	m.Push(int64(9), []any{int64(9)})
	m.Close()
	require.Equal(t, [][]int{{0, 2}, {1, 1}, {3, keep}}, groups, "3 has no children; 5 is cut at keep")
	require.Equal(t, keep, m.MaxRetained, "never more than limit+1 rows held")
}

func TestMerge_CompareAndMergeableTypes(t *testing.T) {
	require.Negative(t, Compare(int32(2), int64(10)), "integers numerically")
	require.Negative(t, Compare("B", "a"), "text byte-wise, like COLLATE \"C\"")
	require.Negative(t, Compare("a", "ab"))
	require.Negative(t, Compare([16]byte{1}, [16]byte{2}))
	require.Negative(t, Compare(time.Unix(1, 0), time.Unix(2, 0)))
	require.Zero(t, Compare(int16(3), 3))
	require.True(t, MergeableType("text"))
	require.False(t, MergeableType("numeric"), "Go can't order numerics like PostgreSQL does")
}
