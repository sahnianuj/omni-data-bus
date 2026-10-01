package mappings

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
)

// withOrders adds shop.orders referencing customer to the fixture's snapshot.
func withOrders(f *fixture) {
	snap := shopSnapshot()
	tables := snap.Schemas[0].Tables
	tables[0].ReferencedBy = []schema.Reference{{Schema: "shop", Table: "orders", Name: "orders_customer_fk", Columns: []string{"customer_id"}, RefColumns: []string{"customer_id"}}}
	tables = append(tables, schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}, RowEstimate: est(10000), Readable: true, Columns: []schema.Column{
		{Name: "order_id", Ordinal: 1, UDTName: "int8", JSONType: schema.TypeInteger, IsPrimaryKey: true},
		{Name: "customer_id", Ordinal: 2, UDTName: "int4", JSONType: schema.TypeInteger},
		{Name: "total", Ordinal: 3, UDTName: "numeric", JSONType: schema.TypeNumber},
	}})
	snap.Schemas[0].Tables = tables
	f.schemas.snap = snap
}

func TestAddEmbed_ServiceViewAndStats(t *testing.T) {
	f := newFixture(t, true)
	withOrders(f)
	v, err := f.svc.Create(ctx, ws, "u1", f.project, CreateInput{SourceSchema: "shop", SourceTable: "customer"})
	require.NoError(t, err)
	require.Len(t, v.EmbedCandidates, 1, "orders references customer")
	c := v.EmbedCandidates[0]
	require.Equal(t, mapping.EmbedOneToMany, c.Kind)
	require.InDelta(t, 5, c.RowsPerParent, 0.001)

	v, err = f.svc.AddEmbed(ctx, ws, "u1", f.project, v.ID, v.Version, automap.EmbedInput{
		Kind: c.Kind, Table: c.Table, Join: c.Join, TargetName: "orders", Generate: true,
	})
	require.NoError(t, err)
	require.Len(t, v.Spec.Relations, 1)
	e := v.Spec.Relations[0]
	require.Equal(t, e.ID, v.EmbedCandidates[0].EmbedID, "the candidate shows as embedded")
	require.Len(t, v.EmbedTables[e.ID].Columns, 3)
	require.Equal(t, &mapping.EmbeddedStats{Tables: 1, TotalColumns: 2, AutoMapped: 2}, v.Stats.Embedded)
	for _, i := range v.Issues {
		require.NotEqual(t, mapping.SeverityError, i.Severity, "%s %s", i.Code, i.Message)
	}

	// The saved spec round-trips through Update (embedded sources are known columns).
	got, err := f.svc.Update(ctx, ws, "u1", f.project, v.ID, v.Version, UpdateInput{Spec: v.Spec})
	require.NoError(t, err)
	require.Len(t, got.Spec.Relations, 1)

	// Unknown table or parent column: clear errors.
	_, err = f.svc.AddEmbed(ctx, ws, "u1", f.project, v.ID, 0, automap.EmbedInput{Kind: c.Kind, Table: mapping.TableRef{Schema: "shop", Name: "nope"}, Join: c.Join})
	require.True(t, problem.HasCode(err, problem.CodeNotFound))
	_, err = f.svc.AddEmbed(ctx, ws, "u1", f.project, v.ID, 0, automap.EmbedInput{Kind: c.Kind, Table: c.Table, Join: []mapping.JoinPair{{Parent: "cust", Child: "customer_id"}}})
	require.True(t, problem.HasCode(err, problem.CodeValidation))
}
