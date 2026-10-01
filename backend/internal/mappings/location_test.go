package mappings

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// directoryProject turns the fixture's target into a directory (OC-036).
func directoryProject(t *testing.T, f *fixture) {
	t.Helper()
	_, err := f.st.Connections().Upsert(ctx, ws, f.project, store.RoleTarget, store.ConnectionInput{Type: store.KindDirectory, Config: json.RawMessage(`{"path":"/data"}`)})
	require.NoError(t, err)
}

func TestCreate_OC036_DirectoryLocations(t *testing.T) {
	f := newFixture(t, true)
	directoryProject(t, f)
	// No location: the folder defaults to the table name.
	v, err := f.svc.Create(ctx, ws, "u1", f.project, CreateInput{SourceSchema: "shop", SourceTable: "customer"})
	require.NoError(t, err)
	require.Equal(t, mapping.Location{Kind: "directory", Folder: "customer"}, v.Location)
	require.Equal(t, mapping.Target{}, v.Target, "no 1.0 target for a directory")

	// A 1.0 Couchbase target doesn't apply to a directory project.
	f2 := newFixture(t, true)
	directoryProject(t, f2)
	_, err = f2.svc.Create(ctx, ws, "u1", f2.project, CreateInput{SourceSchema: "shop", SourceTable: "customer", Target: target1()})
	require.True(t, problem.HasCode(err, problem.CodeLocationInvalid))
	// Nor does a bad folder.
	_, err = f2.svc.Create(ctx, ws, "u1", f2.project, CreateInput{SourceSchema: "shop", SourceTable: "customer", Location: &mapping.Location{Folder: "../etc"}})
	require.True(t, problem.HasCode(err, problem.CodeLocationInvalid))

	// Update moves it to another folder; a Couchbase location is refused.
	got, err := f.svc.Update(ctx, ws, "u1", f.project, v.ID, 0, UpdateInput{Spec: v.Spec, Location: &mapping.Location{Folder: "archive/customers"}})
	require.NoError(t, err)
	require.Equal(t, "archive/customers", got.Location.Folder)
	_, err = f.svc.Update(ctx, ws, "u1", f.project, v.ID, 0, UpdateInput{Spec: v.Spec, Location: &mapping.Location{Kind: "couchbase", Bucket: "b", Scope: "s", Collection: "c"}})
	require.True(t, problem.HasCode(err, problem.CodeLocationInvalid))
	// A Couchbase project still takes a 1.0 target on update, and reports it as a location.
	f3 := newFixture(t, true)
	c := f3.create(t, "customer")
	got, err = f3.svc.Update(ctx, ws, "u1", f3.project, c.ID, 0, UpdateInput{Spec: c.Spec, Target: &mapping.Target{Bucket: "b", Scope: "s", Collection: "c2"}})
	require.NoError(t, err)
	require.Equal(t, mapping.Location{Kind: "couchbase", Bucket: "b", Scope: "s", Collection: "c2"}, got.Location)
}

func TestValidate_OC036_StatLocationPort(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	var asked store.Location
	f.svc.StatLocation = func(_ context.Context, _ store.WorkspaceID, _ string, loc store.Location) (target.LocationNode, error) {
		asked = loc
		return target.LocationNode{}, problem.New(http.StatusNotFound, problem.CodeNotFound, "Not found", "")
	}
	res, err := f.svc.Validate(ctx, ws, f.project, v.ID)
	require.NoError(t, err)
	require.Equal(t, "customer", asked.Collection)
	var codes []string
	for _, i := range res.Issues {
		codes = append(codes, i.Code)
	}
	require.Contains(t, codes, mapping.CodeTargetKeyspaceMissing)

	// A target that can't be asked leaves the location unchecked.
	f.svc.StatLocation = func(context.Context, store.WorkspaceID, string, store.Location) (target.LocationNode, error) {
		return target.LocationNode{}, problem.New(http.StatusBadGateway, problem.CodeUnavailable, "Down", "")
	}
	res, err = f.svc.Validate(ctx, ws, f.project, v.ID)
	require.NoError(t, err)
	for _, i := range res.Issues {
		require.NotEqual(t, mapping.CodeTargetKeyspaceMissing, i.Code)
	}
}

func TestPlanHash_OC037(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	m, err := f.st.Mappings().Get(ctx, ws, v.ID)
	require.NoError(t, err)
	h1, err := f.svc.PlanHash(ctx, ws, m)
	require.NoError(t, err)
	require.Len(t, h1, 64)
	h2, _ := f.svc.PlanHash(ctx, ws, m)
	require.Equal(t, h1, h2)

	m.SourceTable = "gone"
	_, err = f.svc.PlanHash(ctx, ws, m)
	require.True(t, problem.HasCode(err, problem.CodeNotFound) || err != nil)
	m.Spec = json.RawMessage(`{"fieldMappings":"x"}`)
	_, err = f.svc.PlanHash(ctx, ws, m)
	require.Error(t, err)
}
