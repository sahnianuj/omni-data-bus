package cbstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/google/uuid"

	"omnidatabus/internal/store"
)

// Jobs live in `jobs` (key = job ID, type "job"). The project's active-job lock is
// uniques::activejob::<projectId>, inserted in the same transaction as the job and removed
// when the job reaches a terminal status (OC-023 AC4). Frequent worker writes (heartbeats,
// checkpoints) are plain CAS replaces; only the terminal transition runs in a transaction.
// A job's timeline is one document per job in `jobs` (key events::<jobId>, type
// "job_events") appended with sub-document operations.

type jobDoc struct {
	Type            string          `json:"type"`
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspaceId"`
	ProjectID       string          `json:"projectId"`
	Status          string          `json:"status"`
	Reason          string          `json:"reason,omitempty"`
	ReasonDetail    string          `json:"reasonDetail,omitempty"`
	Options         json.RawMessage `json:"options"`
	MappingIDs      []string        `json:"mappingIds"`
	Checkpoint      json.RawMessage `json:"checkpoint,omitempty"`
	Progress        json.RawMessage `json:"progress,omitempty"`
	ErrorsTruncated bool            `json:"errorsTruncated,omitempty"`
	ClaimedBy       string          `json:"claimedBy,omitempty"`
	HeartbeatAt     string          `json:"heartbeatAt,omitempty"`
	RetryOf         string          `json:"retryOf,omitempty"`
	CreatedBy       string          `json:"createdBy"`
	CreatedAt       string          `json:"createdAt"`
	UpdatedAt       string          `json:"updatedAt"`
	StartedAt       string          `json:"startedAt,omitempty"`
	FinishedAt      string          `json:"finishedAt,omitempty"`
	Version         int64           `json:"version"`
}

func optTS(s string) *time.Time {
	if s == "" {
		return nil
	}
	t := parseTS(s)
	return &t
}

func fmtOptTS(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTS(*t)
}

func (d jobDoc) toJob() store.Job {
	return store.Job{
		ID: d.ID, WorkspaceID: store.WorkspaceID(d.WorkspaceID), ProjectID: d.ProjectID, Status: d.Status, Reason: d.Reason,
		ReasonDetail: d.ReasonDetail, Options: d.Options, MappingIDs: d.MappingIDs, Checkpoint: d.Checkpoint, Progress: d.Progress,
		ErrorsTruncated: d.ErrorsTruncated, ClaimedBy: d.ClaimedBy, HeartbeatAt: optTS(d.HeartbeatAt), RetryOf: d.RetryOf,
		CreatedBy: d.CreatedBy, CreatedAt: parseTS(d.CreatedAt), UpdatedAt: parseTS(d.UpdatedAt), StartedAt: optTS(d.StartedAt),
		FinishedAt: optTS(d.FinishedAt), Version: d.Version,
	}
}

func jobToDoc(j store.Job) jobDoc {
	return jobDoc{
		Type: "job", ID: j.ID, WorkspaceID: string(j.WorkspaceID), ProjectID: j.ProjectID, Status: j.Status, Reason: j.Reason,
		ReasonDetail: j.ReasonDetail, Options: j.Options, MappingIDs: j.MappingIDs, Checkpoint: j.Checkpoint, Progress: j.Progress,
		ErrorsTruncated: j.ErrorsTruncated, ClaimedBy: j.ClaimedBy, HeartbeatAt: fmtOptTS(j.HeartbeatAt), RetryOf: j.RetryOf,
		CreatedBy: j.CreatedBy, CreatedAt: formatTS(j.CreatedAt), UpdatedAt: formatTS(j.UpdatedAt), StartedAt: fmtOptTS(j.StartedAt),
		FinishedAt: fmtOptTS(j.FinishedAt), Version: j.Version,
	}
}

func activeJobKey(projectID string) string { return "activejob::" + projectID }
func jobEventsKey(jobID string) string     { return "events::" + jobID }
func jobErrorKey(jobID string, seq int64) string {
	return jobID + "::" + strconv.FormatInt(seq, 10)
}

type jobsRepo struct{ s *Store }

// Jobs implements store.Store.
func (s *Store) Jobs() store.Jobs { return jobsRepo{s} }

func (r jobsRepo) Create(ctx context.Context, ws store.WorkspaceID, j store.Job) (store.Job, error) {
	if _, err := projects(r).Get(ctx, ws, j.ProjectID); err != nil {
		return store.Job{}, err
	}
	if j.ID == "" {
		j.ID = uuid.Must(uuid.NewV7()).String()
	}
	now := r.s.clock.Now()
	j.WorkspaceID, j.CreatedAt, j.UpdatedAt, j.Version = ws, now, now, 1
	if j.Status == "" {
		j.Status = store.JobQueued
	}
	doc := jobToDoc(j)
	err := r.s.tx(func(tx *gocb.TransactionAttemptContext) error {
		lock := uniqueDoc{Type: "unique", Kind: "activejob", Owner: j.ID, WorkspaceID: string(ws)}
		if _, err := tx.Insert(r.s.coll("uniques"), activeJobKey(j.ProjectID), lock); err != nil {
			if errors.Is(err, gocb.ErrDocumentExists) {
				return &store.ConflictError{Field: "activeJob"}
			}
			return err
		}
		_, err := tx.Insert(r.s.coll("jobs"), j.ID, doc)
		return err
	})
	if err != nil {
		return store.Job{}, Translate(err, "activeJob")
	}
	return doc.toJob(), nil
}

func (r jobsRepo) getAny(ctx context.Context, id string) (jobDoc, gocb.Cas, error) {
	res, err := r.s.coll("jobs").Get(id, &gocb.GetOptions{Context: ctx})
	if err != nil {
		return jobDoc{}, 0, Translate(err, "")
	}
	var d jobDoc
	if err := res.Content(&d); err != nil {
		return jobDoc{}, 0, err
	}
	if d.Type != "job" {
		return jobDoc{}, 0, store.ErrNotFound
	}
	return d, res.Cas(), nil
}

func (r jobsRepo) Get(ctx context.Context, ws store.WorkspaceID, id string) (store.Job, error) {
	d, _, err := r.getAny(ctx, id)
	if err != nil {
		return store.Job{}, err
	}
	if d.WorkspaceID != string(ws) {
		return store.Job{}, store.ErrNotFound
	}
	return d.toJob(), nil
}

func (r jobsRepo) GetAny(ctx context.Context, id string) (store.Job, error) {
	d, _, err := r.getAny(ctx, id)
	return d.toJob(), err
}

func (r jobsRepo) scan(res *gocb.QueryResult, err error) ([]store.Job, error) {
	if err != nil {
		return nil, Translate(err, "")
	}
	var out []store.Job
	for res.Next() {
		var d jobDoc
		if err := res.Row(&d); err != nil {
			_ = res.Close()
			return nil, err
		}
		out = append(out, d.toJob())
	}
	return out, Translate(res.Err(), "")
}

func (r jobsRepo) List(ctx context.Context, ws store.WorkspaceID, projectID string, page store.Page) ([]store.Job, string, error) {
	page = page.Normalize()
	cursorAt, cursorID, err := store.DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", err
	}
	at := "9999-12-31T23:59:59.999999999Z"
	if page.Cursor != "" {
		at = formatTS(cursorAt)
	}
	params := map[string]any{"workspaceId": string(ws), "cursorAt": at, "cursorId": cursorID, "limit": page.Limit + 1}
	name := "ListJobs"
	if projectID != "" {
		name, params["projectId"] = "ListProjectJobs", projectID
	}
	items, err := r.scan(r.s.query(ctx, name, params))
	if err != nil {
		return nil, "", err
	}
	if len(items) <= page.Limit {
		return items, "", nil
	}
	items = items[:page.Limit]
	last := items[len(items)-1]
	return items, store.EncodeCursor(last.CreatedAt, last.ID), nil
}

func (r jobsRepo) Update(ctx context.Context, j store.Job) (store.Job, error) {
	cur, cas, err := r.getAny(ctx, j.ID)
	if err != nil {
		return store.Job{}, err
	}
	if cur.Version != j.Version {
		return store.Job{}, store.ErrVersionConflict
	}
	j.WorkspaceID, j.ProjectID, j.CreatedAt, j.CreatedBy = store.WorkspaceID(cur.WorkspaceID), cur.ProjectID, parseTS(cur.CreatedAt), cur.CreatedBy
	j.UpdatedAt = r.s.clock.Now()
	j.Version++
	doc := jobToDoc(j)
	if store.JobTerminal(j.Status) == store.JobTerminal(cur.Status) {
		if _, err := r.s.coll("jobs").Replace(j.ID, doc, &gocb.ReplaceOptions{Cas: cas, Context: ctx}); err != nil {
			return store.Job{}, Translate(err, "")
		}
		return doc.toJob(), nil
	}
	// Entering or leaving a terminal status: replace the job and release (or retake) the
	// project's lock atomically.
	revive := !store.JobTerminal(j.Status)
	err = r.s.tx(func(tx *gocb.TransactionAttemptContext) error {
		res, err := tx.Get(r.s.coll("jobs"), j.ID)
		if err != nil {
			return err
		}
		var now jobDoc
		if err := res.Content(&now); err != nil {
			return err
		}
		if now.Version != j.Version-1 {
			return store.ErrVersionConflict
		}
		if _, err := tx.Replace(res, doc); err != nil {
			return err
		}
		if revive {
			lock := uniqueDoc{Type: "unique", Kind: "activejob", Owner: j.ID, WorkspaceID: string(j.WorkspaceID)}
			if _, err := tx.Insert(r.s.coll("uniques"), activeJobKey(j.ProjectID), lock); err != nil {
				if errors.Is(err, gocb.ErrDocumentExists) {
					return &store.ConflictError{Field: "activeJob"}
				}
				return err
			}
			return nil
		}
		lock, err := tx.Get(r.s.coll("uniques"), activeJobKey(j.ProjectID))
		if errors.Is(err, gocb.ErrDocumentNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var u uniqueDoc
		if err := lock.Content(&u); err != nil {
			return err
		}
		if u.Owner == j.ID {
			return tx.Remove(lock)
		}
		return nil
	})
	if err != nil {
		return store.Job{}, Translate(err, "activeJob")
	}
	return doc.toJob(), nil
}

func (r jobsRepo) ListByStatus(ctx context.Context, statuses []string, limit int) ([]store.Job, error) {
	if limit <= 0 {
		limit = 100
	}
	return r.scan(r.s.query(ctx, "ListJobsByStatus", map[string]any{"statuses": statuses, "limit": limit}))
}

func (r jobsRepo) ActiveForProject(ctx context.Context, ws store.WorkspaceID, projectID string) (store.Job, error) {
	res, err := r.s.coll("uniques").Get(activeJobKey(projectID), &gocb.GetOptions{Context: ctx})
	if err != nil {
		return store.Job{}, Translate(err, "")
	}
	var u uniqueDoc
	if err := res.Content(&u); err != nil {
		return store.Job{}, err
	}
	if u.WorkspaceID != string(ws) {
		return store.Job{}, store.ErrNotFound
	}
	return r.Get(ctx, ws, u.Owner)
}

func (r jobsRepo) PutProgress(ctx context.Context, jobID string, doc json.RawMessage) error {
	_, err := r.s.coll("job_progress").Upsert(jobID, doc, &gocb.UpsertOptions{Context: ctx, Transcoder: gocb.NewRawJSONTranscoder()})
	return Translate(err, "")
}

func (r jobsRepo) GetProgress(ctx context.Context, jobID string) (json.RawMessage, uint64, error) {
	res, err := r.s.coll("job_progress").Get(jobID, &gocb.GetOptions{Context: ctx, Transcoder: gocb.NewRawJSONTranscoder()})
	if err != nil {
		return nil, 0, Translate(err, "")
	}
	var raw []byte
	if err := res.Content(&raw); err != nil {
		return nil, 0, err
	}
	return raw, uint64(res.Cas()), nil
}

func (r jobsRepo) PutReport(ctx context.Context, jobID string, doc json.RawMessage) error {
	_, err := r.s.coll("job_reports").Upsert(jobID, doc, &gocb.UpsertOptions{Context: ctx, Transcoder: gocb.NewRawJSONTranscoder()})
	return Translate(err, "")
}

func (r jobsRepo) GetReport(ctx context.Context, jobID string) (json.RawMessage, error) {
	res, err := r.s.coll("job_reports").Get(jobID, &gocb.GetOptions{Context: ctx, Transcoder: gocb.NewRawJSONTranscoder()})
	if err != nil {
		return nil, Translate(err, "")
	}
	var raw []byte
	err = res.Content(&raw)
	return raw, err
}

// AddErrors inserts the records concurrently (bounded), keyed <jobId>::<seq>.
func (r jobsRepo) AddErrors(ctx context.Context, errs []store.JobError, retention time.Duration) error {
	c := r.s.coll("job_errors")
	sem := make(chan struct{}, 32)
	var mu sync.Mutex
	var first error
	var wg sync.WaitGroup
	for _, e := range errs {
		doc := struct {
			Type string `json:"type"`
			store.JobError
			At string `json:"at"`
		}{Type: "job_error", JobError: e, At: formatTS(e.At)}
		sem <- struct{}{}
		wg.Add(1)
		go func(key string) {
			defer func() { <-sem; wg.Done() }()
			_, err := c.Upsert(key, doc, &gocb.UpsertOptions{Context: ctx, Expiry: retention})
			if err != nil {
				mu.Lock()
				if first == nil {
					first = Translate(err, "")
				}
				mu.Unlock()
			}
		}(jobErrorKey(e.JobID, e.Seq))
	}
	wg.Wait()
	return first
}

type jobErrorRow struct {
	store.JobError
	At string `json:"at"`
}

func (e jobErrorRow) toError() store.JobError {
	out := e.JobError
	out.At = parseTS(e.At)
	return out
}

func (r jobsRepo) ListErrors(ctx context.Context, ws store.WorkspaceID, jobID string, f store.JobErrorFilter, page store.Page) ([]store.JobError, string, error) {
	page = page.Normalize()
	after := int64(-1)
	if page.Cursor != "" {
		n, err := strconv.ParseInt(page.Cursor, 10, 64)
		if err != nil {
			return nil, "", store.ErrInvalidCursor
		}
		after = n
	}
	res, err := r.s.query(ctx, "ListJobErrors", map[string]any{
		"workspaceId": string(ws), "jobId": jobID, "after": after, "stage": f.Stage, "code": f.Code,
		"mappingId": f.MappingID, "limit": page.Limit + 1,
	})
	if err != nil {
		return nil, "", Translate(err, "")
	}
	var out []store.JobError
	for res.Next() {
		var row jobErrorRow
		if err := res.Row(&row); err != nil {
			_ = res.Close()
			return nil, "", err
		}
		out = append(out, row.toError())
	}
	if err := res.Err(); err != nil {
		return nil, "", Translate(err, "")
	}
	if len(out) <= page.Limit {
		return out, "", nil
	}
	out = out[:page.Limit]
	return out, strconv.FormatInt(out[len(out)-1].Seq, 10), nil
}

func (r jobsRepo) SummarizeErrors(ctx context.Context, ws store.WorkspaceID, jobID string) ([]store.JobErrorCount, error) {
	res, err := r.s.query(ctx, "SummarizeJobErrors", map[string]any{"workspaceId": string(ws), "jobId": jobID})
	if err != nil {
		return nil, Translate(err, "")
	}
	out := []store.JobErrorCount{}
	for res.Next() {
		var row struct {
			Stage   string      `json:"stage"`
			Code    string      `json:"code"`
			Count   int64       `json:"count"`
			Example jobErrorRow `json:"example"`
		}
		if err := res.Row(&row); err != nil {
			_ = res.Close()
			return nil, err
		}
		out = append(out, store.JobErrorCount{Stage: row.Stage, Code: row.Code, Count: row.Count, Example: row.Example.toError()})
	}
	return out, Translate(res.Err(), "")
}

type jobEvent struct {
	At      string          `json:"at"`
	Kind    string          `json:"kind"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (r jobsRepo) AppendEvent(ctx context.Context, jobID string, e store.JobEvent) error {
	ev := jobEvent{At: formatTS(e.At), Kind: e.Kind, Message: e.Message, Data: e.Data}
	c := r.s.coll("jobs")
	_, err := c.MutateIn(jobEventsKey(jobID), []gocb.MutateInSpec{
		gocb.UpsertSpec("type", "job_events", nil),
		gocb.UpsertSpec("jobId", jobID, nil),
		gocb.ArrayAppendSpec("events", ev, &gocb.ArrayAppendSpecOptions{CreatePath: true}),
	}, &gocb.MutateInOptions{Context: ctx, StoreSemantic: gocb.StoreSemanticsUpsert})
	return Translate(err, "")
}

func (r jobsRepo) ListEvents(ctx context.Context, ws store.WorkspaceID, jobID string) ([]store.JobEvent, error) {
	if _, err := r.Get(ctx, ws, jobID); err != nil {
		return nil, err
	}
	res, err := r.s.coll("jobs").Get(jobEventsKey(jobID), &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return []store.JobEvent{}, nil
	}
	if err != nil {
		return nil, Translate(err, "")
	}
	var d struct {
		Events []jobEvent `json:"events"`
	}
	if err := res.Content(&d); err != nil {
		return nil, err
	}
	out := make([]store.JobEvent, 0, len(d.Events))
	for _, e := range d.Events {
		out = append(out, store.JobEvent{At: parseTS(e.At), Kind: e.Kind, Message: e.Message, Data: e.Data})
	}
	return out, nil
}

type leaseDoc struct {
	Type   string `json:"type"`
	Holder string `json:"holder"`
	Until  string `json:"until"`
}

// AcquireLease uses a lease document whose expiry is the lease end, so a dead holder's lease
// simply disappears; a live holder renews it with CAS.
func (r jobsRepo) AcquireLease(ctx context.Context, name, holder string, ttl time.Duration) (bool, error) {
	c := r.s.coll("leases")
	key := "lease::" + name
	now := r.s.clock.Now()
	doc := leaseDoc{Type: "lease", Holder: holder, Until: formatTS(now.Add(ttl))}
	res, err := c.Get(key, &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		_, err = c.Insert(key, doc, &gocb.InsertOptions{Context: ctx, Expiry: ttl})
		if errors.Is(err, gocb.ErrDocumentExists) {
			return false, nil
		}
		return err == nil, Translate(err, "")
	}
	if err != nil {
		return false, Translate(err, "")
	}
	var cur leaseDoc
	if err := res.Content(&cur); err != nil {
		return false, err
	}
	if cur.Holder != holder && now.Before(parseTS(cur.Until)) {
		return false, nil
	}
	_, err = c.Replace(key, doc, &gocb.ReplaceOptions{Cas: res.Cas(), Context: ctx, Expiry: ttl})
	if errors.Is(err, gocb.ErrCasMismatch) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("renew lease %s: %w", name, Translate(err, ""))
	}
	return true, nil
}
