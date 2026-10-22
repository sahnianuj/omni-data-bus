package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
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

	// Creating the job refuses with MAPPING_INVALID listing the issue, and the checklist (OC-049 AC6).
	_, pre, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.True(t, problem.HasCode(err, problem.CodeMappingInvalid))
	require.Contains(t, problem.From(err).FieldErrors[0].Message, "Required field email")
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

func TestCreate_OC037_RecordsPlanHashes(t *testing.T) {
	f := newFixture(t)
	f.svc.PlanHash = func(_ context.Context, _ store.WorkspaceID, m store.Mapping) (string, error) {
		if m.SourceTable == "page_view" {
			return "", errors.New("no snapshot")
		}
		return "hash-" + m.SourceTable, nil
	}
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	j, err := f.st.Jobs().Get(ctx, ws, v.ID)
	require.NoError(t, err)
	opts, err := ParseOptions(j.Options)
	require.NoError(t, err)
	require.Equal(t, map[string]string{f.maps["customer"].ID: "hash-customer"}, opts.PlanHashes)
}

// OC-170: two mappings of one run writing the same keys into the same place are a warning.
func TestPreflight_OC170_KeyOverlap(t *testing.T) {
	loc := store.Location{Kind: store.KindCouchbase, Bucket: "shop", Scope: "pos", Collection: "carts"}
	m := func(id, table, key string, l store.Location) store.Mapping {
		x := store.Mapping{ID: id, SourceTable: table, Spec: json.RawMessage(`{"key":{"template":"` + key + `"}}`)}
		x.SetLocation(l)
		return x
	}
	other := loc
	other.Collection = "carts_v2"
	got := keyOverlapChecks([]store.Mapping{m("a", "carts", "cart::{id}", loc), m("b", "cart_view", "cart::{id}", loc), m("c", "x", "cart::{id}", other), m("d", "y", "order::{id}", loc)})
	require.Len(t, got, 1)
	require.Equal(t, CheckWarn, got[0].Status)
	require.Equal(t, "KEY_OVERLAP", got[0].Code)
	require.Contains(t, got[0].Detail, "carts and cart_view both write cart::{id} keys")
}

// downTarget makes the target's connection test fail as the Couchbase connector does: no
// permissions reported, and a plain detail with a hint (OC-190).
func downTarget(f *fixture) {
	tgt := store.ConnectionID(f.project.ID, store.RoleTarget)
	test := f.pre.TestConn
	f.pre.TestConn = func(ctx context.Context, w store.WorkspaceID, u, id string) (connectors.TestResult, error) {
		if id != tgt {
			return test(ctx, w, u, id)
		}
		return connectors.TestResult{OK: false, Capabilities: json.RawMessage(`{"canWriteDocs":false}`), Steps: []connectors.TestStep{{
			ID: "bootstrap", Status: connectors.StepFail, Code: "TARGET_TIMEOUT", Detail: "No answer from cb:11210 within 5s.",
			Hint: "Check the host and port.", Cause: "unambiguous timeout",
		}}}, nil
	}
}

func TestPreflight_OC190_AC1_TargetDownSkipsTargetChecks(t *testing.T) {
	f := newFixture(t)
	downTarget(f)
	f.pre.Target = func(context.Context, store.WorkspaceID, string, []target.PreflightLocation) ([]target.LocationPreflight, error) {
		t.Fatal("the target can't be reached: its location checks must not connect again")
		return nil, nil
	}
	res, err := f.pre.Run(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	require.False(t, res.OK)
	tc := check(t, res, "target")
	require.Equal(t, CheckFail, tc.Status)
	require.Equal(t, "No answer from cb:11210 within 5s.", tc.Detail)
	require.Equal(t, "Check the host and port.", tc.Hint)
	require.Equal(t, "unambiguous timeout", tc.Cause)
	ks := check(t, res, "mapping.shop.customer.keyspace")
	require.Equal(t, CheckSkipped, ks.Status)
	require.Equal(t, "Not checked: the target isn't reachable.", ks.Detail)
}

func TestPreflight_OC190_AC3_NoWriteCheckWhenUnreachable(t *testing.T) {
	f := newFixture(t)
	downTarget(f)
	res, err := f.pre.Run(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	for _, c := range res.Checks {
		require.NotEqual(t, "target.write", c.ID, "an unreachable target isn't a permission problem")
	}

	// Reachable, but the credential can't write: still reported.
	g := newFixture(t)
	g.pre.TestConn = func(context.Context, store.WorkspaceID, string, string) (connectors.TestResult, error) {
		return connectors.TestResult{OK: true, Capabilities: json.RawMessage(`{"canWriteDocs":false}`)}, nil
	}
	res, err = g.pre.Run(ctx, ws, "u1", g.project.ID, DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, "TARGET_PERMISSION_DENIED", check(t, res, "target.write").Code)
}

func TestPreflight_OC190_SourceDownSkipsSnapshot(t *testing.T) {
	f := newFixture(t)
	f.testFail[store.ConnectionID(f.project.ID, store.RoleSource)] = true
	f.pre.OpenSource = func(context.Context, store.WorkspaceID, string) (source.Session, store.Connection, error) {
		t.Fatal("the source can't be reached: the snapshot check must not connect again")
		return nil, store.Connection{}, nil
	}
	res, err := f.pre.Run(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, CheckSkipped, check(t, res, "snapshot").Status)
}
