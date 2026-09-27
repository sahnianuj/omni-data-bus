package pipeline

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/jobs"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/store"
)

// resumeFrom points the fixture's job at a stored checkpoint, as a worker claiming it would.
func (f *runFixture) resumeFrom(t *testing.T, cp Checkpoint) {
	t.Helper()
	raw, err := json.Marshal(cp)
	require.NoError(t, err)
	f.job.Checkpoint = raw
}

// addMapping adds a second table (same shape as people) to the project and the job.
func (f *runFixture) addMapping(t *testing.T, table string) store.Mapping {
	t.Helper()
	raw, _ := json.Marshal(peopleSpec())
	m, err := f.st.Mappings().Create(context.Background(), ws, store.Mapping{
		ProjectID: f.project.ID, SourceSchema: "hr", SourceTable: table, Spec: raw, Status: "ready",
		Target: store.MappingTarget{Bucket: "b", Scope: "s", Collection: table},
	})
	require.NoError(t, err)
	tb := peopleTable()
	tb.Name = table
	f.tables = append(f.tables, *tb)
	f.job.MappingIDs = append(f.job.MappingIDs, m.ID)
	return m
}

func TestResume_AC3_ReaderStartsAfterLastKey(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 3000), 3000, opts(100))
	f.resumeFrom(t, Checkpoint{Mappings: map[string]*MappingCheckpoint{f.job.MappingIDs[0]: {
		Partitions: []PartitionState{
			{Hi: int64(1501), Seq: 4, LastKey: []any{int64(402)}},
			{Lo: int64(1501), Seq: 15, LastKey: []any{int64(3000)}, Done: true},
		},
		Written: 402 + 1500,
	}}})
	o := f.run(jobs.Control{})
	require.True(t, o.Finished)
	require.Len(t, f.src.starts, 1, "only the unfinished partition is read")
	require.Equal(t, []any{int64(402)}, f.src.starts[0].after, "continues after the checkpoint's last key")
	require.Nil(t, f.src.starts[0].lo)
	require.Equal(t, int64(1501), f.src.starts[0].hi, "the partition plan is reused as is")
	require.Equal(t, 1500-402, f.w.count(), "exactly rows 403..1500")
	_, written := f.w.docs["person::402"]
	require.False(t, written)
	_, written = f.w.docs["person::403"]
	require.True(t, written)
}

func TestResume_AC3_SkipsDonePartitionsAndMappings(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 1000), 1000, opts(100))
	archive := f.addMapping(t, "people_archive")
	f.resumeFrom(t, Checkpoint{Mappings: map[string]*MappingCheckpoint{
		f.job.MappingIDs[0]: {Partitions: []PartitionState{{Seq: 3, LastKey: []any{int64(300)}}}, Written: 300},
		archive.ID:          {Partitions: []PartitionState{{Seq: 10, LastKey: []any{int64(1000)}, Done: true}}, Written: 1000, Done: true},
	}})
	o := f.run(jobs.Control{})
	require.True(t, o.Finished)
	require.Equal(t, 1, f.src.opened, "the finished mapping isn't read again")
	require.Len(t, f.writeOpts, 1, "nor written")
	require.Equal(t, "people", f.writeOpts[0].Collection)
	require.Equal(t, 700, f.w.count())
	cp, _ := ParseCheckpoint(o.Checkpoint)
	require.True(t, cp.Mappings[archive.ID].Done)
	require.True(t, cp.Mappings[f.job.MappingIDs[0]].Done)
}

func TestResume_AC3_CountersRestored(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 2000), 2000, opts(100))
	f.resumeFrom(t, Checkpoint{Mappings: map[string]*MappingCheckpoint{f.job.MappingIDs[0]: {
		Partitions: []PartitionState{{Seq: 12, LastKey: []any{int64(1200)}}}, Written: 1190, Skipped: 4, Failed: 6,
	}}})
	o := f.run(jobs.Control{})
	require.True(t, o.Finished)
	snap := progress.Parse(f.job.ID, o.Progress)
	require.EqualValues(t, 1190+800, snap.Written, "continues from the stored count")
	require.EqualValues(t, 4, snap.Skipped)
	require.EqualValues(t, 6, snap.Failed)
	evs, err := f.st.Jobs().ListEvents(context.Background(), ws, f.job.ID)
	require.NoError(t, err)
	require.True(t, hasEvent(evs, "resumed", "Resumed from checkpoint (1,200 rows already processed)"))
}

func TestResume_AC6_InsertReplayAccounting(t *testing.T) {
	o := opts(500)
	o.WriteMode = jobs.WriteInsert
	f := newRunFixture(t, peopleRows(1, 3000), 3000, o)
	f.w.insert = true
	// Rows 1..1000 were written just before the crash, after the last persisted checkpoint;
	// row 2600 already existed for an unrelated reason.
	for id := 1; id <= 1000; id++ {
		f.w.docs[keyFor(id)] = []byte(`{}`)
	}
	f.w.docs[keyFor(2600)] = []byte(`{}`)
	f.resumeFrom(t, Checkpoint{Mappings: map[string]*MappingCheckpoint{f.job.MappingIDs[0]: {Partitions: []PartitionState{{}}}}})
	out := f.run(jobs.Control{})
	require.True(t, out.Finished)
	snap := progress.Parse(f.job.ID, out.Progress)
	require.EqualValues(t, 2999, snap.Written, "the 1,000 replayed rows in the first 2 batches count as written")
	require.EqualValues(t, 1, snap.Skipped, "skipped isn't inflated; a genuine exists later still counts")
	evs, _ := f.st.Jobs().ListEvents(context.Background(), ws, f.job.ID)
	require.True(t, hasEvent(evs, "replayed", "1,000 rows written again"))
}

func TestResume_AC7_UsesFrozenSpec(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 300), 300, opts(100))
	frozen := peopleSpec()
	frozen.Key.Template = "frozen::{id}"
	raw, _ := json.Marshal(frozen)
	o := opts(100)
	o.MappingIDs = f.job.MappingIDs
	o.Specs = map[string]json.RawMessage{f.job.MappingIDs[0]: raw}
	f.job.Options, _ = json.Marshal(o)
	require.True(t, f.run(jobs.Control{}).Finished)
	require.Equal(t, 300, f.w.count())
	for k := range f.w.docs {
		require.True(t, strings.HasPrefix(k, "frozen::"), "the stored mapping (person::) was edited after the job started: %s", k)
	}
}

func TestResume_AC7_NoPKRestarts(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 500), 500, opts(100))
	f.tables[0].PrimaryKey = nil
	f.tables[0].Columns[0].IsPrimaryKey = false
	f.tables[0].Columns[0].Nullable = true
	// A keyless table was part-way through when the job stopped.
	f.resumeFrom(t, Checkpoint{Mappings: map[string]*MappingCheckpoint{f.job.MappingIDs[0]: {
		Partitions: []PartitionState{{Seq: 2}}, Cursor: true, Written: 200,
	}}})
	o := f.run(jobs.Control{})
	require.True(t, o.Finished)
	require.Equal(t, 500, f.w.count(), "re-read from the beginning")
	snap := progress.Parse(f.job.ID, o.Progress)
	require.EqualValues(t, 500, snap.Written, "counts restart with the table")
	evs, _ := f.st.Jobs().ListEvents(context.Background(), ws, f.job.ID)
	require.True(t, hasEvent(evs, "warning", CodeMappingRestartedNoPK))
}

func keyFor(id int) string { return "person::" + strconv.Itoa(id) }

func hasEvent(evs []store.JobEvent, kind, contains string) bool {
	for _, e := range evs {
		if e.Kind == kind && strings.Contains(e.Message, contains) {
			return true
		}
	}
	return false
}
