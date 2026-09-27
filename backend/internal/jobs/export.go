package jobs

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"
	"time"

	"omnidatabus/internal/store"
)

// ErrorPages walks every stored error of a job, page by page.
func (s *Service) ErrorPages(ctx context.Context, ws store.WorkspaceID, id string, fn func([]store.JobError) error) error {
	if _, err := s.store.Jobs().Get(ctx, ws, id); err != nil {
		return err
	}
	cursor := ""
	for {
		page, next, err := s.store.Jobs().ListErrors(ctx, ws, id, store.JobErrorFilter{}, store.Page{Limit: 100, Cursor: cursor})
		if err != nil {
			return err
		}
		if err := fn(page); err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}

// ErrorsCSVHeader is the export's first row.
var ErrorsCSVHeader = []string{"seq", "at", "mappingId", "stage", "code", "fieldPath", "sourceKey", "message", "sample"}

// CSVWriter writes error records as RFC 4180 CSV (commas, quotes and newlines quoted).
type CSVWriter struct{ w *csv.Writer }

// NewCSVWriter writes the header.
func NewCSVWriter(w io.Writer) (*CSVWriter, error) {
	c := &CSVWriter{w: csv.NewWriter(w)}
	return c, c.w.Write(ErrorsCSVHeader)
}

// Write adds records.
func (c *CSVWriter) Write(errs []store.JobError) error {
	for _, e := range errs {
		if err := c.w.Write([]string{
			strconv.FormatInt(e.Seq, 10), e.At.UTC().Format(time.RFC3339), e.MappingID, e.Stage, e.Code, e.FieldPath,
			string(e.SourceKey), e.Message, string(e.Sample),
		}); err != nil {
			return err
		}
	}
	c.w.Flush()
	return c.w.Error()
}

// WriteNDJSON writes one JSON object per line.
func WriteNDJSON(w io.Writer, errs []store.JobError) error {
	enc := json.NewEncoder(w)
	for _, e := range errs {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}
