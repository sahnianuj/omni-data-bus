package jobs

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

func check(t *testing.T, res PreflightResult, id string) Check {
	t.Helper()
	for _, c := range res.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", id, res.Checks)
	return Check{}
}

func TestPreflight_AC2_ValidationErrorsBlock(t *testing.T) {
	f := newFixture(t)
	m := f.maps["customer"]
	f.issues[m.ID] = []mapping.Issue{{Severity: mapping.SeverityError, Code: "REQUIRED_FIELD_UNMAPPED", Message: "Required field email isn't mapped."}}
	res, err := f.pre.Run(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	require.False(t, res.OK)
	c := check(t, res, "mapping.shop.customer.valid")
	require.Equal(t, CheckFail, c.Status)
	require.Contains(t, c.Label, "shop.customer", "the failing mapping is named")
	require.Contains(t, c.Detail, "Required field email")

	// Creating the job refuses with PREFLIGHT_FAILED and the checklist.
	_, pre, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.True(t, problem.HasCode(err, problem.CodePreflightFailed))
	require.False(t, pre.OK)
	jobs, _, _ := f.st.Jobs().List(ctx, ws, "", store.Page{})
	require.Empty(t, jobs)
}

func TestPreflight_AC2_CreatesMissingKeyspace(t *testing.T) {
	f := newFixture(t)
	m := f.addMapping(t, "orders", "ready", true) // shop.sales.orders doesn't exist yet
	f.ks.scopes = map[string]bool{}               // nor does the scope
	res, err := f.pre.Run(ctx, ws, "u1", f.project.ID, Options{MappingIDs: []string{m.ID}, WriteMode: WriteUpsert})
	require.NoError(t, err)
	c := check(t, res, "mapping.shop.orders.keyspace")
	require.Equal(t, CheckOK, c.Status)
	require.Equal(t, []string{"scope shop.sales", "collection shop.sales.orders"}, f.ks.created)

	// Without createIfMissing it fails; a creation error fails too.
	f2 := newFixture(t)
	m2 := f2.addMapping(t, "orders", "ready", false)
	res, err = f2.pre.Run(ctx, ws, "u1", f2.project.ID, Options{MappingIDs: []string{m2.ID}})
	require.NoError(t, err)
	require.Equal(t, mapping.CodeTargetKeyspaceMissing, check(t, res, "mapping.shop.orders.keyspace").Code)
	require.False(t, res.OK)
	f3 := newFixture(t)
	m3 := f3.addMapping(t, "orders", "ready", true)
	f3.ks.createErr = errors.New("forbidden")
	res, _ = f3.pre.Run(ctx, ws, "u1", f3.project.ID, Options{MappingIDs: []string{m3.ID}})
	require.Equal(t, CheckFail, check(t, res, "mapping.shop.orders.keyspace").Status)
}

func TestPreflight_AC2_Warnings(t *testing.T) {
	f := newFixture(t)
	pv := f.addMapping(t, "page_view", "ready", true) // no PK, {$uuid} key
	f.ks.existing["shop.sales.customer"] = 1200       // not empty
	f.sess.exportErr = errors.New("cannot export a snapshot on a standby")
	opts := DefaultOptions()
	opts.WriteMode = WriteInsert
	opts.MappingIDs = []string{f.maps["customer"].ID, pv.ID}
	res, err := f.pre.Run(ctx, ws, "u1", f.project.ID, opts)
	require.NoError(t, err)
	require.True(t, res.OK, "warnings don't block: %+v", res.Checks)
	require.Equal(t, WarnNoResume, check(t, res, "mapping.shop.page_view.resume").Code)
	require.Equal(t, WarnKeyNotUnique, check(t, res, "mapping.shop.page_view.key").Code)
	require.Equal(t, WarnInsertExisting, check(t, res, "mapping.shop.customer.empty").Code)
	require.Equal(t, WarnNoSnapshotExport, check(t, res, "snapshot").Code)
	require.EqualValues(t, 7000, res.TotalRows)
	// Repository checks come first, warnings last.
	require.Equal(t, "source", res.Checks[0].ID)
	require.Equal(t, CheckWarn, res.Checks[len(res.Checks)-1].Status)

	// A job created despite warnings records them in its timeline.
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, opts)
	require.NoError(t, err)
	evs, _ := f.st.Jobs().ListEvents(ctx, ws, v.ID)
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	require.Contains(t, kinds, "warning")
}

func TestPreflight_AC2_UsesStoredProjectConnections(t *testing.T) {
	f := newFixture(t)
	f.testFail[store.ConnectionID(f.project.ID, store.RoleSource)] = true
	res, err := f.pre.Run(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Equal(t, "SOURCE_UNREACHABLE", check(t, res, "source").Code)
	for _, id := range f.connCalls {
		require.Contains(t, []string{store.ConnectionID(f.project.ID, store.RoleSource), store.ConnectionID(f.project.ID, store.RoleTarget)}, id,
			"only the project's own repositories are used")
	}
	require.NotEmpty(t, f.connCalls)
}

func TestPreflight_UnreadableAndNoReadyMappings(t *testing.T) {
	f := newFixture(t)
	sec := f.addMapping(t, "secret", "ready", true)
	res, err := f.pre.Run(ctx, ws, "u1", f.project.ID, Options{MappingIDs: []string{sec.ID}})
	require.NoError(t, err)
	require.Equal(t, CheckFail, check(t, res, "mapping.shop.secret.table").Status)

	g := newFixture(t)
	c := g.maps["customer"]
	c.Status = "draft"
	_, err = g.st.Mappings().Update(ctx, ws, c)
	require.NoError(t, err)
	_, err = g.pre.Run(ctx, ws, "u1", g.project.ID, DefaultOptions())
	require.True(t, problem.HasCode(err, problem.CodeNoReadyMappings))
}
