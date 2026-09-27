package jobs

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// pausedJob creates a job and moves it to paused with the given checkpoint.
func pausedJob(t *testing.T, f *fixture, checkpoint string) View {
	t.Helper()
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	j, _ := f.st.Jobs().Get(ctx, ws, v.ID)
	j.Status, j.Checkpoint = store.JobPaused, json.RawMessage(checkpoint)
	_, err = f.st.Jobs().Update(ctx, j)
	require.NoError(t, err)
	v, err = f.svc.Get(ctx, ws, v.ID)
	require.NoError(t, err)
	return v
}

func editMapping(t *testing.T, f *fixture, table, template string) {
	t.Helper()
	m, err := f.st.Mappings().Get(ctx, ws, f.maps[table].ID)
	require.NoError(t, err)
	m.Spec, _ = json.Marshal(mapping.Spec{Key: mapping.KeySpec{Template: template}})
	_, err = f.st.Mappings().Update(ctx, ws, m)
	require.NoError(t, err)
}

func TestResume_AC7_UsesFrozenSpec(t *testing.T) {
	f := newFixture(t)
	id := f.maps["customer"].ID
	v := pausedJob(t, f, `{"mappings":{}}`)
	require.Nil(t, v.Options.Specs, "frozen specs stay server-side")
	j, _ := f.st.Jobs().Get(ctx, ws, v.ID)
	stored, _ := ParseOptions(j.Options)
	require.JSONEq(t, string(f.maps["customer"].Spec), string(stored.Specs[id]), "the spec is frozen at creation")
	require.Equal(t, f.maps["customer"].Version, stored.MappingVersions[id])
	require.Empty(t, v.ResumeInfo.Changed)

	// The mapping is edited while the job is paused: the dialog is told, and a plain resume
	// keeps running the frozen copy.
	editMapping(t, f, "customer", "cust::{customer_id}")
	v, _ = f.svc.Get(ctx, ws, v.ID)
	require.Equal(t, []MappingRef{{MappingID: id, Table: "shop.customer"}}, v.ResumeInfo.Changed)
	r, err := f.svc.Resume(ctx, ws, "u1", v.ID, ResumeOriginal)
	require.NoError(t, err)
	require.Equal(t, store.JobQueued, r.Status)
	j, _ = f.st.Jobs().Get(ctx, ws, v.ID)
	stored, _ = ParseOptions(j.Options)
	require.Contains(t, string(stored.Specs[id]), "customer::{customer_id}", "still the original spec")
}

func TestResume_AC7_RestartWithNewMapping(t *testing.T) {
	f := newFixture(t)
	id := f.maps["customer"].ID
	v := pausedJob(t, f, `{"mappings":{}}`)
	editMapping(t, f, "customer", "cust::{customer_id}")

	n, err := f.svc.Resume(ctx, ws, "u1", v.ID, ResumeRestart)
	require.NoError(t, err)
	require.NotEqual(t, v.ID, n.ID, "a new job")
	require.Equal(t, store.JobQueued, n.Status)
	j, _ := f.st.Jobs().Get(ctx, ws, n.ID)
	stored, _ := ParseOptions(j.Options)
	require.Contains(t, string(stored.Specs[id]), "cust::{customer_id}", "runs the current mapping")
	require.Empty(t, j.Checkpoint, "from the beginning")
	old, _ := f.svc.Get(ctx, ws, v.ID)
	require.Equal(t, store.JobCancelled, old.Status)

	_, err = f.svc.Resume(ctx, ws, "u1", n.ID, "sideways")
	require.True(t, problem.HasCode(err, problem.CodeJobInvalidTransition) || problem.HasCode(err, problem.CodeBadRequest))
}

func TestResume_AC7_NoPKRestarts(t *testing.T) {
	f := newFixture(t)
	id := f.maps["customer"].ID
	v := pausedJob(t, f, `{"mappings":{"`+id+`":{"cursor":true,"written":500,"partitions":[{"seq":1}]}}}`)
	require.Equal(t, []MappingRef{{MappingID: id, Table: "shop.customer"}}, v.ResumeInfo.Restart, "a keyless table is re-read")
	require.Zero(t, v.ResumeInfo.RowsDone, "its rows don't count as done")

	v = pausedJob(t, newFixture(t), `{"mappings":{"x":{"written":400,"skipped":2,"failed":1}}}`)
	require.Empty(t, v.ResumeInfo.Restart)
}

func TestResume_ResumeInfoCountsRows(t *testing.T) {
	f := newFixture(t)
	id := f.maps["customer"].ID
	v := pausedJob(t, f, `{"mappings":{"`+id+`":{"written":400,"skipped":2,"failed":1,"partitions":[{"seq":4}]}}}`)
	require.EqualValues(t, 403, v.ResumeInfo.RowsDone)
	running, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.Error(t, err, "the paused job still holds the project")
	require.Nil(t, running.ResumeInfo)
}
