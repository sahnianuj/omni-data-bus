package jobs

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

func TestErrorsExport_AC5_CSVQuoting(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewCSVWriter(&buf)
	require.NoError(t, err)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, w.Write([]store.JobError{{
		Seq: 1, At: at, MappingID: "m1", Stage: "transform", Code: "PARSE_FAILED", FieldPath: "total",
		SourceKey: json.RawMessage(`[1,"a,b"]`), Message: "\"12,5\" is not a number\nsecond line", Sample: json.RawMessage(`{"total":"12,5"}`),
	}}))
	recs, err := csv.NewReader(bytes.NewReader(buf.Bytes())).ReadAll()
	require.NoError(t, err, buf.String())
	require.Equal(t, ErrorsCSVHeader, recs[0])
	require.Equal(t, []string{
		"1", "2026-09-01T12:00:00Z", "m1", "transform", "PARSE_FAILED", "total", `[1,"a,b"]`,
		"\"12,5\" is not a number\nsecond line", `{"total":"12,5"}`,
	}, recs[1], "commas, quotes and newlines survive")
}
