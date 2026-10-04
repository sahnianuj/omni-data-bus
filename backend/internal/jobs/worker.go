package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"omnidatabus/internal/observability"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/store"
)

// WorkerConfig tunes the claim loop, heartbeats and the reaper (defaults per OC-023).
type WorkerConfig struct {
	ID                string
	MaxJobs           int           // OC_WORKER_MAX_JOBS, default 2
	PollInterval      time.Duration // claim loop, default 1 s (±20% jitter)
	StatusPoll        time.Duration // how often a running job's status is read, default 1 s
	HeartbeatInterval time.Duration // default 5 s
	StaleAfter        time.Duration // a heartbeat older than this is reaped, default 30 s
	ReaperInterval    time.Duration // default 15 s
	ShutdownBudget    time.Duration // time to drain and requeue on SIGTERM, default 25 s
	// Version is this worker's release (buildinfo): it only claims jobs whose MinWorkerVersion
	// it meets (OC-057). A development build claims everything.
	Version string
}

// level is the worker's version level for the claim query.
func (c WorkerConfig) level() int {
	if l, ok := store.VersionLevel(c.Version); ok {
		return l
	}
	return math.MaxInt32
}

func (c WorkerConfig) withDefaults() WorkerConfig {
	if c.ID == "" {
		host, _ := os.Hostname()
		c.ID = fmt.Sprintf("%s-%d-%04x", host, os.Getpid(), rand.IntN(0xffff)) //nolint:gosec // an identifier, not a secret
	}
	def := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	if c.MaxJobs == 0 {
		c.MaxJobs = 2
	}
	def(&c.PollInterval, time.Second)
	def(&c.StatusPoll, time.Second)
	def(&c.HeartbeatInterval, 5*time.Second)
	def(&c.StaleAfter, 30*time.Second)
	def(&c.ReaperInterval, 15*time.Second)
	def(&c.ShutdownBudget, 25*time.Second)
	return c
}

// Stop causes, recorded when the supervisor signals the run.
const (
	causePause    = "pause"
	causeCancel   = "cancel"
	causeLost     = "lost"
	causeShutdown = "shutdown"
)

// Worker claims queued jobs and runs them (OC-023).
type Worker struct {
	cfg    WorkerConfig
	store  store.Store
	runner Runner
	now    func() time.Time
	log    *slog.Logger

	mu       sync.Mutex
	running  map[string]*supervisor
	wg       sync.WaitGroup
	shutdown chan struct{}
}

// NewWorker returns a worker; call Run.
func NewWorker(cfg WorkerConfig, st store.Store, runner Runner, now func() time.Time, log *slog.Logger) *Worker {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Worker{
		cfg: cfg.withDefaults(), store: st, runner: runner, now: now, log: log,
		running: map[string]*supervisor{}, shutdown: make(chan struct{}),
	}
}

// ID is this worker's claim identity.
func (w *Worker) ID() string { return w.cfg.ID }

// Running reports how many jobs this worker runs now.
func (w *Worker) Running() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.running)
}

// Run claims and runs jobs until ctx is cancelled, then shuts down gracefully: running jobs
// drain, checkpoint and go back to queued so another worker continues them (OC-023 AC8).
func (w *Worker) Run(ctx context.Context) error {
	reaper := time.NewTicker(w.cfg.ReaperInterval)
	defer reaper.Stop()
	observability.WorkerCapacity.WithLabelValues(w.cfg.ID).Set(float64(w.cfg.MaxJobs))
	observability.WorkerActiveJobs.WithLabelValues(w.cfg.ID).Set(0)
	w.ClaimOnce(ctx)
	for {
		jitter := time.Duration(float64(w.cfg.PollInterval) * (0.8 + 0.4*rand.Float64())) //nolint:gosec // jitter
		select {
		case <-ctx.Done():
			w.Shutdown()
			return nil
		case <-reaper.C:
			if _, err := w.ReapOnce(ctx); err != nil {
				w.log.Warn("reaper", "err", err)
			}
		case <-time.After(jitter):
			w.ClaimOnce(ctx)
		}
	}
}

// Shutdown signals every run to drain and waits (up to the budget) for them to requeue.
func (w *Worker) Shutdown() {
	w.mu.Lock()
	select {
	case <-w.shutdown:
	default:
		close(w.shutdown)
	}
	w.mu.Unlock()
	done := make(chan struct{})
	go func() { w.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(w.cfg.ShutdownBudget):
		w.log.Warn("shutdown budget exceeded; the reaper will requeue the remaining jobs")
	}
}

// ClaimOnce claims queued jobs up to the free capacity (OC-023 AC5). A CAS mismatch means
// another worker won the job, so the next candidate is tried.
func (w *Worker) ClaimOnce(ctx context.Context) int {
	select {
	case <-w.shutdown:
		return 0
	default:
	}
	free := w.cfg.MaxJobs - w.Running()
	if free <= 0 {
		return 0
	}
	cands, err := w.store.Jobs().ListClaimable(ctx, w.cfg.level(), 10)
	if err != nil {
		w.log.Warn("claim: list queued jobs", "err", err)
		return 0
	}
	claimed := 0
	for _, j := range cands {
		if claimed >= free {
			break
		}
		to, err := Transition(j.Status, EvClaim)
		if err != nil {
			continue
		}
		now := w.now()
		j.Status, j.ClaimedBy, j.HeartbeatAt = to, w.cfg.ID, &now
		if j.StartedAt == nil {
			j.StartedAt = &now
		}
		j, err = w.store.Jobs().Update(ctx, j)
		if err != nil {
			continue // another worker claimed it (or it changed): try the next one
		}
		claimed++
		publishStatus(ctx, w.store, j, now)
		w.event(ctx, j.ID, "claimed", "Started on worker "+w.cfg.ID)
		w.start(ctx, j)
	}
	return claimed
}

func (w *Worker) event(ctx context.Context, jobID, kind, msg string) {
	_ = w.store.Jobs().AppendEvent(ctx, jobID, store.JobEvent{At: w.now(), Kind: kind, Message: msg})
}

type supervisor struct {
	w      *Worker
	job    store.Job
	drain  chan struct{}
	abort  chan struct{}
	once   sync.Once
	cause  string
	causeM sync.Mutex
}

func (s *supervisor) stop(cause string) {
	s.once.Do(func() {
		s.causeM.Lock()
		s.cause = cause
		s.causeM.Unlock()
		if cause == causePause || cause == causeShutdown {
			close(s.drain)
		} else {
			close(s.abort)
		}
	})
}

func (s *supervisor) stopCause() string {
	s.causeM.Lock()
	defer s.causeM.Unlock()
	return s.cause
}

func (w *Worker) start(ctx context.Context, j store.Job) {
	s := &supervisor{w: w, job: j, drain: make(chan struct{}), abort: make(chan struct{})}
	w.mu.Lock()
	w.running[j.ID] = s
	observability.WorkerActiveJobs.WithLabelValues(w.cfg.ID).Set(float64(len(w.running)))
	w.mu.Unlock()
	w.wg.Add(1)
	go func() {
		defer func() {
			w.mu.Lock()
			delete(w.running, j.ID)
			observability.WorkerActiveJobs.WithLabelValues(w.cfg.ID).Set(float64(len(w.running)))
			w.mu.Unlock()
			w.wg.Done()
		}()
		// A run outlives the claim loop: shutdown drains it instead of cancelling it.
		s.run(context.WithoutCancel(ctx))
	}()
}

// run supervises one job: it polls the job's status every StatusPoll (pause and cancel are
// status changes), writes a heartbeat every HeartbeatInterval, and aborts at once when the
// claim moved to another worker (fencing, OC-023 AC6).
func (s *supervisor) run(ctx context.Context) {
	w := s.w
	exec := w.runner.Start(ctx, s.job, Control{Drain: s.drain, Abort: s.abort})
	done := make(chan Outcome, 1)
	go func() { done <- exec.Wait() }()
	tick := time.NewTicker(w.cfg.StatusPoll)
	defer tick.Stop()
	lastBeat := w.now()
	for {
		select {
		case o := <-done:
			w.finish(ctx, s.job.ID, s.stopCause(), o)
			return
		case <-w.shutdown:
			s.stop(causeShutdown)
		case <-tick.C:
			cur, err := w.store.Jobs().GetAny(ctx, s.job.ID)
			if err != nil {
				continue // metadata store hiccup: the reaper allows 30 s
			}
			if cur.ClaimedBy != w.cfg.ID {
				s.stop(causeLost)
				continue
			}
			switch cur.Status {
			case store.JobPausing:
				s.stop(causePause)
			case store.JobCancelling:
				s.stop(causeCancel)
			}
			if w.now().Sub(lastBeat) >= w.cfg.HeartbeatInterval {
				now := w.now()
				cur.HeartbeatAt = &now
				if cp := exec.Checkpoint(); len(cp) > 0 {
					cur.Checkpoint = cp
				}
				if _, err := w.store.Jobs().Update(ctx, cur); err == nil {
					lastBeat = now
					observability.WorkerHeartbeats.WithLabelValues(w.cfg.ID).Inc()
				} else if !errors.Is(err, store.ErrVersionConflict) {
					w.log.Warn("heartbeat", "job", s.job.ID, "err", err)
				}
			}
		}
	}
}

// finish records how the run ended, with CAS retries against concurrent status changes.
func (w *Worker) finish(ctx context.Context, jobID, cause string, o Outcome) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for attempt := 0; attempt < 10; attempt++ {
		cur, err := w.store.Jobs().GetAny(ctx, jobID)
		if err != nil {
			w.log.Error("finish: read job", "job", jobID, "err", err)
			return
		}
		if cur.ClaimedBy != w.cfg.ID || cause == causeLost {
			return // fenced out: the new owner records the outcome
		}
		ev, reason, detail := closingEvent(cur.Status, cause, o)
		to, err := Transition(cur.Status, ev)
		if err != nil {
			w.log.Error("finish: transition", "job", jobID, "status", cur.Status, "event", ev)
			return
		}
		now := w.now()
		cur.Status, cur.Reason, cur.ReasonDetail = to, reason, detail
		if len(o.Checkpoint) > 0 {
			cur.Checkpoint = o.Checkpoint
		}
		cur.ErrorsTruncated = cur.ErrorsTruncated || o.ErrorsTruncated
		cur.HeartbeatAt = nil
		cur.ClaimedBy = ""
		if store.JobTerminal(to) {
			cur.FinishedAt = &now
		}
		cur.Progress = progress.WithStatus(jobID, o.Progress, to, now)
		updated, err := w.store.Jobs().Update(ctx, cur)
		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		if err != nil {
			w.log.Error("finish: update job", "job", jobID, "err", err)
			return
		}
		_ = w.store.Jobs().PutProgress(ctx, jobID, updated.Progress)
		w.event(ctx, jobID, string(ev), closingMessage(to, reason, detail))
		return
	}
}

// closingEvent picks the event for a run that returned (see FinalEvent for completed runs).
func closingEvent(status, cause string, o Outcome) (Event, string, string) {
	switch {
	case status == store.JobCancelling || cause == causeCancel:
		return EvStopped, ReasonCancelled, ""
	case o.Fatal != nil:
		return EvFail, o.Fatal.Code, o.Fatal.Message
	case o.Finished:
		return FinalEvent(o), "", ""
	case status == store.JobPausing || cause == causePause:
		return EvDrained, "", ""
	case cause == causeShutdown:
		return EvRequeue, "", ""
	}
	return EvFail, "INTERNAL", "The run stopped unexpectedly."
}

func closingMessage(status, reason, detail string) string {
	switch status {
	case store.JobCompleted:
		return "Completed"
	case store.JobCompletedWithErrors:
		return "Completed with errors"
	case store.JobPaused:
		return "Paused; progress saved"
	case store.JobQueued:
		return "Worker shutting down; handed back to the queue"
	case store.JobCancelled:
		return "Cancelled"
	}
	if detail != "" {
		return "Failed: " + detail
	}
	return "Failed (" + reason + ")"
}

// ReapOnce resets, on the node holding the reaper lease, jobs whose owner stopped heart-
// beating: running → queued (resumes from the checkpoint), pausing → paused, cancelling →
// cancelled (OC-023 AC6). It returns how many jobs were reset.
func (w *Worker) ReapOnce(ctx context.Context) (int, error) {
	ok, err := w.store.Jobs().AcquireLease(ctx, "reaper", w.cfg.ID, 2*w.cfg.ReaperInterval)
	if err != nil || !ok {
		return 0, err
	}
	stale, err := w.store.Jobs().ListByStatus(ctx, []string{store.JobRunning, store.JobPausing, store.JobCancelling}, 100)
	if err != nil {
		return 0, err
	}
	cutoff := w.now().Add(-w.cfg.StaleAfter)
	n := 0
	for _, j := range stale {
		last := j.UpdatedAt
		if j.HeartbeatAt != nil {
			last = *j.HeartbeatAt
		}
		if !last.Before(cutoff) {
			continue
		}
		to, err := Transition(j.Status, EvReap)
		if err != nil {
			continue
		}
		prev := j.ClaimedBy
		j.Status, j.ClaimedBy, j.HeartbeatAt = to, "", nil
		if store.JobTerminal(to) {
			now := w.now()
			j.FinishedAt, j.Reason = &now, ReasonCancelled
		}
		if j, err = w.store.Jobs().Update(ctx, j); err != nil {
			continue
		}
		n++
		publishStatus(ctx, w.store, j, w.now())
		w.event(ctx, j.ID, "reaped", "Worker "+prev+" stopped responding; job set to "+to)
	}
	return n, nil
}
