package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/store"
)

// ErrJobRunning is returned when the project already has an active job (OC-023 AC4).
var ErrJobRunning = problem.New(http.StatusConflict, problem.CodeProjectJobRunning, "Migration already running",
	"This project already has an active migration. Open it, or wait until it finishes.")

// Service manages jobs for the API (the worker drives them).
type Service struct {
	store     store.Store
	preflight *Preflighter
	now       func() time.Time
	// PlanHash identifies the execution plan of a mapping's (frozen) spec (OC-037); recorded
	// on the job so runs can be compared. Nil skips it.
	PlanHash func(ctx context.Context, ws store.WorkspaceID, m store.Mapping) (string, error)
}

// NewService returns the jobs service.
func NewService(st store.Store, pre *Preflighter, now func() time.Time) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: st, preflight: pre, now: now}
}

// View is a job as the API returns it.
type View struct {
	store.Job
	Options   Options           `json:"options"`
	Progress  progress.Snapshot `json:"progress"`
	Resumable bool              `json:"resumable"`
	// Retryable is true when "retry failed rows" is possible (OC-026 AC6).
	Retryable bool `json:"retryable"`
	// ResumeInfo tells the resume dialog what a resume will do (OC-029 AC7).
	ResumeInfo *ResumeInfo `json:"resumeInfo,omitempty"`
}

// MappingRef names a mapping of the job.
type MappingRef struct {
	MappingID string `json:"mappingId"`
	Table     string `json:"table"`
}

// ResumeInfo describes a resumable job's checkpoint.
type ResumeInfo struct {
	// RowsDone is how many rows the checkpoint covers (they aren't read again).
	RowsDone int64 `json:"rowsDone"`
	// Changed lists mappings edited since the job started: a resume keeps the frozen spec.
	Changed []MappingRef `json:"changed"`
	// Restart lists unfinished tables without a primary key: a resume re-reads them.
	Restart []MappingRef `json:"restart"`
}

// storedCheckpoint is the part of pipeline.Checkpoint the service reads.
type storedCheckpoint struct {
	Mappings map[string]struct {
		Cursor  bool  `json:"cursor"`
		Done    bool  `json:"done"`
		Written int64 `json:"written"`
		Skipped int64 `json:"skipped"`
		Failed  int64 `json:"failed"`
	} `json:"mappings"`
}

func (s *Service) resumeInfo(ctx context.Context, j store.Job, opts Options) *ResumeInfo {
	info := &ResumeInfo{Changed: []MappingRef{}, Restart: []MappingRef{}}
	var cp storedCheckpoint
	_ = json.Unmarshal(j.Checkpoint, &cp)
	for _, id := range j.MappingIDs {
		m, err := s.store.Mappings().Get(ctx, j.WorkspaceID, id)
		ref := MappingRef{MappingID: id, Table: id}
		if err == nil {
			srcSchema, srcTable := m.Driving()
			ref.Table = srcSchema + "." + srcTable
			if v, ok := opts.MappingVersions[id]; ok && v != m.Version {
				info.Changed = append(info.Changed, ref)
			}
		}
		mc, ok := cp.Mappings[id]
		switch {
		case !ok:
		case mc.Cursor && !mc.Done:
			info.Restart = append(info.Restart, ref)
		default:
			info.RowsDone += mc.Written + mc.Skipped + mc.Failed
		}
	}
	return info
}

func (s *Service) view(ctx context.Context, j store.Job) View {
	opts, _ := ParseOptions(j.Options)
	v := View{Job: j, Options: opts}
	v.Options.Specs, v.Options.TargetEmpty = nil, nil // server-side bookkeeping
	raw := j.Progress
	if doc, _, err := s.store.Jobs().GetProgress(ctx, j.ID); err == nil {
		raw = doc
	}
	v.Progress = progress.Parse(j.ID, raw)
	if v.Progress.Status == "" || store.JobTerminal(j.Status) || v.Progress.Status != j.Status {
		v.Progress.Status = j.Status
	}
	v.Resumable = j.Status == store.JobPaused || (j.Status == store.JobFailed && len(j.Checkpoint) > 0 && j.Reason != ReasonNotResumable)
	v.Retryable = (j.Status == store.JobFailed || j.Status == store.JobCompletedWithErrors) && v.Progress.Failed > 0 && !j.ErrorsTruncated
	if v.Resumable {
		v.ResumeInfo = s.resumeInfo(ctx, j, opts)
	}
	return v
}

// Preflight runs the checks without creating a job.
func (s *Service) Preflight(ctx context.Context, ws store.WorkspaceID, userID, projectID string, opts Options) (PreflightResult, error) {
	if err := opts.Validate(); err != nil {
		return PreflightResult{}, err
	}
	return s.preflight.Run(ctx, ws, userID, projectID, opts)
}

// Create runs the pre-flight again and queues the job. A failing pre-flight is a 422 with the
// checklist attached; an active job in the project is 409 PROJECT_HAS_RUNNING_JOB.
func (s *Service) Create(ctx context.Context, ws store.WorkspaceID, userID, projectID string, opts Options) (View, PreflightResult, error) {
	if err := opts.Validate(); err != nil {
		return View{}, PreflightResult{}, err
	}
	if _, err := s.store.Jobs().ActiveForProject(ctx, ws, projectID); err == nil {
		return View{}, PreflightResult{}, ErrJobRunning
	}
	pre, err := s.preflight.Run(ctx, ws, userID, projectID, opts)
	if err != nil {
		return View{}, pre, err
	}
	if !pre.OK {
		// Validation errors come first, listed one by one (OC-049 AC6); the checklist goes along.
		if err := s.blockInvalid(ctx, ws, projectID, opts.MappingIDs); err != nil {
			return View{}, pre, err
		}
		p := problem.New(http.StatusUnprocessableEntity, problem.CodePreflightFailed, "Pre-flight checks failed", failedChecks(pre))
		return View{}, pre, p
	}
	ids := make([]string, len(pre.Mappings))
	for i, m := range pre.Mappings {
		ids[i] = m.MappingID
	}
	return s.create(ctx, ws, userID, projectID, opts, ids, pre)
}

func (s *Service) create(ctx context.Context, ws store.WorkspaceID, userID, projectID string, opts Options, ids []string, pre PreflightResult) (View, PreflightResult, error) {
	opts.MappingIDs = ids
	// Freeze each mapping's spec: a resumed run uses exactly what the job started with.
	opts.Specs, opts.MappingVersions, opts.TargetEmpty = map[string]json.RawMessage{}, map[string]int64{}, map[string]bool{}
	for _, pm := range pre.Mappings {
		if pm.TargetEmpty {
			opts.TargetEmpty[pm.MappingID] = true
		}
	}
	for _, id := range ids {
		m, err := s.store.Mappings().Get(ctx, ws, id)
		if err != nil {
			return View{}, pre, err
		}
		opts.Specs[id], opts.MappingVersions[id] = m.Spec, m.Version
		if s.PlanHash != nil {
			if h, err := s.PlanHash(ctx, ws, m); err == nil {
				if opts.PlanHashes == nil {
					opts.PlanHashes = map[string]string{}
				}
				opts.PlanHashes[id] = h
			}
		}
	}
	raw, _ := json.Marshal(opts)
	j, err := s.store.Jobs().Create(ctx, ws, store.Job{
		ProjectID: projectID, Status: store.JobQueued, Options: raw, MappingIDs: ids, CreatedBy: userID, RetryOf: opts.RetryOf,
	})
	var conflict *store.ConflictError
	if errors.As(err, &conflict) {
		return View{}, pre, ErrJobRunning
	}
	if err != nil {
		return View{}, pre, err
	}
	now := s.now()
	snap := progress.Snapshot{JobID: j.ID, Status: j.Status, TS: now, TotalKind: progress.TotalEstimate, ErrorsByCode: []progress.CodeCount{}}
	for _, m := range pre.Mappings {
		snap.Total += m.RowEstimate
		snap.Mappings = append(snap.Mappings, progress.MappingProgress{
			MappingID: m.MappingID, Table: m.Table, Target: m.Location.String(),
			Phase: progress.PhaseQueued, Total: m.RowEstimate, TotalKind: progress.TotalEstimate,
		})
	}
	doc, _ := json.Marshal(snap)
	_ = s.store.Jobs().PutProgress(ctx, j.ID, doc)
	s.event(ctx, j.ID, "created", "Queued "+plural(len(ids), "table")+" for migration")
	for _, c := range pre.Checks {
		if c.Status == CheckWarn {
			s.event(ctx, j.ID, "warning", c.Label+": "+c.Detail)
		}
	}
	return s.view(ctx, j), pre, nil
}

// Get returns one job.
func (s *Service) Get(ctx context.Context, ws store.WorkspaceID, id string) (View, error) {
	j, err := s.store.Jobs().Get(ctx, ws, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, j), nil
}

// List returns jobs, newest first (projectID "" = the whole workspace).
func (s *Service) List(ctx context.Context, ws store.WorkspaceID, projectID string, page store.Page) ([]View, string, error) {
	if projectID != "" {
		if _, err := s.store.Projects().Get(ctx, ws, projectID); err != nil {
			return nil, "", err
		}
	}
	items, next, err := s.store.Jobs().List(ctx, ws, projectID, page)
	if err != nil {
		return nil, "", err
	}
	out := make([]View, len(items))
	for i, j := range items {
		out[i] = s.view(ctx, j)
	}
	return out, next, nil
}

// transition applies ev to the job with CAS, retrying on concurrent worker writes.
func (s *Service) transition(ctx context.Context, ws store.WorkspaceID, id string, ev Event, mutate func(*store.Job)) (store.Job, error) {
	for attempt := 0; attempt < 10; attempt++ {
		j, err := s.store.Jobs().Get(ctx, ws, id)
		if err != nil {
			return store.Job{}, err
		}
		to, err := Transition(j.Status, ev)
		if err != nil {
			return store.Job{}, invalidTransition(j.Status, ev)
		}
		j.Status = to
		if store.JobTerminal(to) {
			now := s.now()
			j.FinishedAt = &now
		}
		if mutate != nil {
			mutate(&j)
		}
		j, err = s.store.Jobs().Update(ctx, j)
		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		if err != nil {
			return store.Job{}, err
		}
		s.publishStatus(ctx, j)
		return j, nil
	}
	return store.Job{}, store.ErrVersionConflict
}

// publishStatus pushes a status change into the progress document so every node's SSE
// subscribers see it (OC-027).
func (s *Service) publishStatus(ctx context.Context, j store.Job) {
	publishStatus(ctx, s.store, j, s.now())
}

func publishStatus(ctx context.Context, st store.Store, j store.Job, now time.Time) {
	raw, _, err := st.Jobs().GetProgress(ctx, j.ID)
	if err != nil {
		raw = nil
	}
	_ = st.Jobs().PutProgress(ctx, j.ID, progress.WithStatus(j.ID, raw, j.Status, now))
}

// Pause asks the worker to drain and checkpoint (running → pausing).
func (s *Service) Pause(ctx context.Context, ws store.WorkspaceID, _, id string) (View, error) {
	j, err := s.transition(ctx, ws, id, EvPause, nil)
	if err != nil {
		return View{}, err
	}
	s.event(ctx, id, "pause", "Pause requested")
	return s.view(ctx, j), nil
}

// Resume modes (OC-029): continue with the frozen specs, or start over with the current ones.
const (
	ResumeOriginal = "original"
	ResumeRestart  = "restart"
)

// Resume queues a paused (or resumable failed) job again; the checkpoint is kept and the run
// uses the job's frozen specs. Mode "restart" instead cancels the job and starts a new one with
// the current mappings (the returned view is the new job).
func (s *Service) Resume(ctx context.Context, ws store.WorkspaceID, userID, id, mode string) (View, error) {
	cur, err := s.store.Jobs().Get(ctx, ws, id)
	if err != nil {
		return View{}, err
	}
	if cur.Status == store.JobFailed && !s.view(ctx, cur).Resumable {
		return View{}, problem.New(http.StatusConflict, problem.CodeJobInvalidTransition, "Can't resume",
			"This job can't continue from where it stopped. Start a new migration instead.")
	}
	switch mode {
	case "", ResumeOriginal:
	case ResumeRestart:
		return s.restart(ctx, ws, userID, cur)
	default:
		return View{}, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid resume mode", `mode must be "original" or "restart".`)
	}
	// Resuming a failed job takes the project's lock back; the store refuses while another
	// job of the project is active.
	j, err := s.transition(ctx, ws, id, EvResume, func(j *store.Job) {
		j.Reason, j.ReasonDetail, j.ClaimedBy, j.HeartbeatAt, j.FinishedAt = "", "", "", nil, nil
	})
	var conflict *store.ConflictError
	if errors.As(err, &conflict) {
		return View{}, ErrJobRunning
	}
	if err != nil {
		return View{}, err
	}
	s.event(ctx, id, "resume", "Resumed; waiting for a worker")
	return s.view(ctx, j), nil
}

// restart replaces a paused or failed job with a new one using the current mappings; the new
// job's pre-flight must pass before the old job is cancelled.
func (s *Service) restart(ctx context.Context, ws store.WorkspaceID, userID string, old store.Job) (View, error) {
	opts, err := ParseOptions(old.Options)
	if err != nil {
		return View{}, err
	}
	opts.Specs, opts.MappingVersions, opts.TargetEmpty = nil, nil, nil
	opts.MappingIDs = old.MappingIDs
	pre, err := s.preflight.Run(ctx, ws, userID, old.ProjectID, opts)
	if err != nil {
		return View{}, err
	}
	if !pre.OK {
		return View{}, problem.New(http.StatusUnprocessableEntity, problem.CodePreflightFailed, "Pre-flight checks failed", failedChecks(pre))
	}
	if old.Status == store.JobPaused {
		if _, err := s.Cancel(ctx, ws, userID, old.ID); err != nil {
			return View{}, err
		}
	}
	ids := make([]string, len(pre.Mappings))
	for i, m := range pre.Mappings {
		ids[i] = m.MappingID
	}
	v, _, err := s.create(ctx, ws, userID, old.ProjectID, opts, ids, pre)
	if err == nil {
		s.event(ctx, old.ID, "restart", "Restarted with the current mappings as job "+v.ID)
	}
	return v, err
}

// Cancel stops the job: at once when queued or paused, else via the worker (→ cancelling).
func (s *Service) Cancel(ctx context.Context, ws store.WorkspaceID, _, id string) (View, error) {
	j, err := s.transition(ctx, ws, id, EvCancel, func(j *store.Job) {
		if j.Status == store.JobCancelled {
			j.Reason = ReasonCancelled
		}
	})
	if err != nil {
		return View{}, err
	}
	s.event(ctx, id, "cancel", "Cancel requested")
	return s.view(ctx, j), nil
}

func (s *Service) event(ctx context.Context, jobID, kind, msg string) {
	_ = s.store.Jobs().AppendEvent(ctx, jobID, store.JobEvent{At: s.now(), Kind: kind, Message: msg})
}

// Events returns the job's timeline (OC-028 AC7).
func (s *Service) Events(ctx context.Context, ws store.WorkspaceID, id string) ([]store.JobEvent, error) {
	return s.store.Jobs().ListEvents(ctx, ws, id)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// Guard reports whether a project has an active job; projects and connections use it to block
// deletes and credential changes during a migration.
type Guard struct{ Store store.Store }

// HasActiveJob implements projects.JobGuard and connections.JobGuard.
func (g Guard) HasActiveJob(ctx context.Context, ws store.WorkspaceID, projectID string) (bool, error) {
	_, err := g.Store.Jobs().ActiveForProject(ctx, ws, projectID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// blockInvalid refuses to start a job whose mappings have validation errors (OC-049 AC6: a
// composed mapping's SOURCE_OUT_OF_SCOPE, BINDING_* and the rest): 422 MAPPING_INVALID listing
// every error. A missing target location isn't one: the pre-flight creates it or reports it.
func (s *Service) blockInvalid(ctx context.Context, ws store.WorkspaceID, projectID string, ids []string) error {
	if s.preflight == nil || s.preflight.Validate == nil {
		return nil
	}
	maps, err := selectMappings(ctx, s.store, ws, projectID, ids)
	if err != nil {
		return err
	}
	var fields []problem.FieldError
	for _, m := range maps {
		res, err := s.preflight.Validate(ctx, ws, projectID, m.ID)
		if err != nil {
			return err
		}
		srcSchema, srcTable := m.Driving()
		for _, i := range res.Issues {
			if i.Severity != mapping.SeverityError || i.Code == mapping.CodeTargetKeyspaceMissing {
				continue
			}
			where := i.NodePath
			if where == "" {
				where = i.Path
			}
			fields = append(fields, problem.FieldError{Field: srcSchema + "." + srcTable + ": " + where, Message: i.Code + ": " + i.Message})
		}
	}
	if len(fields) == 0 {
		return nil
	}
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeMappingInvalid, "Fix the mappings first",
		fmt.Sprintf("%d validation error(s) must be fixed before migrating: %s", len(fields), fields[0].Message))
	p.FieldErrors = fields
	return p
}
