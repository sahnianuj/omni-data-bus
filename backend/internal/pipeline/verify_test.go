package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/jobs"
	"omnidatabus/internal/store"
	"omnidatabus/internal/verify"
)

func storedReport(t *testing.T, f *runFixture) verify.MappingResult {
	t.Helper()
	raw, err := f.st.Jobs().GetReport(context.Background(), f.job.ID)
	require.NoError(t, err)
	return verify.ParseReport(raw).Mappings[f.job.MappingIDs[0]]
}

func TestVerify_RunVerifiesAndStoresTheResult(t *testing.T) {
	o := opts(100)
	o.TargetEmpty = map[string]bool{}
	f := newRunFixture(t, peopleRows(1, 3000, 11), 3000, o)
	var counted int
	f.runner.d.CountTarget = func(context.Context, store.WorkspaceID, string, store.MappingTarget) (int64, error) {
		counted++
		return int64(f.w.count()), nil
	}
	f.job.Options = mustJSON(func() jobs.Options {
		x := o
		x.MappingIDs = f.job.MappingIDs
		x.TargetEmpty = map[string]bool{f.job.MappingIDs[0]: true}
		return x
	}())
	out := f.run(jobs.Control{})
	require.True(t, out.Finished)
	r := storedReport(t, f)
	require.Equal(t, verify.Passed, r.Result, "%+v", r)
	require.True(t, r.Counts.Reconciled)
	require.EqualValues(t, 3000, r.Counts.SourceRows)
	require.EqualValues(t, 2999, r.Counts.Written)
	require.InDelta(t, 2999, float64(*r.Counts.DistinctKeys), 60)
	require.Equal(t, 1, counted, "the collection was empty at the start: its count is compared")
	require.True(t, *r.Counts.TargetCountMatch)
	require.Equal(t, 100, r.Sample.Checked)
	require.Equal(t, 100, r.Sample.Matched)
	require.Positive(t, r.AvgDocBytes)
	evs, _ := f.st.Jobs().ListEvents(context.Background(), ws, f.job.ID)
	require.True(t, hasEvent(evs, "verified", "Verification passed: 100/100 samples match"))
}

func TestVerify_DetectsChangedAndMissingDocuments(t *testing.T) {
	o := opts(100)
	o.VerifySampleSize = 3000 // every row
	f := newRunFixture(t, peopleRows(1, 3000), 3000, o)
	// Once the table is written (the last batch), someone edits one document and deletes another.
	f.w.onWrite = func(stored int) {
		if stored == 3000 {
			f.w.mu.Lock()
			f.w.docs["person::7"] = []byte(`{"id":7,"name":"changed","age":27}`)
			delete(f.w.docs, "person::8")
			f.w.mu.Unlock()
		}
	}
	require.True(t, f.run(jobs.Control{}).Finished)
	r := storedReport(t, f)
	require.Equal(t, verify.Failed, r.Result)
	require.Equal(t, 1, r.Sample.Missing)
	require.Equal(t, 1, r.Sample.Mismatched)
	require.Equal(t, "$.name", r.Sample.Mismatches[0].Diffs[0].Path)
}

func TestVerify_OffWhenSampleSizeZero(t *testing.T) {
	o := opts(100)
	o.VerifySampleSize = 0
	f := newRunFixture(t, peopleRows(1, 300), 300, o)
	require.True(t, f.run(jobs.Control{}).Finished)
	_, err := f.st.Jobs().GetReport(context.Background(), f.job.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestVerify_ResumedRunIsPartialAndKeyless(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 500), 500, opts(100))
	f.resumeFrom(t, Checkpoint{Mappings: map[string]*MappingCheckpoint{f.job.MappingIDs[0]: {Partitions: []PartitionState{{Seq: 2, LastKey: []any{int64(200)}}}, Written: 200}}})
	require.True(t, f.run(jobs.Control{}).Finished)
	r := storedReport(t, f)
	require.Equal(t, []string{verify.CodePartial}, r.Counts.Warnings)
	require.EqualValues(t, 500, r.Counts.SourceRows, "rows from before the resume count too")
	require.Equal(t, verify.Passed, r.Result)

	g := newRunFixture(t, peopleRows(1, 200), 200, opts(100))
	g.tables[0].PrimaryKey = nil
	require.True(t, g.run(jobs.Control{}).Finished)
	r = storedReport(t, g)
	require.Contains(t, r.Error, "no primary key")
	require.Equal(t, verify.PassedWithWarnings, r.Result)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestVerify_TargetCountSettles(t *testing.T) {
	countPoll = time.Millisecond
	f := newRunFixture(t, peopleRows(1, 10), 10, opts(100))
	e := &execution{r: f.runner, job: f.job}
	seq := func(vals ...int64) func(context.Context, store.WorkspaceID, string, store.MappingTarget) (int64, error) {
		i := 0
		return func(context.Context, store.WorkspaceID, string, store.MappingTarget) (int64, error) {
			v := vals[min(i, len(vals)-1)]
			i++
			if v < 0 {
				return 0, errors.New("query unavailable")
			}
			return v, nil
		}
	}
	f.runner.d.CountTarget = seq(3, 7, 10)
	n, ok := e.settledCount(context.Background(), store.MappingTarget{}, 10)
	require.True(t, ok)
	require.EqualValues(t, 10, n, "lagging counts catch up")
	f.runner.d.CountTarget = seq(12, 12)
	n, _ = e.settledCount(context.Background(), store.MappingTarget{}, 10)
	require.EqualValues(t, 12, n, "more than expected and stable")
	f.runner.d.CountTarget = seq(-1)
	_, ok = e.settledCount(context.Background(), store.MappingTarget{}, 10)
	require.False(t, ok, "no count at all")
	f.runner.d.CountTarget = seq(4, -1)
	n, ok = e.settledCount(context.Background(), store.MappingTarget{}, 10)
	require.True(t, ok)
	require.EqualValues(t, 4, n, "the last count read")
}
