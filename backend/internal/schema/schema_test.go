package schema

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func sample() *Snapshot {
	est := int64(10)
	return &Snapshot{Database: "shop", ServerVersion: "16.3", Schemas: []Schema{
		{Name: "shop", Tables: []Table{
			{
				Name: "orders", Kind: KindTable, RowEstimate: &est, SizeBytes: 100, Readable: true, PrimaryKey: []string{"id"},
				Columns: []Column{{Name: "total", Ordinal: 2}, {Name: "id", Ordinal: 1}},
			},
			{Name: "customer", Kind: KindView, StatsStale: true, Readable: false, Columns: []Column{{Name: "id", Ordinal: 1}}},
		}},
		{Name: "billing", Tables: []Table{{Name: "invoice", Kind: KindTable}}},
	}}
}

func TestSnapshot_LookupsAndCounts(t *testing.T) {
	s := sample()
	require.Equal(t, 3, s.TableCount())
	require.NotNil(t, s.Table("shop", "orders"))
	require.Nil(t, s.Table("shop", "invoice"), "wrong schema")
	require.Nil(t, s.Table("nope", "orders"))
	require.NotNil(t, s.Table("shop", "orders").Column("total"))
	require.Nil(t, s.Table("shop", "orders").Column("missing"))
}

func TestSnapshot_SortOrdersSchemasTablesAndColumns(t *testing.T) {
	s := sample()
	s.Sort()
	require.Equal(t, "billing", s.Schemas[0].Name)
	require.Equal(t, "customer", s.Schemas[1].Tables[0].Name)
	require.Equal(t, "id", s.Schemas[1].Tables[1].Columns[0].Name, "by ordinal")
}

func TestSnapshot_SummarizeDropsDetail(t *testing.T) {
	sum := sample().Summarize()
	require.Equal(t, "shop", sum.Database)
	orders := sum.Schemas[0].Tables[0]
	require.Equal(t, TableSummary{Name: "orders", Kind: KindTable, RowEstimate: orders.RowEstimate, SizeBytes: 100, ColumnCount: 2, HasPK: true, Readable: true}, orders)
	require.Equal(t, int64(10), *orders.RowEstimate)
	view := sum.Schemas[0].Tables[1]
	require.True(t, view.StatsStale)
	require.False(t, view.HasPK)
	b, err := json.Marshal(sum)
	require.NoError(t, err)
	require.NotContains(t, string(b), `"columns"`)
	require.Contains(t, string(b), `"hasPrimaryKey":true`)
}

func TestUniqueKey(t *testing.T) {
	tb := &Table{
		PrimaryKey:        []string{"cart_id"},
		UniqueConstraints: [][]string{{"store", "code"}},
		Indexes: []Index{
			{Name: "ix_pair", Columns: []string{"a", "b"}, Unique: true},
			{Name: "ix_expr", Columns: []string{"expr", "c"}, Unique: true},
			{Name: "ix_plain", Columns: []string{"d"}},
		},
		Columns: []Column{{Name: "email", IsUnique: true}, {Name: "d"}},
	}
	require.True(t, tb.UniqueKey([]string{"cart_id"}), "primary key")
	require.True(t, tb.UniqueKey([]string{"code", "store"}), "unique constraint, any order")
	require.True(t, tb.UniqueKey([]string{"b", "a"}), "unique index, any order")
	require.True(t, tb.UniqueKey([]string{"email"}), "single unique column")
	require.False(t, tb.UniqueKey([]string{"c"}), "expression index")
	require.False(t, tb.UniqueKey([]string{"d"}), "non-unique index")
	require.False(t, tb.UniqueKey([]string{"a"}), "subset of a unique index")
	require.False(t, tb.UniqueKey([]string{"cart_id", "d"}), "superset of the key")
	require.False(t, tb.UniqueKey(nil))
}
