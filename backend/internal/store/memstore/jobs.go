package memstore

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"omnidatabus/internal/store"
)

type jobsData struct {
	jobs     map[string]store.Job
	active   map[string]string // projectId → jobId
	progress map[string]progressEntry
	reports  map[string]json.RawMessage
	errs     map[string][]store.JobError // jobId → errors (Seq order)
	events   map[string][]store.JobEvent
	leases   map[string]lease
}

type progressEntry struct {
	doc json.RawMessage
	rev uint64
}

type lease struct {
	holder string
	until  time.Time
}

type jobsState struct {
	once sync.Once
	d    jobsData
}

func (s *Store) jobsData() *jobsData {
	s.jobs.once.Do(func() {
		s.jobs.d = jobsData{
			jobs: map[string]store.Job{}, active: map[string]string{}, progress: map[string]progressEntry{}, reports: map[string]json.RawMessage{},
			errs: map[string][]store.JobError{}, events: map[string][]store.JobEvent{}, leases: map[string]lease{},
		}
	})
	return &s.jobs.d
}

// Jobs implements store.Store.
func (s *Store) Jobs() store.Jobs { return jobs{s} }

type jobs struct{ s *Store }

func cloneJob(j store.Job) store.Job {
	j.Options = slices.Clone(j.Options)
	j.Checkpoint = slices.Clone(j.Checkpoint)
	j.Progress = slices.Clone(j.Progress)
	j.MappingIDs = slices.Clone(j.MappingIDs)
	j.External = slices.Clone(j.External)
	return j
}

func (r jobs) Create(_ context.Context, ws store.WorkspaceID, j store.Job) (store.Job, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.project(ws, j.ProjectID); err != nil {
		return store.Job{}, err
	}
	d := s.jobsData()
	if _, busy := d.active[j.ProjectID]; busy && !store.JobTerminal(j.Status) {
		return store.Job{}, &store.ConflictError{Field: "activeJob"}
	}
	if j.ID == "" {
		j.ID = uuid.Must(uuid.NewV7()).String()
	}
	now := s.clock.Now()
	j.WorkspaceID, j.CreatedAt, j.UpdatedAt, j.Version = ws, now, now, 1
	if j.Status == "" {
		j.Status = store.JobQueued
	}
	d.jobs[j.ID] = cloneJob(j)
	if !store.JobTerminal(j.Status) {
		d.active[j.ProjectID] = j.ID
	}
	return cloneJob(j), nil
}

func (r jobs) Get(_ context.Context, ws store.WorkspaceID, id string) (store.Job, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	j, ok := r.s.jobsData().jobs[id]
	if !ok || j.WorkspaceID != ws {
		return store.Job{}, store.ErrNotFound
	}
	return cloneJob(j), nil
}

func (r jobs) GetAny(_ context.Context, id string) (store.Job, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	j, ok := r.s.jobsData().jobs[id]
	if !ok {
		return store.Job{}, store.ErrNotFound
	}
	return cloneJob(j), nil
}

func (r jobs) List(_ context.Context, ws store.WorkspaceID, projectID string, page store.Page) ([]store.Job, string, error) {
	page = page.Normalize()
	cursorAt, cursorID, err := store.DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", err
	}
	r.s.mu.Lock()
	var all []store.Job
	for _, j := range r.s.jobsData().jobs {
		if j.WorkspaceID == ws && (projectID == "" || j.ProjectID == projectID) &&
			(page.Cursor == "" || store.Before(j.CreatedAt, j.ID, cursorAt, cursorID)) {
			all = append(all, cloneJob(j))
		}
	}
	r.s.mu.Unlock()
	sort.Slice(all, func(i, k int) bool { return store.Before(all[k].CreatedAt, all[k].ID, all[i].CreatedAt, all[i].ID) })
	if len(all) <= page.Limit {
		return all, "", nil
	}
	items := all[:page.Limit]
	last := items[len(items)-1]
	return items, store.EncodeCursor(last.CreatedAt, last.ID), nil
}

func (r jobs) Update(_ context.Context, j store.Job) (store.Job, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.jobsData()
	cur, ok := d.jobs[j.ID]
	if !ok {
		return store.Job{}, store.ErrNotFound
	}
	if cur.Version != j.Version {
		return store.Job{}, store.ErrVersionConflict
	}
	if store.JobTerminal(cur.Status) && !store.JobTerminal(j.Status) {
		if other, busy := d.active[cur.ProjectID]; busy && other != j.ID {
			return store.Job{}, &store.ConflictError{Field: "activeJob"}
		}
		d.active[cur.ProjectID] = j.ID
	}
	j.WorkspaceID, j.ProjectID, j.CreatedAt, j.CreatedBy = cur.WorkspaceID, cur.ProjectID, cur.CreatedAt, cur.CreatedBy
	j.UpdatedAt = s.clock.Now()
	j.Version++
	d.jobs[j.ID] = cloneJob(j)
	if store.JobTerminal(j.Status) && d.active[j.ProjectID] == j.ID {
		delete(d.active, j.ProjectID)
	}
	return cloneJob(j), nil
}

func (r jobs) ListByStatus(_ context.Context, statuses []string, limit int) ([]store.Job, error) {
	r.s.mu.Lock()
	var out []store.Job
	for _, j := range r.s.jobsData().jobs {
		if slices.Contains(statuses, j.Status) {
			out = append(out, cloneJob(j))
		}
	}
	r.s.mu.Unlock()
	sort.Slice(out, func(i, k int) bool {
		if out[i].CreatedAt.Equal(out[k].CreatedAt) {
			return out[i].ID < out[k].ID
		}
		return out[i].CreatedAt.Before(out[k].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r jobs) ListClaimable(ctx context.Context, workerLevel, limit int) ([]store.Job, error) {
	all, err := r.ListByStatus(ctx, []string{store.JobQueued}, 0)
	if err != nil {
		return nil, err
	}
	var out []store.Job
	for _, j := range all {
		if j.MinWorkerLevel <= workerLevel {
			out = append(out, j)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r jobs) ActiveForProject(_ context.Context, ws store.WorkspaceID, projectID string) (store.Job, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	d := r.s.jobsData()
	id, ok := d.active[projectID]
	if !ok || d.jobs[id].WorkspaceID != ws {
		return store.Job{}, store.ErrNotFound
	}
	return cloneJob(d.jobs[id]), nil
}

func (r jobs) PutProgress(_ context.Context, jobID string, doc json.RawMessage) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	d := r.s.jobsData()
	e := d.progress[jobID]
	d.progress[jobID] = progressEntry{doc: slices.Clone(doc), rev: e.rev + 1}
	return nil
}

func (r jobs) GetProgress(_ context.Context, jobID string) (json.RawMessage, uint64, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	e, ok := r.s.jobsData().progress[jobID]
	if !ok {
		return nil, 0, store.ErrNotFound
	}
	return slices.Clone(e.doc), e.rev, nil
}

func (r jobs) PutReport(_ context.Context, jobID string, doc json.RawMessage) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.jobsData().reports[jobID] = slices.Clone(doc)
	return nil
}

func (r jobs) GetReport(_ context.Context, jobID string) (json.RawMessage, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	doc, ok := r.s.jobsData().reports[jobID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return slices.Clone(doc), nil
}

func (r jobs) AddErrors(_ context.Context, errs []store.JobError, _ time.Duration) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	d := r.s.jobsData()
	for _, e := range errs {
		d.errs[e.JobID] = append(d.errs[e.JobID], e)
	}
	for id := range d.errs {
		sort.Slice(d.errs[id], func(i, k int) bool { return d.errs[id][i].Seq < d.errs[id][k].Seq })
	}
	return nil
}

func (r jobs) jobIn(ws store.WorkspaceID, jobID string) bool {
	j, ok := r.s.jobsData().jobs[jobID]
	return ok && j.WorkspaceID == ws
}

func matches(e store.JobError, f store.JobErrorFilter) bool {
	return (f.Stage == "" || e.Stage == f.Stage) && (f.Code == "" || e.Code == f.Code) && (f.MappingID == "" || e.MappingID == f.MappingID)
}

func (r jobs) ListErrors(_ context.Context, ws store.WorkspaceID, jobID string, f store.JobErrorFilter, page store.Page) ([]store.JobError, string, error) {
	page = page.Normalize()
	after := int64(-1)
	if page.Cursor != "" {
		n, err := strconv.ParseInt(page.Cursor, 10, 64)
		if err != nil {
			return nil, "", store.ErrInvalidCursor
		}
		after = n
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if !r.jobIn(ws, jobID) {
		return nil, "", store.ErrNotFound
	}
	var out []store.JobError
	for _, e := range r.s.jobsData().errs[jobID] {
		if e.Seq > after && matches(e, f) {
			out = append(out, e)
		}
	}
	if len(out) <= page.Limit {
		return out, "", nil
	}
	out = out[:page.Limit]
	return out, strconv.FormatInt(out[len(out)-1].Seq, 10), nil
}

func (r jobs) SummarizeErrors(_ context.Context, ws store.WorkspaceID, jobID string) ([]store.JobErrorCount, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if !r.jobIn(ws, jobID) {
		return nil, store.ErrNotFound
	}
	idx := map[[2]string]int{}
	var out []store.JobErrorCount
	for _, e := range r.s.jobsData().errs[jobID] {
		k := [2]string{e.Stage, e.Code}
		i, ok := idx[k]
		if !ok {
			idx[k] = len(out)
			out = append(out, store.JobErrorCount{Stage: e.Stage, Code: e.Code, Example: e})
			i = len(out) - 1
		}
		out[i].Count++
	}
	sort.SliceStable(out, func(i, k int) bool { return out[i].Count > out[k].Count })
	return out, nil
}

func (r jobs) AppendEvent(_ context.Context, jobID string, e store.JobEvent) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	d := r.s.jobsData()
	d.events[jobID] = append(d.events[jobID], e)
	return nil
}

func (r jobs) ListEvents(_ context.Context, ws store.WorkspaceID, jobID string) ([]store.JobEvent, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if !r.jobIn(ws, jobID) {
		return nil, store.ErrNotFound
	}
	return slices.Clone(r.s.jobsData().events[jobID]), nil
}

func (r jobs) AcquireLease(_ context.Context, name, holder string, ttl time.Duration) (bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	d := r.s.jobsData()
	now := r.s.clock.Now()
	if l, ok := d.leases[name]; ok && l.holder != holder && now.Before(l.until) {
		return false, nil
	}
	d.leases[name] = lease{holder: holder, until: now.Add(ttl)}
	return true, nil
}
