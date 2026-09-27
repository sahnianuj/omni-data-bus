package related

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

func tables() (parent, address, orders *schema.Table) {
	c := func(n, udt string) schema.Column { return schema.Column{Name: n, UDTName: udt} }
	parent = &schema.Table{Name: "customer", Columns: []schema.Column{c("customer_id", "int4"), c("address_id", "int4"), c("name", "text")}}
	address = &schema.Table{Name: "address", Columns: []schema.Column{c("address_id", "int4"), c("city", "text"), c("zip", "text")}}
	orders = &schema.Table{Name: "orders", Columns: []schema.Column{c("order_id", "int8"), c("customer_id", "int4"), c("note", "text")}}
	return parent, address, orders
}

func spec() *mapping.Spec {
	s := &mapping.Spec{
		Embeds: []mapping.Embed{
			{ID: "a", Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "address"}, Join: []mapping.JoinPair{{Parent: "address_id", Child: "address_id"}}},
			{
				ID: "o", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "shop", Name: "orders"}, Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, Limit: 2,
				OrderBy: []mapping.OrderColumn{{Column: "order_id", Desc: true}},
			},
		},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m1", Sources: []mapping.SourceRef{{Column: "city", Embed: "a"}}, State: mapping.StateActive},
			{ID: "m2", Sources: []mapping.SourceRef{{Column: "note", Embed: "o"}}, State: mapping.StateActive, Transforms: []mapping.Step{{Op: "expression"}}},
			{ID: "m3", Sources: []mapping.SourceRef{{Column: "zip", Embed: "a"}}, State: mapping.StatePending},
		},
		DocSchema: docschema.Schema{Root: docschema.NewRoot()},
	}
	return s
}

func rel() mapping.RelatedTables {
	_, address, orders := tables()
	return func(_, n string) *schema.Table {
		switch n {
		case "address":
			return address
		case "orders":
			return orders
		}
		return nil
	}
}

func TestPlans_ProjectionsAndLimits(t *testing.T) {
	parent, _, _ := tables()
	plans, err := Plans(spec(), parent, rel())
	require.NoError(t, err)
	require.Len(t, plans, 2)
	a, o := plans[0], plans[1]
	require.False(t, a.Many)
	require.Equal(t, 1, a.Keep)
	require.Equal(t, []int{1}, a.ParentIdx)
	require.Equal(t, []int{0}, a.ChildIdx)
	require.Equal(t, []bool{true, true, false}, a.Query.Table.Project, "join + active mapping; a pending suggestion isn't read")
	require.True(t, o.Many)
	require.Equal(t, 3, o.Keep)
	require.Equal(t, 3, o.Query.PerKeyLimit)
	require.Equal(t, []source.OrderColumn{{Column: "order_id", Desc: true}}, o.Query.OrderBy)
	require.Equal(t, []bool{true, true, true}, o.Query.Table.Project, "an expression may read any column")

	require.Equal(t, []string{"address_id", "customer_id"}, ParentColumns(spec()))
	require.Len(t, Tables(spec(), rel()), 2)
	require.Empty(t, Tables(spec(), nil))

	_, err = Plans(spec(), parent, func(string, string) *schema.Table { return nil })
	require.ErrorContains(t, err, "shop.address isn't in the latest schema snapshot")
	bad := spec()
	bad.Embeds[0].Join[0].Child = "nope"
	_, err = Plans(bad, parent, rel())
	require.ErrorContains(t, err, "join column address_id → nope is missing")
}

type fakeReader struct {
	calls atomic.Int32
	err   error
	rows  map[string][][]any // table → rows
	delay time.Duration
}

func (f *fakeReader) ReadRelated(ctx context.Context, q source.RelatedQuery, _ [][]any) ([][]any, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.rows[q.Table.Table], f.err
}

func TestResolve_GroupsKeepsAndNulls(t *testing.T) {
	parent, _, _ := tables()
	plans, _ := Plans(spec(), parent, rel())
	fr := &fakeReader{rows: map[string][][]any{
		"address": {{int32(1), "Lyon", "69"}, {int32(2), "Nice", "06"}},
		// customer 7 has 4 orders; Keep (limit+1) = 3 are kept.
		"orders": {{int64(4), int64(7), "d"}, {int64(3), int64(7), "c"}, {int64(2), int64(7), "b"}, {int64(1), int64(7), "a"}},
	}}
	var wrapped atomic.Int32
	r := &Resolver{Plans: plans, Reader: fr, Do: func(_ context.Context, read func() ([][]any, error)) ([][]any, error) {
		wrapped.Add(1)
		return read()
	}}
	got, err := r.Resolve(context.Background(), [][]any{{int32(7), int32(2), "x"}, {int32(8), nil, "y"}})
	require.NoError(t, err)
	require.EqualValues(t, 2, fr.calls.Load(), "one query per embed")
	require.EqualValues(t, 2, wrapped.Load(), "through Do")
	require.Equal(t, [][]any{{int32(2), "Nice", "06"}}, got["a"][0], "int4 parent key matches")
	require.Nil(t, got["a"][1], "null key: none")
	require.Len(t, got["o"][0], 3, "limit+1 kept so truncation shows")
	require.Nil(t, got["o"][1])
	row := Row(got, 0)
	require.Len(t, row["o"], 3)
	require.Nil(t, Row(nil, 0))
	empty, err := (&Resolver{Plans: plans, Reader: fr}).Resolve(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestResolve_ErrorsAndCancel(t *testing.T) {
	parent, _, _ := tables()
	plans, _ := Plans(spec(), parent, rel())
	r := &Resolver{Plans: plans, Reader: &fakeReader{err: errors.New("boom")}, Concurrency: 1}
	_, err := r.Resolve(context.Background(), [][]any{{int32(7), int32(2), "x"}})
	require.ErrorContains(t, err, "boom")
	require.ErrorContains(t, err, "embed ")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = &Resolver{Plans: plans, Reader: &fakeReader{delay: time.Second}, Concurrency: 1}
	_, err = r.Resolve(ctx, [][]any{{int32(7), int32(2), "x"}})
	require.ErrorIs(t, err, context.Canceled)
}

func TestKeyOf_NormalizesTypes(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	for _, pair := range [][2]any{
		{int16(5), int64(5)}, {int32(5), int(5)}, {"a", "a"}, {[16]byte{1}, [16]byte{1}}, {[]byte{1, 2}, []byte{1, 2}}, {ts, ts}, {1.5, 1.5},
	} {
		a, ok := KeyOf([]any{pair[0]}, []int{0})
		require.True(t, ok)
		b, _ := KeyOf([]any{pair[1]}, []int{0})
		require.Equal(t, a, b, "%T vs %T", pair[0], pair[1])
	}
	x, _ := KeyOf([]any{"a", int32(1)}, []int{0, 1})
	y, _ := KeyOf([]any{"a1", nil}, []int{0})
	require.NotEqual(t, x, y, "composite keys are separated")
	_, ok := KeyOf([]any{"a"}, []int{3})
	require.False(t, ok)
}
