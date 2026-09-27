// Package sourcetest provides an in-memory source connector for tests.
package sourcetest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

// Fake is a deterministic in-memory source connector.
type Fake struct {
	Type      string
	Rows      int
	BatchSize int
	// FailAtBatch makes Reader.Next fail on that batch number (1-based); 0 = never.
	FailAtBatch int
	Result      connectors.TestResult
	OpenErr     error

	// Discovery (OC-010).
	Snapshot    *schema.Snapshot
	Relations   int   // EstimateRelations result
	DiscoverErr error // returned by Discover
	// DiscoverGate, when set, blocks Discover until it is closed (single-flight tests).
	DiscoverGate chan struct{}
	Discovers    atomic.Int32 // Discover calls

	// Preview (OC-011): PreviewRows are returned (up to the request's limit); the last request is kept.
	PreviewRows [][]any
	PreviewErr  error
	LastPreview source.PreviewRequest
}

// Descriptor implements source.Connector.
func (f *Fake) Descriptor() connectors.Descriptor {
	t := f.Type
	if t == "" {
		t = "fake"
	}
	return connectors.Descriptor{Type: t, Role: connectors.RoleSource, DisplayName: "Fake " + t, Icon: t}
}

// Validate implements source.Connector.
func (f *Fake) Validate(cfg json.RawMessage) error {
	if !json.Valid(cfg) {
		return errors.New("invalid config")
	}
	return nil
}

// Test implements connectors.Tester.
func (f *Fake) Test(context.Context, json.RawMessage, connectors.Credentials) connectors.TestResult {
	return f.Result
}

// Open implements source.Connector.
func (f *Fake) Open(context.Context, json.RawMessage, connectors.Credentials) (source.Session, error) {
	if f.OpenErr != nil {
		return nil, f.OpenErr
	}
	return &session{f: f}, nil
}

type session struct{ f *Fake }

func (s *session) Ping(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{Product: "Fake", Version: "1.0", Database: "fake", User: "tester"}, nil
}

func (s *session) CheckPrivileges(context.Context) (source.PrivilegeReport, error) {
	return source.PrivilegeReport{}, nil
}
func (s *session) Close() error { return nil }

func (s *session) EstimateRelations(context.Context, source.DiscoverOptions) (int, error) {
	return s.f.Relations, nil
}

func (s *session) Preview(_ context.Context, req source.PreviewRequest) ([][]any, error) {
	s.f.LastPreview = req
	if s.f.PreviewErr != nil {
		return nil, s.f.PreviewErr
	}
	rows := s.f.PreviewRows
	if limit := source.ClampPreviewLimit(req.Limit); len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *session) Discover(ctx context.Context, _ source.DiscoverOptions) (*schema.Snapshot, error) {
	s.f.Discovers.Add(1)
	if s.f.DiscoverGate != nil {
		select {
		case <-s.f.DiscoverGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.f.DiscoverErr != nil {
		return nil, s.f.DiscoverErr
	}
	if s.f.Snapshot == nil {
		return &schema.Snapshot{Database: "fake", ServerVersion: "1.0", Schemas: []schema.Schema{}}, nil
	}
	cp := *s.f.Snapshot
	return &cp, nil
}

// NewReader returns a reader over the fake rows (id = 1..Rows).
func (f *Fake) NewReader() source.Reader {
	bs := f.BatchSize
	if bs <= 0 {
		bs = 1000
	}
	return &reader{f: f, batchSize: bs}
}

// ErrInjected is returned at FailAtBatch.
var ErrInjected = errors.New("sourcetest: injected read failure")

type reader struct {
	f         *Fake
	batchSize int
	next      int // next row id - 1
	seq       uint64
}

func (r *reader) Next(context.Context) (*source.Batch, error) {
	if r.next >= r.f.Rows {
		return nil, io.EOF
	}
	r.seq++
	if r.f.FailAtBatch > 0 && r.seq == uint64(r.f.FailAtBatch) {
		return nil, ErrInjected
	}
	end := min(r.next+r.batchSize, r.f.Rows)
	rows := make([][]any, 0, end-r.next)
	for id := r.next + 1; id <= end; id++ {
		rows = append(rows, []any{int64(id), "row"})
	}
	r.next = end
	return &source.Batch{Seq: r.seq, Rows: rows, LastKey: []any{int64(end)}}, nil
}

func (r *reader) Close() error { return nil }
