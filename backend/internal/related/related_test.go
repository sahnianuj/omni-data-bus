package related

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
		Relations: []mapping.Relation{
			{ID: "a", Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "address"}, Join: []mapping.JoinPair{{Parent: "address_id", Child: "address_id"}}},
			{
				ID: "o", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "shop", Name: "orders"}, Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, Limit: 2,
				OrderBy: []mapping.OrderColumn{{Column: "order_id", Desc: true}},
			},
		},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m1", Sources: []mapping.SourceRef{{Column: "city", Relation: "a"}}, State: mapping.StateActive},
			{ID: "m2", Sources: []mapping.SourceRef{{Column: "note", Relation: "o"}}, State: mapping.StateActive, Transforms: []mapping.Step{{Op: "expression"}}},
			{ID: "m3", Sources: []mapping.SourceRef{{Column: "zip", Relation: "a"}}, State: mapping.StatePending},
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
	bad.Relations[0].Join[0].Child = "nope"
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
	got, fails, err := r.Resolve(context.Background(), [][]any{{int32(7), int32(2), "x"}, {int32(8), nil, "y"}})
	require.NoError(t, err)
	require.Empty(t, fails)
	require.EqualValues(t, 2, fr.calls.Load(), "one query per embed")
	require.EqualValues(t, 2, wrapped.Load(), "through Do")
	require.Equal(t, []source.RelatedRow{{Values: []any{int32(2), "Nice", "06"}}}, got[0]["a"].Rows, "int4 parent key matches")
	require.Nil(t, got[1]["a"], "null key: none")
	require.Len(t, got[0]["o"].Rows, 3, "limit+1 kept so truncation shows")
	require.Nil(t, got[1]["o"])
	row := Row(got, 0)
	require.Equal(t, 3, row["o"].Len())
	require.Nil(t, Row(nil, 0))
	empty, _, err := (&Resolver{Plans: plans, Reader: fr}).Resolve(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestResolve_ErrorsAndCancel(t *testing.T) {
	parent, _, _ := tables()
	plans, _ := Plans(spec(), parent, rel())
	r := &Resolver{Plans: plans, Reader: &fakeReader{err: errors.New("boom")}, Concurrency: 1}
	_, _, err := r.Resolve(context.Background(), [][]any{{int32(7), int32(2), "x"}})
	require.ErrorContains(t, err, "boom")
	require.ErrorContains(t, err, "embed ")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = &Resolver{Plans: plans, Reader: &fakeReader{delay: time.Second}, Concurrency: 1}
	_, _, err = r.Resolve(ctx, [][]any{{int32(7), int32(2), "x"}})
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

// OC-157 AC4: a one-to-one from the referenced side (customer ← customer_profile, unique
// customer_id) is a many-to-one keyed on the child's foreign key: one object per parent.
func TestPlans_OC157_ReverseOneToOneIsOneObject(t *testing.T) {
	parent, _, _ := tables()
	profile := &schema.Table{Name: "customer_profile", PrimaryKey: []string{"customer_id"}, Columns: []schema.Column{{Name: "bio", UDTName: "text"}, {Name: "customer_id", UDTName: "int4"}}}
	s := &mapping.Spec{
		Relations:     []mapping.Relation{{ID: "p", Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "customer_profile"}, Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}}},
		FieldMappings: []mapping.FieldMapping{{ID: "m1", Sources: []mapping.SourceRef{{Column: "bio", Relation: "p"}}, State: mapping.StateActive}},
		DocSchema:     docschema.Schema{Root: docschema.NewRoot()},
	}
	plans, err := Plans(s, parent, func(_, n string) *schema.Table { return map[string]*schema.Table{"customer_profile": profile}[n] })
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.False(t, plans[0].Many)
	require.Equal(t, 1, plans[0].Keep)
	require.Equal(t, []string{"customer_id"}, plans[0].Query.Join)
	require.Equal(t, []int{0}, plans[0].ParentIdx)
	require.Equal(t, []int{1}, plans[0].ChildIdx)
}

// keysReader records each query's table, projection and number of keys.
type keysReader struct {
	fakeReader
	mu      sync.Mutex
	queries []string
}

func (k *keysReader) ReadRelated(ctx context.Context, q source.RelatedQuery, keys [][]any) ([][]any, error) {
	k.mu.Lock()
	k.queries = append(k.queries, fmt.Sprintf("%s keys=%d project=%v", q.Table.Table, len(keys), q.Table.Project))
	k.mu.Unlock()
	return k.fakeReader.ReadRelated(ctx, q, keys)
}

// OC-144 AC1: two many-to-one relations of one depth reading the same table by the same
// columns (shipping and billing address) share one query, with the union of their keys and
// columns, and each gets the rows it would have read alone; a filter keeps them apart.
func TestResolver_OC144_AC1_SharedManyToOneReads(t *testing.T) {
	c := func(n, udt string) schema.Column { return schema.Column{Name: n, UDTName: udt} }
	customer := &schema.Table{Name: "customer", Columns: []schema.Column{c("customer_id", "int4"), c("ship_id", "int4"), c("bill_id", "int4")}}
	address := &schema.Table{Name: "address", Columns: []schema.Column{c("address_id", "int4"), c("city", "text"), c("zip", "text")}}
	addr := mapping.TableRef{Schema: "shop", Name: "address"}
	build := func(filter bool) *mapping.Spec {
		s := &mapping.Spec{
			Relations: []mapping.Relation{
				{ID: "ship", Kind: mapping.EmbedManyToOne, Table: addr, Join: []mapping.JoinPair{{Parent: "ship_id", Child: "address_id"}}},
				{ID: "bill", Kind: mapping.EmbedManyToOne, Table: addr, Join: []mapping.JoinPair{{Parent: "bill_id", Child: "address_id"}}},
			},
			FieldMappings: []mapping.FieldMapping{
				{ID: "m1", Sources: []mapping.SourceRef{{Column: "city", Relation: "ship"}}, State: mapping.StateActive},
				{ID: "m2", Sources: []mapping.SourceRef{{Column: "zip", Relation: "bill"}}, State: mapping.StateActive},
			},
			DocSchema: docschema.Schema{Root: docschema.NewRoot()},
		}
		if filter {
			s.Relations[1].Filter = []mapping.Filter{{Column: "zip", Op: "eq", Value: "06"}}
		}
		return s
	}
	related := func(_, n string) *schema.Table {
		if n == "address" {
			return address
		}
		return nil
	}
	rows := map[string][][]any{"address": {{int32(1), "Lyon", "69"}, {int32(2), "Nice", "06"}, {int32(3), "Pau", "64"}}}
	parents := [][]any{{int32(7), int32(1), int32(2)}, {int32(8), int32(3), int32(3)}}

	resolve := func(s *mapping.Spec, noShare bool) ([]source.Related, *keysReader) {
		plans, err := Plans(s, customer, related)
		require.NoError(t, err)
		kr := &keysReader{fakeReader: fakeReader{rows: rows}}
		got, fails, err := (&Resolver{Plans: plans, Reader: kr, NoShare: noShare}).Resolve(context.Background(), parents)
		require.NoError(t, err)
		require.Empty(t, fails)
		return got, kr
	}
	shared, kr := resolve(build(false), false)
	require.Len(t, kr.queries, 1, "one address query")
	require.Equal(t, "address keys=3 project=[true true true]", kr.queries[0], "keys 1, 2, 3 once each; city and zip")
	alone, kr2 := resolve(build(false), true)
	require.Len(t, kr2.queries, 2)
	for i := range parents {
		for _, id := range []string{"ship", "bill"} {
			require.Equal(t, alone[i][id].Rows, shared[i][id].Rows, "row %d %s", i, id)
		}
	}
	require.Equal(t, []any{int32(1), "Lyon", "69"}, shared[0]["ship"].Rows[0].Values)
	require.Equal(t, []any{int32(2), "Nice", "06"}, shared[0]["bill"].Rows[0].Values)

	_, kr3 := resolve(build(true), false)
	require.Len(t, kr3.queries, 2, "a filtered relation reads alone")
}
