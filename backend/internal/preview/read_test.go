package preview

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/plan"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/transform"
)

// customersWithOrders is customers with orders[] directly under the root, compiled against
// in-memory tables.
func customersWithOrders(t *testing.T) (*mapping.Spec, *plan.ExecutionPlan, *schema.Table) {
	t.Helper()
	customers := &schema.Table{
		Name: "customers", PrimaryKey: []string{"customer_id"}, Readable: true,
		Columns: []schema.Column{{Name: "customer_id", UDTName: "int4", JSONType: schema.TypeInteger}, {Name: "name", UDTName: "text", JSONType: schema.TypeString}},
	}
	orders := &schema.Table{
		Name: "orders", PrimaryKey: []string{"order_id"}, Readable: true,
		Columns: []schema.Column{{Name: "order_id", UDTName: "int8", JSONType: schema.TypeInteger}, {Name: "order_no", UDTName: "text", JSONType: schema.TypeString}, {Name: "customer_id", UDTName: "int4", JSONType: schema.TypeInteger}},
	}
	order := docschema.DocField{ID: "f_order", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
		{ID: "f_ono", Name: "orderNo", Type: docschema.String},
	}}
	spec := &mapping.Spec{
		Mode: mapping.ModeComposed, Root: &mapping.Root{Table: mapping.TableRef{Schema: "s", Name: "customers"}},
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_name", Name: "name", Type: docschema.String},
			{ID: "f_orders", Name: "orders", Type: docschema.Array, Items: &order},
		}}},
		Key: mapping.KeySpec{Template: "customer::{customer_id}"},
		Relations: []mapping.Relation{{
			ID: "rel_orders", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "s", Name: "orders"},
			Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, OrderBy: []mapping.OrderColumn{{Column: "order_id"}},
			TargetFieldID: "f_orders",
		}},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m_name", TargetFieldID: "f_name", State: mapping.StateActive, Sources: []mapping.SourceRef{{Column: "name"}}},
			{ID: "m_ono", TargetFieldID: "f_ono", State: mapping.StateActive, Sources: []mapping.SourceRef{{Relation: "rel_orders", Column: "order_no"}}},
		},
	}
	spec.Normalize()
	pl, issues, err := plan.Compile(plan.Input{
		Spec: spec, Root: spec.Root.Table, Table: customers, Options: transform.Options{},
		Related: func(_, name string) *schema.Table {
			if name == "orders" {
				return orders
			}
			return nil
		},
	})
	require.NoError(t, err)
	require.NotNil(t, pl.Program, "%v", issues)
	return spec, pl, customers
}

// plainSession reads keys only; the embedded interface panics on anything else.
type plainSession struct {
	source.ReadSession
	keysRead []source.TableRead
	keys     [][][]any
	rows     [][]any
	err      error
}

func (s *plainSession) ReadKeys(_ context.Context, t source.TableRead, keys [][]any) ([][]any, error) {
	s.keysRead = append(s.keysRead, t)
	s.keys = append(s.keys, keys)
	return s.rows, s.err
}

// relatedSession also reads related rows (none) and, with counts set, counts them.
type relatedSession struct {
	plainSession
	relErr error
}

func (s *relatedSession) ReadRelated(context.Context, source.RelatedQuery, [][]any) ([][]any, error) {
	return nil, s.relErr
}

type countingSession struct {
	relatedSession
	counts   []int64
	countErr error
	topKey   []any
	topN     int64
	topFound bool
	topErr   error
}

func (s *countingSession) CountRelated(_ context.Context, _ source.RelatedQuery, keys [][]any) ([]int64, error) {
	if s.countErr != nil {
		return nil, s.countErr
	}
	return s.counts[:min(len(s.counts), len(keys))], nil
}

func (s *countingSession) TopRelated(context.Context, source.RelatedQuery) ([]any, int64, bool, error) {
	return s.topKey, s.topN, s.topFound, s.topErr
}

func TestRelated_NothingToLookUp(t *testing.T) {
	ctx := context.Background()
	spec, pl, _ := customersWithOrders(t)
	rows := [][]any{{int32(1), "Ana"}}

	for name, rs := range map[string]source.ReadSession{"no session": nil, "no related reader": &plainSession{}} {
		rel, fails, totals, err := Related(ctx, rs, pl, spec, rows)
		require.NoError(t, err, name)
		require.Nil(t, rel, name)
		require.Nil(t, fails, name)
		require.Nil(t, totals, name)
	}
	rel, _, _, err := Related(ctx, &relatedSession{}, pl, spec, nil)
	require.NoError(t, err)
	require.Nil(t, rel, "no rows")
}

func TestRelated_TotalsWhenTheSourceCounts(t *testing.T) {
	ctx := context.Background()
	spec, pl, _ := customersWithOrders(t)
	rows := [][]any{{int32(1), "Ana"}, {int32(2), "Bo"}}

	rel, fails, totals, err := Related(ctx, &relatedSession{}, pl, spec, rows)
	require.NoError(t, err)
	require.Len(t, rel, 2)
	require.Empty(t, fails)
	require.Equal(t, []map[string]int64{nil, nil}, totals, "a source that can't count leaves totals empty")

	// The second count is missing: that row keeps no total.
	rel, _, totals, err = Related(ctx, &countingSession{counts: []int64{1240}}, pl, spec, rows)
	require.NoError(t, err)
	require.Len(t, rel, 2)
	require.Equal(t, map[string]int64{"rel_orders": 1240}, totals[0])
	require.Nil(t, totals[1])
}

func TestRelated_Errors(t *testing.T) {
	ctx := context.Background()
	spec, pl, _ := customersWithOrders(t)
	rows := [][]any{{int32(1), "Ana"}}
	boom := errors.New("boom")

	_, _, _, err := Related(ctx, &relatedSession{relErr: boom}, pl, spec, rows)
	require.ErrorIs(t, err, boom)
	_, _, _, err = Related(ctx, &countingSession{countErr: boom}, pl, spec, rows)
	require.ErrorIs(t, err, boom)
}

func TestRowsByKey(t *testing.T) {
	ctx := context.Background()
	spec, _, customers := customersWithOrders(t)
	drv := spec.Root.Table

	rows, err := RowsByKey(ctx, nil, drv, customers, map[string]any{"customer_id": 1})
	require.NoError(t, err)
	require.Nil(t, rows)

	_, err = RowsByKey(ctx, &plainSession{}, drv, customers, map[string]any{"nope": 1})
	require.ErrorContains(t, err, "Column nope isn't in s.customers")

	rs := &plainSession{rows: [][]any{{int32(7), "Ana"}, {int32(7), "Ana again"}}}
	rows, err = RowsByKey(ctx, rs, drv, customers, map[string]any{"name": "Ana", "customer_id": 7})
	require.NoError(t, err)
	require.Equal(t, [][]any{{int32(7), "Ana"}}, rows, "one row per key")
	read := rs.keysRead[0]
	require.Equal(t, []string{"customer_id", "name"}, read.Key, "key columns in a stable order")
	require.Equal(t, [][]any{{7, "Ana"}}, rs.keys[0])
	require.True(t, read.Distinct)
	require.Equal(t, []bool{true, true}, read.Project, "every column")
	require.Equal(t, customers.PrimaryKey, read.Within)
}

func TestWorstCaseRows(t *testing.T) {
	ctx := context.Background()
	spec, pl, customers := customersWithOrders(t)
	drv := spec.Root.Table

	_, _, err := WorstCaseRows(ctx, &countingSession{}, pl, spec, drv, customers, "nope")
	require.ErrorContains(t, err, "directly under the root")

	rows, wc, err := WorstCaseRows(ctx, &relatedSession{}, pl, spec, drv, customers, "rel_orders")
	require.NoError(t, err)
	require.Nil(t, rows, "a source that can't count has no worst case")
	require.Equal(t, &WorstCase{RelationID: "rel_orders", Path: "orders"}, wc)

	rows, wc, err = WorstCaseRows(ctx, &countingSession{}, pl, spec, drv, customers, "rel_orders")
	require.NoError(t, err)
	require.Nil(t, rows, "no parent has rows")
	require.Zero(t, wc.Rows)

	boom := errors.New("timeout")
	_, _, err = WorstCaseRows(ctx, &countingSession{topErr: boom}, pl, spec, drv, customers, "rel_orders")
	require.ErrorIs(t, err, boom)

	rs := &countingSession{topKey: []any{int32(7)}, topN: 1240, topFound: true}
	rs.rows = [][]any{{int32(7), "Ana"}, {int32(7), "dup"}}
	rows, wc, err = WorstCaseRows(ctx, rs, pl, spec, drv, customers, "rel_orders")
	require.NoError(t, err)
	require.Equal(t, [][]any{{int32(7), "Ana"}}, rows)
	require.Equal(t, int64(1240), wc.Rows)
	require.Equal(t, []string{"customer_id"}, rs.keysRead[0].Key, "read by the relation's parent join columns")
	require.Equal(t, [][]any{{int32(7)}}, rs.keys[0])
}
