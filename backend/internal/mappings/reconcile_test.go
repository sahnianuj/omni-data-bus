package mappings

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
)

func TestReplaceDesign_Reconcile(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	mapped := map[string]string{} // field name → mapped column
	for _, m := range v.Spec.FieldMappings {
		if m.Active() {
			mapped[v.Spec.DocSchema.Find(m.TargetFieldID).Name] = m.Sources[0].Column
		}
	}
	require.Equal(t, "first_name", mapped["firstName"])

	// The customer's revision: firstName → first_name (a rename), ageText gone, phone added.
	incoming, _, err := docschema.Import(docschema.KindJSONSample, `{"customerId":1,"first_name":"Ann","emailAddress":"a@x","phone":"555"}`, "rev.json")
	require.NoError(t, err)
	res, err := f.svc.ReplaceDesign(ctx, ws, "u1", f.project, v.ID, v.Version, DesignInput{DocSchema: incoming, Strategy: mapping.StrategyReconcile})
	require.NoError(t, err)
	require.Equal(t, []docschema.DiffChange{{FieldID: v.Spec.DocSchema.Root.Children[1].ID, From: "firstName", To: "first_name"}}, res.Diff.Renamed)
	require.Len(t, res.Orphans, 1, "ageText's mapping")
	got := map[string]string{}
	for _, m := range res.Mapping.Spec.FieldMappings {
		if m.Active() {
			got[res.Mapping.Spec.DocSchema.Find(m.TargetFieldID).Name] = m.Sources[0].Column
		}
	}
	require.Equal(t, "first_name", got["first_name"], "the renamed field keeps its mapping")
	require.NotContains(t, got, "phone", "added fields stay unmapped")
	require.Equal(t, "rev.json", res.Mapping.Spec.DocSchema.Imported.FileName)

	// Declined: first_name is a new, unmapped field.
	f2 := newFixture(t, true)
	v2 := f2.create(t, "customer")
	res2, err := f2.svc.ReplaceDesign(ctx, ws, "u1", f2.project, v2.ID, v2.Version, DesignInput{DocSchema: incoming, Strategy: mapping.StrategyReconcile, Declined: []string{v2.Spec.DocSchema.Root.Children[1].ID}})
	require.NoError(t, err)
	require.Empty(t, res2.Diff.Renamed)
	require.Len(t, res2.Orphans, 2)
}

func TestMappingSheet_Service(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	title, rows, err := f.svc.MappingSheet(ctx, ws, f.project, v.ID)
	require.NoError(t, err)
	require.Equal(t, "shop.customer", title)
	require.Equal(t, "customerId", rows[0].Path)
	require.Equal(t, "customer.customer_id", rows[0].Source)
	require.Equal(t, mapping.SheetMapped, rows[0].Status)
}
