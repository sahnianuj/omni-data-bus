package source

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/schema"
)

// previewSession answers Preview with fixed rows (or an error) and records the request.
type previewSession struct {
	Session
	rows [][]any
	err  error
	got  PreviewRequest
}

func (s *previewSession) Preview(_ context.Context, req PreviewRequest) ([][]any, error) {
	s.got = req
	return s.rows, s.err
}

// OC-171: a variant discriminator's distinct values, read through the preview, nulls skipped.
func TestDistinctValues_OC171(t *testing.T) {
	tbl := &schema.Table{Name: "payment", Columns: []schema.Column{{Name: "kind"}, {Name: "amount"}}}
	s := &previewSession{rows: [][]any{{"card"}, {nil}, {"cash"}, {7}, {}}}
	require.Equal(t, []string{"card", "cash", "7"}, DistinctValues(context.Background(), s, "pos", tbl, "kind", 10))
	require.Equal(t, PreviewRequest{Schema: "pos", Table: "payment", Columns: []schema.Column{{Name: "kind"}}, DistinctOn: []string{"kind"}, OrderBy: []string{"kind"}, Limit: 10}, s.got)

	require.Nil(t, DistinctValues(context.Background(), s, "pos", tbl, "missing", 10), "unknown column")
	require.Nil(t, DistinctValues(context.Background(), nil, "pos", tbl, "kind", 10), "no session")
	require.Nil(t, DistinctValues(context.Background(), &previewSession{err: errors.New("boom")}, "pos", tbl, "kind", 10), "a failed read gives nothing")
}
