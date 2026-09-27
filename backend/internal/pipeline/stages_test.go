package pipeline

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
)

func transformAll(t *testing.T, workers int, batches []*source.Batch) []*DocBatch {
	t.Helper()
	in := make(chan *source.Batch, len(batches))
	for _, b := range batches {
		in <- b
	}
	close(in)
	out := make(chan *DocBatch, len(batches))
	tr := Transformer{MappingID: "m", Workers: workers, NewProgram: compilePeople(t), KeyIdx: []int{0}}
	requireNoErr(t, tr.Run(context.Background(), in, out))
	var res []*DocBatch
	for db := range out {
		res = append(res, db)
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Partition != res[j].Partition {
			return res[i].Partition < res[j].Partition
		}
		return res[i].Seq < res[j].Seq
	})
	return res
}

func TestTransformer_AC1_SplitsDocsAndErrors(t *testing.T) {
	res := transformAll(t, 4, []*source.Batch{batchOf(1, 0, peopleRows(1, 1000, 42, 777))})
	require.Len(t, res, 1)
	db := res[0]
	require.Len(t, db.Docs, 998)
	require.Len(t, db.RowErrors, 2)
	require.Equal(t, 1000, db.Rows)
	e := db.RowErrors[0]
	require.Equal(t, StageTransform, e.Stage)
	require.Equal(t, "PARSE_FAILED", e.Code)
	require.Equal(t, "age", e.FieldPath)
	require.Equal(t, []any{int32(42)}, e.SourceKey)
	require.Equal(t, "person::1", db.Docs[0].Key)
	require.JSONEq(t, `{"id":1,"name":"p1","age":21}`, string(db.Docs[0].Value))
	require.Equal(t, []any{int32(1)}, db.Docs[0].SourceKey)
}

func TestTransformer_AC1_PreservesBatchMeta(t *testing.T) {
	res := transformAll(t, 2, []*source.Batch{batchOf(7, 3, peopleRows(1, 10))})
	require.EqualValues(t, 7, res[0].Seq)
	require.Equal(t, 3, res[0].Partition)
	require.Equal(t, []any{int32(10)}, res[0].LastKey)
}

func TestTransformer_AC1_ParallelDeterministicOutput(t *testing.T) {
	var in []*source.Batch
	for i := int32(0); i < 20; i++ {
		in = append(in, batchOf(uint64(i+1), int(i%3), peopleRows(i*100+1, i*100+100, i*100+5)))
	}
	one := transformAll(t, 1, in)
	many := transformAll(t, 8, in)
	require.Equal(t, len(one), len(many))
	for i := range one {
		require.Equal(t, one[i].Docs, many[i].Docs)
		require.Equal(t, one[i].RowErrors, many[i].RowErrors)
	}
}

func TestWriterStage_AC7_SingleAckWithCounts(t *testing.T) {
	res := transformAll(t, 2, []*source.Batch{batchOf(1, 0, peopleRows(1, 1000, 3, 4))})
	w := newMemWriter()
	w.fail["person::10"] = target.WriteResult{Outcome: target.Failed, Code: target.CodeDocTooLarge}
	w.fail["person::11"] = target.WriteResult{Outcome: target.Skipped}
	in := make(chan *DocBatch, 1)
	in <- res[0]
	close(in)
	acks := make(chan Ack, 4)
	Writer{MappingID: "m", DocWriter: w, Workers: 3}.Run(context.Background(), in, acks)
	var got []Ack
	for a := range acks {
		got = append(got, a)
	}
	require.Len(t, got, 1, "exactly one ack per batch")
	a := got[0]
	require.EqualValues(t, 996, a.Written)
	require.EqualValues(t, 1, a.Skipped)
	require.EqualValues(t, 3, a.Failed, "2 transform errors + 1 write failure")
	require.EqualValues(t, 1000, a.Written+a.Skipped+a.Failed)
	require.Len(t, a.Errors, 3)
	require.Equal(t, StageWrite, a.Errors[2].Stage)
	require.Equal(t, []any{int32(10)}, a.Errors[2].SourceKey)
	require.Positive(t, a.Bytes)
	require.Nil(t, a.Fatal)
	require.Equal(t, []any{int32(1000)}, a.LastKey)
}
