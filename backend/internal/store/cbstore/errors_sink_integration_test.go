//go:build integration

package cbstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/pipeline"
	"omnidatabus/internal/store"
)

// TestErrorSink_AC2_Integration_BatchInsert: 1,200 dead-lettered rows are stored in batches on
// real Couchbase and come back paged, filtered and summarized (OC-026 AC2).
func TestErrorSink_AC2_Integration_BatchInsert(t *testing.T) {
	ctx := context.Background()
	ws := store.WorkspaceID("ws-sink-" + uuid.NewString())
	p, err := shared.Projects().Create(ctx, ws, store.NewProject{Name: "sink"})
	require.NoError(t, err)
	j, err := shared.Jobs().Create(ctx, ws, store.Job{ProjectID: p.ID, Options: json.RawMessage(`{}`)})
	require.NoError(t, err)
	sink := pipeline.NewErrorSink(shared.Jobs(), j.ID, ws, time.Hour, 0, 0, nil)
	sink.Describe("m1", pipeline.TableInfo{Columns: []string{"id", "age"}})
	var errs []pipeline.RowError
	for i := 0; i < 1200; i++ {
		code := "PARSE_FAILED"
		if i%4 == 0 {
			code = "DOC_TOO_LARGE"
		}
		errs = append(errs, pipeline.RowError{MappingID: "m1", Stage: "transform", Code: code, Message: "bad", SourceKey: []any{int64(i)}, Row: []any{int64(i), "x"}})
	}
	start := time.Now()
	sink.Add(ctx, errs)
	sink.Flush(ctx)
	t.Logf("stored 1,200 errors in %s", time.Since(start))
	_, stored, truncated, flushes := sink.Stats()
	require.EqualValues(t, 1200, stored)
	require.False(t, truncated)
	require.Equal(t, 3, flushes)

	sum, err := shared.Jobs().SummarizeErrors(ctx, ws, j.ID)
	require.NoError(t, err)
	require.Len(t, sum, 2)
	require.EqualValues(t, 900, sum[0].Count)
	require.EqualValues(t, 300, sum[1].Count)
	page, next, err := shared.Jobs().ListErrors(ctx, ws, j.ID, store.JobErrorFilter{Code: "DOC_TOO_LARGE"}, store.Page{Limit: 100})
	require.NoError(t, err)
	require.Len(t, page, 100)
	require.NotEmpty(t, next)
	require.JSONEq(t, `{"id":0,"age":"x"}`, string(page[0].Sample))
}
