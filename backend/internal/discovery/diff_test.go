package discovery

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/schema"
)

func table(name string, cols ...schema.Column) schema.Table {
	return schema.Table{Name: name, Kind: schema.KindTable, Columns: cols}
}

func column(name, dataType string) schema.Column {
	return schema.Column{Name: name, DataType: dataType, UDTName: dataType}
}

func TestDiff_AC7_DetectsAddRemoveTypeChange(t *testing.T) {
	prev := &schema.Snapshot{Schemas: []schema.Schema{{Name: "public", Tables: []schema.Table{
		table("x", column("id", "integer")),
		table("y", column("id", "integer")),
		table("z", column("id", "integer"), column("col", "integer"), column("gone", "text")),
	}}}}
	next := &schema.Snapshot{Schemas: []schema.Schema{{Name: "public", Tables: []schema.Table{
		table("x", column("id", "integer"), column("added", "text")),
		table("z", column("id", "integer"), column("col", "bigint")),
		table("w", column("id", "integer")),
	}}}}
	d := Compare(prev, next)
	require.Equal(t, []string{"public.w"}, d.AddedTables)
	require.Equal(t, []string{"public.y"}, d.RemovedTables)
	require.Equal(t, []TableChange{
		{Table: "public.x", AddedColumns: []string{"added"}, RemovedColumns: []string{}, TypeChanged: []TypeChange{}},
		{Table: "public.z", AddedColumns: []string{}, RemovedColumns: []string{"gone"}, TypeChanged: []TypeChange{{Column: "col", From: "integer", To: "bigint"}}},
	}, d.ChangedTables)
	require.False(t, d.Empty())
	require.Equal(t, 4, d.TablesChanged())
}

func TestDiff_AC7_NoChangesEmpty(t *testing.T) {
	snap := &schema.Snapshot{Schemas: []schema.Schema{{Name: "shop", Tables: []schema.Table{table("a", column("id", "integer"))}}}}
	d := Compare(snap, snap)
	require.True(t, d.Empty())
	require.Equal(t, 0, d.TablesChanged())
	require.NotNil(t, d.AddedTables, "lists are [] in JSON, never null")
	require.NotNil(t, d.ChangedTables)

	// Same table name in different schemas are different tables.
	other := &schema.Snapshot{Schemas: []schema.Schema{{Name: "billing", Tables: []schema.Table{table("a", column("id", "integer"))}}}}
	d = Compare(snap, other)
	require.Equal(t, []string{"billing.a"}, d.AddedTables)
	require.Equal(t, []string{"shop.a"}, d.RemovedTables)
	require.Equal(t, 2, Compare(nil, snap).TablesChanged()+Compare(snap, nil).TablesChanged())
}
