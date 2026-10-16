package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// Defaults (OC-010).
const (
	// AsyncThreshold: catalogs with more relations are discovered in the background (202).
	AsyncThreshold = 2000
	// Retention is how many snapshots are kept per connection.
	Retention = 5
	// asyncTimeout bounds a background discovery.
	asyncTimeout = 10 * time.Minute
	// finishedTTL is how long a finished run can still be polled.
	finishedTTL = 15 * time.Minute
)

// Run statuses.
const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// ErrNotDiscovered is returned when a connection has no snapshot yet.
var ErrNotDiscovered = problem.New(http.StatusNotFound, problem.CodeSchemaNotDiscovered, "Schema not discovered yet",
	"Run schema discovery on this source repository first.")

// Opener opens a session on a stored source repository (connections.Service.OpenSource).
type Opener func(ctx context.Context, ws store.WorkspaceID, connectionID string) (source.Session, store.Connection, error)

// Status is a discovery run, returned by POST …/discover and GET …/discoveries/{id}.
type Status struct {
	ID           string           `json:"discoveryId"`
	ConnectionID string           `json:"connectionId"`
	Status       string           `json:"status"`
	StartedAt    time.Time        `json:"startedAt"`
	FinishedAt   *time.Time       `json:"finishedAt,omitempty"`
	SnapshotID   string           `json:"snapshotId,omitempty"`
	TableCount   int              `json:"tableCount,omitempty"`
	DurationMs   int64            `json:"durationMs,omitempty"`
	Diff         *Diff            `json:"diff,omitempty"`
	Error        *problem.Problem `json:"error,omitempty"`
}

type run struct {
	st        Status
	workspace store.WorkspaceID
	done      chan struct{}
}

// Service runs discoveries. Runs are tracked in memory: a finished run can be polled for
// finishedTTL, and only one run per connection is in flight at a time (single flight).
type Service struct {
	store     store.Store
	open      Opener
	clock     store.Clock
	threshold int
	keep      int
	// RefreshBefore: a connection whose latest snapshot was captured before it is rescanned
	// when Latest is first called for it (OC_REDISCOVER_ON_START). Zero = off.
	RefreshBefore time.Time
	refreshed     sync.Map // connection IDs already rescanned for RefreshBefore

	mu     sync.Mutex
	runs   map[string]*run // by discovery ID
	active map[string]*run // by connection ID, while running
	wg     sync.WaitGroup
}

// NewService builds the discovery service.
func NewService(st store.Store, open Opener, clock store.Clock) *Service {
	if clock == nil {
		clock = store.SystemClock{}
	}
	return &Service{
		store: st, open: open, clock: clock, threshold: AsyncThreshold, keep: Retention,
		runs: map[string]*run{}, active: map[string]*run{},
	}
}

// Result is what Discover returns: a finished run (sync) or a running one (async).
type Result struct {
	Async  bool
	Status Status
}

// Discover discovers the connection's schema. Small catalogs finish before it returns; larger
// ones (or a request that joins an in-flight run) return a running status to poll.
func (s *Service) Discover(ctx context.Context, ws store.WorkspaceID, userID, connectionID string, opts source.DiscoverOptions) (Result, error) {
	r, joined, err := s.start(ws, connectionID)
	if err != nil {
		return Result{}, err
	}
	if joined {
		return Result{Async: true, Status: r.st}, nil
	}
	sess, conn, err := s.open(ctx, ws, connectionID)
	if err != nil {
		s.finish(r, nil, err)
		return Result{}, err
	}
	n, err := sess.EstimateRelations(ctx, opts)
	if err != nil {
		_ = sess.Close()
		s.finish(r, nil, err)
		return Result{}, err
	}
	if n > s.threshold {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), asyncTimeout)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer cancel()
			_ = s.execute(bg, r, sess, conn, userID, opts)
		}()
		return Result{Async: true, Status: s.snapshotOf(r)}, nil
	}
	if err := s.execute(ctx, r, sess, conn, userID, opts); err != nil {
		return Result{}, err
	}
	return Result{Status: s.snapshotOf(r)}, nil
}

// MaxConcurrentPerWorkspace bounds discoveries running at once in a workspace (OC-033):
// each holds a source connection and scans the catalog.
const MaxConcurrentPerWorkspace = 2

// start registers a run, or returns the connection's in-flight run (joined = true).
func (s *Service) start(ws store.WorkspaceID, connectionID string) (*run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if r, ok := s.active[connectionID]; ok && r.workspace == ws {
		return r, true, nil
	}
	running := 0
	for _, r := range s.active {
		if r.workspace == ws {
			running++
		}
	}
	if running >= MaxConcurrentPerWorkspace {
		return nil, false, httpapi.ErrTooManyConcurrent("schema discoveries", MaxConcurrentPerWorkspace)
	}
	r := &run{
		st:        Status{ID: uuid.Must(uuid.NewV7()).String(), ConnectionID: connectionID, Status: StatusRunning, StartedAt: s.clock.Now()},
		workspace: ws, done: make(chan struct{}),
	}
	s.runs[r.st.ID], s.active[connectionID] = r, r
	return r, false, nil
}

func (s *Service) execute(ctx context.Context, r *run, sess source.Session, conn store.Connection, userID string, opts source.DiscoverOptions) error {
	defer func() { _ = sess.Close() }()
	ctx, span := observability.Tracer().Start(ctx, "discovery", trace.WithAttributes(attribute.String("source.type", conn.Type)))
	defer span.End()
	started := s.clock.Now()
	wall := time.Now()
	snap, err := sess.Discover(ctx, opts)
	if err != nil {
		span.SetStatus(codes.Error, "discovery failed")
		s.finish(r, nil, err)
		return err
	}
	observability.DiscoveryDuration.WithLabelValues(conn.Type).Observe(time.Since(wall).Seconds())
	span.SetAttributes(attribute.Int("discovery.tables", snap.TableCount()))
	payload, err := json.Marshal(snap)
	if err != nil {
		s.finish(r, nil, err)
		return err
	}
	var prev *schema.Snapshot
	if old, err := s.store.Snapshots().Latest(ctx, conn.WorkspaceID, conn.ID); err == nil {
		prev = &schema.Snapshot{}
		if json.Unmarshal(old.Payload, prev) != nil {
			prev = nil
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		s.finish(r, nil, err)
		return err
	}
	rec := store.DiscoverySnapshot{
		ID: uuid.Must(uuid.NewV7()).String(), ConnectionID: conn.ID, CapturedAt: s.clock.Now(),
		DurationMs: s.clock.Now().Sub(started).Milliseconds(), TableCount: snap.TableCount(), Payload: payload,
	}
	if err := s.store.Snapshots().Save(ctx, conn.WorkspaceID, rec, s.keep); err != nil {
		s.finish(r, nil, err)
		return err
	}
	var diff *Diff
	if prev != nil {
		d := Compare(prev, snap)
		diff = &d
	}
	s.finish(r, &done{rec: rec, diff: diff}, nil)
	s.audit(ctx, conn, userID, rec)
	return nil
}

type done struct {
	rec  store.DiscoverySnapshot
	diff *Diff
}

func (s *Service) finish(r *run, d *done, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	r.st.FinishedAt = &now
	if err != nil {
		p := problem.From(err)
		r.st.Status, r.st.Error = StatusFailed, &p
	} else {
		r.st.Status, r.st.SnapshotID, r.st.TableCount = StatusCompleted, d.rec.ID, d.rec.TableCount
		r.st.DurationMs, r.st.Diff = d.rec.DurationMs, d.diff
	}
	if s.active[r.st.ConnectionID] == r {
		delete(s.active, r.st.ConnectionID)
	}
	close(r.done)
}

func (s *Service) snapshotOf(r *run) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return r.st
}

// pruneLocked forgets runs that finished more than finishedTTL ago.
func (s *Service) pruneLocked() {
	cutoff := s.clock.Now().Add(-finishedTTL)
	for id, r := range s.runs {
		if r.st.FinishedAt != nil && r.st.FinishedAt.Before(cutoff) {
			delete(s.runs, id)
		}
	}
}

// Status returns a run of the connection (for polling).
func (s *Service) Status(ws store.WorkspaceID, connectionID, discoveryID string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[discoveryID]
	if !ok || r.workspace != ws || r.st.ConnectionID != connectionID {
		return Status{}, store.ErrNotFound
	}
	return r.st, nil
}

// Wait blocks until every background discovery has finished (tests and shutdown).
func (s *Service) Wait() { s.wg.Wait() }

// Latest returns the connection's latest snapshot, decoded.
func (s *Service) Latest(ctx context.Context, ws store.WorkspaceID, connectionID string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
	if _, err := s.store.Connections().Get(ctx, ws, connectionID); err != nil {
		return store.DiscoverySnapshot{}, nil, err
	}
	rec, err := s.store.Snapshots().Latest(ctx, ws, connectionID)
	if errors.Is(err, store.ErrNotFound) {
		return rec, nil, ErrNotDiscovered
	}
	if err != nil {
		return rec, nil, err
	}
	if s.refreshStale(ctx, ws, connectionID, rec) {
		if fresh, err := s.store.Snapshots().Latest(ctx, ws, connectionID); err == nil {
			rec = fresh
		}
	}
	var snap schema.Snapshot
	if err := json.Unmarshal(rec.Payload, &snap); err != nil {
		return rec, nil, err
	}
	return rec, &snap, nil
}

// refreshStale rescans the connection when rec predates RefreshBefore, once per connection;
// it reports whether a newer snapshot was saved. A failed or background rescan leaves rec in
// use (a background one is picked up by the next read).
func (s *Service) refreshStale(ctx context.Context, ws store.WorkspaceID, connectionID string, rec store.DiscoverySnapshot) bool {
	if s.RefreshBefore.IsZero() || !rec.CapturedAt.Before(s.RefreshBefore) {
		return false
	}
	if _, done := s.refreshed.LoadOrStore(connectionID, true); done {
		return false
	}
	res, err := s.Discover(ctx, ws, "", connectionID, source.DiscoverOptions{})
	if err != nil {
		slog.WarnContext(ctx, "rescan of a saved schema failed; showing the saved one", "connection", connectionID, "error", err.Error())
		return false
	}
	return !res.Async
}

func (s *Service) audit(ctx context.Context, conn store.Connection, userID string, rec store.DiscoverySnapshot) {
	meta, _ := json.Marshal(map[string]any{"projectId": conn.ProjectID, "snapshotId": rec.ID, "tables": rec.TableCount})
	if err := s.store.Audit().Record(ctx, store.AuditEvent{
		WorkspaceID: conn.WorkspaceID, UserID: userID, Action: "connection.discover",
		EntityType: "connection", EntityID: conn.ID, Metadata: meta,
	}); err != nil {
		slog.WarnContext(ctx, "audit record failed", "action", "connection.discover", "error", err.Error())
	}
}
