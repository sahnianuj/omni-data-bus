package plan

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

func est(n int64) *int64 { return &n }

// strategySpec is customers with orders[] (auto), each order with lines[] (nested).
func strategySpec() (*mapping.Spec, *schema.Table, map[string]*schema.Table) {
	customers := &schema.Table{
		Name: "customers", PrimaryKey: []string{"customer_id"}, RowEstimate: est(1_000_000),
		Columns: []schema.Column{{Name: "customer_id", UDTName: "int8"}},
	}
	orders := &schema.Table{
		Name: "orders", PrimaryKey: []string{"order_id"}, RowEstimate: est(400_000_000),
		Columns: []schema.Column{{Name: "order_id", UDTName: "int8"}, {Name: "customer_id", UDTName: "int8"}},
		Indexes: []schema.Index{{Name: "orders_customer", Columns: []string{"customer_id", "order_id"}}},
	}
	lines := &schema.Table{
		Name: "lines", RowEstimate: est(4_000_000_000),
		Columns: []schema.Column{{Name: "order_id", UDTName: "int8"}, {Name: "n", UDTName: "int4"}},
		Indexes: []schema.Index{{Columns: []string{"order_id"}}},
	}
	s := &mapping.Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_orders", Name: "orders", Type: docschema.Array, Items: &docschema.DocField{ID: "f_o", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
				{ID: "f_lines", Name: "lines", Type: docschema.Array, Items: &docschema.DocField{ID: "f_l", Name: "[]", Type: docschema.Object}},
			}}},
		}}},
		Key: mapping.KeySpec{Template: "{customer_id}"},
		Relations: []mapping.Relation{
			{ID: "o", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "s", Name: "orders"}, Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, TargetFieldID: "f_orders"},
			{ID: "l", ParentID: ptr("o"), Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "s", Name: "lines"}, Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetFieldID: "f_lines"},
		},
	}
	return s, customers, map[string]*schema.Table{"orders": orders, "lines": lines}
}

func ptr(s string) *string { return &s }

func compileStrategy(t *testing.T, s *mapping.Spec, root *schema.Table, tables map[string]*schema.Table) map[string]RelationStep {
	t.Helper()
	pl, _, err := Compile(Input{
		Spec: s, Root: mapping.TableRef{Schema: "s", Name: root.Name}, Table: root,
		Related: func(_, n string) *schema.Table { return tables[n] },
	})
	require.NoError(t, err)
	out := map[string]RelationStep{}
	for _, st := range pl.Relations {
		out[st.Relation.ID] = st
	}
	for _, l := range pl.Lookups() {
		require.Equal(t, out[l.ID].Strategy, l.Strategy, "the resolver runs the plan's strategy")
	}
	return out
}

func TestCompile_AC1_StrategyAuto(t *testing.T) {
	// 1M parents, 400M children: sorted-merge.
	s, root, tables := strategySpec()
	steps := compileStrategy(t, s, root, tables)
	require.Equal(t, StrategySortedMerge, steps["o"].Strategy)
	require.Equal(t, "Reads orders alongside customers, sorted by customer_id.", steps["o"].StrategyNote)

	// A depth-2 relation with high fan-out stays batched, with an info message.
	require.Equal(t, StrategyBatched, steps["l"].Strategy)
	require.Contains(t, steps["l"].StrategyNote, "nested")

	// 1M parents, 3M children: batched.
	tables["orders"].RowEstimate = est(3_000_000)
	steps = compileStrategy(t, s, root, tables)
	require.Equal(t, StrategyBatched, steps["o"].Strategy)
	require.Empty(t, steps["o"].StrategyNote)

	// Over 50M children is enough on its own.
	tables["orders"].RowEstimate = est(60_000_000)
	root.RowEstimate = est(1_000_000_000)
	require.Equal(t, StrategySortedMerge, compileStrategy(t, s, root, tables)["o"].Strategy)

	// Without an index on the child's join column, auto stays batched and says why.
	tables["orders"].Indexes = nil
	steps = compileStrategy(t, s, root, tables)
	require.Equal(t, StrategyBatched, steps["o"].Strategy)
	require.Contains(t, steps["o"].StrategyNote, "orders.customer_id has no index")

	// An explicit batched strategy is kept.
	s, root, tables = strategySpec()
	s.Relations[0].Strategy = mapping.StrategyBatched
	require.Equal(t, StrategyBatched, compileStrategy(t, s, root, tables)["o"].Strategy)
	// Not joined on the root's primary key: can't merge.
	s, root, tables = strategySpec()
	root.PrimaryKey = []string{"email"}
	steps = compileStrategy(t, s, root, tables)
	require.Equal(t, StrategyBatched, steps["o"].Strategy)
	require.Contains(t, steps["o"].StrategyNote, "primary key")
}
